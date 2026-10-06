// kek mutate, as ./kek runs it (cmd_mutate): mutate-build generates the
// mutants into one test module (a mutant schema), cached with its
// precompilation by the compiler and the contents of the program and of
// the base. A baseline run gives each test's status, its probe hits (ticks)
// and the mutant sites it reaches; then each mutant of this shard runs
// against the passing tests that reach it, cheapest first, until one fails
// (`--batch` instances, -j in parallel). (mutant key, test key) results
// are cached in mutate/pairs-<compiler>.tsv.
use std::collections::HashMap;
use std::fs;
use std::io::Write;
use std::path::Path;
use std::time::Duration;
use wasmtime::{Engine, Module, Result};

use crate::cover::lines;
use crate::testcmd::{DEFAULT_CLOCK, run_batch, text_lines};
use crate::wasm::{self, Kind, RunOpts};
use crate::{Die, Kek, build, cache, die, diff, flags};

fn mutate_usage() {
    eprint!(
        r#"Usage of mutate: kek mutate [flags] <file|dir>
  -base string
    	mutate only the definitions that differ from this program (file or directory)
  -diff string
    	mutate only the definitions changed since this git revision
  -j int
    	parallel test processes (default: number of CPUs)
  -json
    	print the report as JSON
  -merge string
    	report the -results files of the shards (comma-separated) instead of running
  -results string
    	also write this run's raw results to a file (for -merge)
  -run string
    	use only tests whose name matches this extended regular expression
  -shard string
    	run only shard i of n (i/n, 0-based; default from TEST_SHARD_INDEX / TEST_TOTAL_SHARDS)
  -timeout string
    	wall-clock limit of one test process: 2s, 500ms (default 60s); a run with a
    	mutant is also limited to 10x the probe hits of its test without mutants
"#
    );
}

/// timeout_ms: "2s", "500ms" or "2" (seconds) in milliseconds.
fn timeout_ms(s: &str) -> Option<u64> {
    let (n, m) = if let Some(n) = s.strip_suffix("ms") {
        (n, 1)
    } else if let Some(n) = s.strip_suffix('s') {
        (n, 1000)
    } else {
        (s, 1000)
    };
    if n.is_empty() || !n.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    n.parse::<u64>().ok().map(|n| n * m)
}

/// The tab-separated fields of a line.
fn tabs(l: &str) -> Vec<&str> {
    l.split('\t').collect()
}

pub fn cmd_mutate(kek: &mut Kek, args: &[String]) -> Result<i32> {
    let f = match flags::parse(
        "json run= base= diff= timeout= j# shard= results= merge=",
        mutate_usage,
        args,
    ) {
        Ok(f) => f,
        Err(code) => return Ok(code),
    };
    if f.rest.len() != 1 {
        mutate_usage();
        return Ok(2);
    }
    let get = |n: &str| f.get(n).unwrap_or("").to_string();
    let (flag_base, flag_diff, flag_timeout) = (get("base"), get("diff"), get("timeout"));
    let jobs = f
        .get("j")
        .and_then(|j| j.parse::<usize>().ok())
        .filter(|&j| j > 0)
        .unwrap_or_else(flags::ncpu);
    let file = f.rest[0].clone();
    if !Path::new(&file).exists() {
        die!("stat {file}: no such file or directory");
    }
    if !flag_base.is_empty() && !flag_diff.is_empty() {
        die!("kek mutate: -base and -diff are exclusive");
    }
    let tmo = if flag_timeout.is_empty() {
        None
    } else {
        match timeout_ms(&flag_timeout) {
            Some(t) => Some(t),
            None => die!("kek mutate: invalid -timeout {flag_timeout} (use 2s or 500ms)"),
        }
    };
    let sk = kek.stage_key();
    let tmp = tempfile::Builder::new().prefix("kek-mutate-").tempdir()?;
    let dir = tmp.path();
    let mut base = flag_base.clone();
    if !flag_diff.is_empty() {
        let bs = dir.join("base-src");
        fs::create_dir(&bs)?;
        base = diff::extract_rev(&flag_diff, Path::new(&file), &bs)?;
    }
    if !base.is_empty() && !Path::new(&base).exists() {
        die!("stat {base}: no such file or directory");
    }

    // the build (mutant generation, type checks, schema) is cached by the
    // compiler and the contents of the program and of the base
    let mut key = format!("{sk}\npath {file}\n").into_bytes();
    for p in std::iter::once(&file).chain((!base.is_empty()).then_some(&base)) {
        key.extend(b"==\n");
        let pp = Path::new(p);
        if pp.is_dir() {
            let mut files = Vec::new();
            build::prog_files(pp, &mut files)?;
            for f in files {
                key.extend(
                    f.strip_prefix(pp)
                        .unwrap_or(&f)
                        .to_string_lossy()
                        .as_bytes(),
                );
                key.push(0);
                key.extend(fs::read(&f)?);
            }
        } else {
            key.extend(fs::read(pp)?);
        }
    }
    let mroot = cache::dir()?.join("mutate");
    let b = mroot.join(format!("build-{}", cache::sha_hex(&key, 24)));
    fs::create_dir_all(&mroot)?;
    if !b.join("module.wasm").is_file() {
        let t = tempfile::tempdir_in(&mroot)?;
        let mut a = vec![
            "mutate-build".to_string(),
            file.clone(),
            t.path().to_string_lossy().into_owned(),
        ];
        if !base.is_empty() {
            a.push("-base".into());
            a.push(base.clone());
        }
        let r = kek.run_compiler(&a, false)?;
        if r.code != 0 {
            return Ok(r.code);
        }
        cache::put_dir(t, &b)?;
    }
    for n in ["tests.txt", "testkeys.txt", "mutants.txt", "mutants.json"] {
        fs::copy(b.join(n), dir.join(n))?;
    }
    let tests = fs::read_to_string(dir.join("tests.txt"))?;
    let run = get("run");
    let Some(selected) = flags::select(&tests, &run) else {
        die!("kek: -run: invalid regular expression: {run}");
    };
    fs::write(dir.join("selected.txt"), lines(&selected))?;

    // sharding: this run owns the mutants with index % n == i (-shard i/n,
    // or Bazel's TEST_SHARD_INDEX / TEST_TOTAL_SHARDS)
    let flag_shard = get("shard");
    let (si, sn) = if !flag_shard.is_empty() {
        match flag_shard.split_once('/') {
            Some((a, b)) => (a.to_string(), b.to_string()),
            None => (flag_shard.clone(), flag_shard.clone()),
        }
    } else if let Some(n) = std::env::var("TEST_TOTAL_SHARDS")
        .ok()
        .filter(|n| !n.is_empty())
    {
        if let Some(sf) = std::env::var_os("TEST_SHARD_STATUS_FILE").filter(|s| !s.is_empty()) {
            fs::write(sf, "")?;
        }
        (
            std::env::var("TEST_SHARD_INDEX").unwrap_or_else(|_| "0".into()),
            n,
        )
    } else {
        ("0".to_string(), "1".to_string())
    };
    let digits = |s: &str| !s.is_empty() && s.bytes().all(|c| c.is_ascii_digit());
    if !digits(&si) || !digits(&sn) {
        die!("kek mutate: invalid -shard {flag_shard} (use i/n)");
    }
    let (si, sn): (u64, u64) = (si.parse().unwrap_or(u64::MAX), sn.parse().unwrap_or(0));
    if sn < 1 || si >= sn {
        die!("kek mutate: invalid shard {si}/{sn}");
    }
    let mutants = fs::read_to_string(dir.join("mutants.txt"))?;
    let mut mine = Vec::new();
    let mut skipped = String::new();
    for (i, l) in mutants.lines().enumerate() {
        if i as u64 % sn == si {
            mine.push(l.to_string());
        } else {
            skipped.push_str(&format!("{}\tskipped\t\n", tabs(l)[0]));
        }
    }

    let mut baseline = String::new();
    let mut results = String::new();
    let flag_merge = get("merge");
    if !flag_merge.is_empty() {
        // combine the -results files of the shards: a mutant's result is
        // the one of the shard that ran it
        let mut first = true;
        for r in flag_merge.split(',').filter(|r| !r.is_empty()) {
            let Ok(s) = fs::read_to_string(r) else {
                die!("kek mutate: -merge: no such file: {r}");
            };
            for l in s.lines() {
                if let Some(x) = l.strip_prefix("B\t") {
                    if first {
                        baseline.push_str(&format!("{x}\n"));
                    }
                } else if let Some(x) = l.strip_prefix("R\t")
                    && tabs(x).get(1) != Some(&"skipped")
                {
                    results.push_str(&format!("{x}\n"));
                }
            }
            first = false;
        }
    } else if !selected.is_empty() && !mutants.is_empty() {
        let wall = Duration::from_millis(tmo.unwrap_or(60_000));
        let engine = wasm::shared_engine(Kind::Timed)?;
        let module = wasm::precompiled(&engine, &fs::read(b.join("module.wasm"))?)?;
        let opts = RunOpts {
            env: vec![
                ("KEK_TEST".into(), "1".into()),
                ("KEK_TEST_CLOCK".into(), DEFAULT_CLOCK.into()),
                ("KEK_TEST_SEED".into(), "0".into()),
                ("KEK_TEST_CASES".into(), "100".into()),
            ],
            capture: true,
            no_stdin: true,
            timeout: Some(wall),
        };
        let run = |plan: &[String], jobs: usize| run_plan(&engine, &module, dir, plan, jobs, &opts);

        // baseline, in one instance: each test's status, probe hits
        // (ticks) and the mutant sites it reaches
        let plan0: Vec<String> = selected.iter().map(|t| format!("0 {t}")).collect();
        let mut reach = Vec::new();
        let mut t = String::new();
        for l in run(&plan0, 1)? {
            let w: Vec<&str> = l.split_whitespace().collect();
            match w.first().copied() {
                Some("kek-test") => t = w.get(1).unwrap_or(&"").to_string(),
                Some("kek-base") => {
                    let g = |i: usize| w.get(i).copied().unwrap_or("");
                    baseline.push_str(&format!("{}\t{}\t{}\n", g(1), g(2), g(3)));
                    if g(2) == "ok" {
                        reach.push((g(1).to_string(), g(4).to_string()));
                    }
                }
                Some("kek-crash") => {
                    let why = if w.get(1) == Some(&"timeout") {
                        "timeout"
                    } else {
                        "trapped"
                    };
                    baseline.push_str(&format!("{t}\t{why}\t0\n"));
                }
                _ => {}
            }
        }

        // plan: for each mutant of this shard, the passing tests that reach
        // it, cheapest first, each with a probe budget of 10x its baseline
        // ticks + 1000 (a deterministic time limit). Pairs already known
        // from the cache (mutant key x test key -> result) are not run.
        let ticks: HashMap<String, i64> = baseline
            .lines()
            .map(|l| {
                let c = tabs(l);
                (
                    c[0].to_string(),
                    c.get(2).and_then(|x| x.parse().ok()).unwrap_or(0),
                )
            })
            .collect();
        let tk: HashMap<String, String> = fs::read_to_string(dir.join("testkeys.txt"))?
            .lines()
            .filter_map(|l| {
                l.split_once('\t')
                    .map(|(a, b)| (a.to_string(), b.to_string()))
            })
            .collect();
        let pairs_path = mroot.join(format!("pairs-{sk}.tsv"));
        let mut pr: HashMap<(String, String), String> = HashMap::new();
        for l in fs::read_to_string(&pairs_path).unwrap_or_default().lines() {
            let c = tabs(l);
            if c.len() >= 3 {
                pr.insert((c[0].to_string(), c[1].to_string()), c[2].to_string());
            }
        }
        let tick = |t: &str| ticks.get(t).copied().unwrap_or(0);
        let mut by_site: HashMap<String, Vec<String>> = HashMap::new();
        for (t, ids) in &reach {
            for s in ids.split(',').filter(|s| !s.is_empty()) {
                let v = by_site.entry(s.to_string()).or_default();
                // a stable insertion by ticks
                let pos = v
                    .iter()
                    .rposition(|x| tick(x) <= tick(t))
                    .map_or(0, |p| p + 1);
                v.insert(pos, t.clone());
            }
        }
        let keys: HashMap<String, String> = mutants
            .lines()
            .map(|l| {
                let c = tabs(l);
                (c[0].to_string(), c.get(1).unwrap_or(&"").to_string())
            })
            .collect();
        let mut plan = Vec::new();
        for m in &mine {
            let c = tabs(m);
            let (id, mk) = (c[0], c.get(1).copied().unwrap_or(""));
            let Some(ts) = by_site.get(id) else {
                results.push_str(&format!("{id}\tnocov\t\n"));
                continue;
            };
            let mut line = String::new();
            let mut known = None;
            for t in ts {
                let r = pr
                    .get(&(mk.to_string(), tk.get(t).cloned().unwrap_or_default()))
                    .map(|s| s.as_str())
                    .unwrap_or("");
                if r == "survived" {
                    continue;
                }
                if !r.is_empty() {
                    known = Some((r.to_string(), t.clone()));
                    break;
                }
                line.push_str(&format!(" {t}:{}", 10 * tick(t) + 1000));
            }
            match known {
                Some((r, by)) => results.push_str(&format!("{id}\t{r}\t{by}\n")),
                None if line.is_empty() => results.push_str(&format!("{id}\tsurvived\t\n")),
                None => plan.push(format!("{id}{line}")),
            }
        }
        if !plan.is_empty() {
            // results, and the (mutant, test) pairs for the cache: the
            // tests before the one that killed a mutant let it survive
            let mut newpairs = String::new();
            let (mut m, mut t) = (String::new(), String::new());
            let pair = |m: &str, t: &str, r: &str, out: &mut String| {
                if let Some(k) = tk.get(t).filter(|k| !k.is_empty()) {
                    out.push_str(&format!(
                        "{}\t{k}\t{r}\n",
                        keys.get(m).cloned().unwrap_or_default()
                    ));
                }
            };
            for l in run(&plan, jobs)? {
                let w: Vec<&str> = l.split_whitespace().collect();
                let g = |i: usize| w.get(i).copied().unwrap_or("").to_string();
                match w.first().copied() {
                    Some("kek-start") => {
                        m = g(1);
                        t.clear();
                    }
                    Some("kek-test") => {
                        if !t.is_empty() {
                            pair(&m, &t, "survived", &mut newpairs);
                        }
                        t = g(1);
                    }
                    Some("kek-result") => {
                        if g(2) == "survived" {
                            if !t.is_empty() {
                                pair(&m, &t, "survived", &mut newpairs);
                            }
                            results.push_str(&format!("{m}\tsurvived\t\n"));
                        } else {
                            pair(&m, &t, "killed", &mut newpairs);
                            results.push_str(&format!("{m}\tkilled\t{t}\n"));
                        }
                        t.clear();
                    }
                    Some("kek-crash") => {
                        pair(&m, &t, &g(1), &mut newpairs);
                        results.push_str(&format!("{m}\t{}\t{t}\n", g(1)));
                        t.clear();
                    }
                    _ => {}
                }
            }
            // appends of short lines are atomic: concurrent runs may share it
            let mut pf = fs::OpenOptions::new()
                .create(true)
                .append(true)
                .open(&pairs_path)?;
            pf.write_all(newpairs.as_bytes())?;
        }
        results.push_str(&skipped);
    } else if !selected.is_empty() {
        for t in &selected {
            baseline.push_str(&format!("{t}\tok\t0\n"));
        }
    }
    fs::write(dir.join("baseline.txt"), &baseline)?;
    fs::write(dir.join("results.txt"), &results)?;
    let flag_results = get("results");
    if !flag_results.is_empty() {
        let mut s = String::new();
        for l in baseline.lines() {
            s.push_str(&format!("B\t{l}\n"));
        }
        for l in results.lines() {
            s.push_str(&format!("R\t{l}\n"));
        }
        fs::write(&flag_results, s)?;
    }
    let mut a = vec!["mutate-report".to_string()];
    if f.bool("json") {
        a.push("-json".into());
    }
    a.push(dir.to_string_lossy().into_owned());
    kek.compiler_cmd(&a)
}

/// run_plan splits the plan into `jobs` chunks, runs them in parallel
/// (mutate_batch) and returns the combined log.
fn run_plan(
    engine: &Engine,
    module: &Module,
    dir: &Path,
    plan: &[String],
    jobs: usize,
    opts: &RunOpts,
) -> Result<Vec<String>> {
    let n = jobs.min(plan.len()).max(1);
    let mut chunks = vec![Vec::new(); n];
    for (i, l) in plan.iter().enumerate() {
        chunks[i % n].push(l.clone());
    }
    let logs: Vec<Result<Vec<String>>> = std::thread::scope(|s| {
        let hs: Vec<_> = chunks
            .into_iter()
            .enumerate()
            .map(|(c, lines)| s.spawn(move || mutate_batch(engine, module, dir, c, lines, opts)))
            .collect();
        hs.into_iter().map(|h| h.join().unwrap()).collect()
    });
    let mut all = Vec::new();
    for l in logs {
        all.extend(l?);
    }
    Ok(all)
}

fn kek_lines(o: &[u8]) -> Vec<String> {
    text_lines(o)
        .into_iter()
        .filter(|l| l.starts_with("kek-"))
        .collect()
}

/// mutate_batch runs a chunk of the plan through `module --batch`
/// instances (one instance for many (mutant, test) runs) and returns their
/// `kek-` lines. An instance that traps, runs out of its probe budget (exit
/// status 3) or of wall-clock time stops in the middle of a line: that line
/// gets `kek-crash killed|timeout` and the rest of the plan restarts in a
/// new instance.
fn mutate_batch(
    engine: &Engine,
    module: &Module,
    dir: &Path,
    c: usize,
    mut rest: Vec<String>,
    opts: &RunOpts,
) -> Result<Vec<String>> {
    let mut log = Vec::new();
    while !rest.is_empty() {
        let r = run_batch(engine, module, dir, &format!("rest.{c}"), &rest, opts)?;
        log.extend(kek_lines(&r.stdout));
        if r.code == 0 {
            break;
        }
        let n = text_lines(&r.stdout)
            .iter()
            .filter(|l| l.starts_with("kek-start"))
            .count();
        if n == 0 {
            // nothing started: the module itself cannot run
            let e = String::from_utf8_lossy(&r.stderr);
            return Err(Die(format!(
                "kek mutate: the test module failed:\n{}",
                e.trim_end()
            ))
            .into());
        }
        let mut why = if r.code == 3 { "timeout" } else { "killed" };
        if String::from_utf8_lossy(&r.stderr).contains("wasm trap: interrupt") {
            // the wall clock limit is per instance: give the line that was
            // running an instance of its own before calling it a timeout
            let one = [rest[n - 1].clone()];
            let r = run_batch(engine, module, dir, &format!("rest.{c}.one"), &one, opts)?;
            log.extend(kek_lines(&r.stdout));
            why = if r.code == 3
                || String::from_utf8_lossy(&r.stderr).contains("wasm trap: interrupt")
            {
                "timeout"
            } else if r.code != 0 {
                "killed"
            } else {
                ""
            };
        }
        if !why.is_empty() {
            log.push(format!("kek-crash {why}"));
        }
        rest.drain(..n.min(rest.len()));
    }
    Ok(log)
}
