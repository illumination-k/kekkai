// kek test, as ./kek runs it (cmd_test):
//
// test-build -list discovers the tests with their definition hashes
// (tests.tsv). A test's result is a function of the compiler, its trans
// hash, the mock options (-seed, -clock, the contents of -net/-db files)
// and its number of cases, so results are cached under
// test/<options key>/<hash>-<cases>-<name>. Only the uncached tests are
// built (test-build -run) and run, in -j batches of many tests per module
// instance (threads of this process); the results are printed in
// declaration order.
//
// Also kek similar -semantic, which runs the generated equivalence tests.
use std::collections::{HashMap, HashSet};
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};
use std::time::Duration;
use wasmtime::{Engine, Module, Result};

use crate::wasm::{self, Kind, RunOpts};
use crate::{Kek, build, cache, die, diff, flags, remote};

/// The default time of the mock &Clock (2026-01-01, ms since the epoch).
pub const DEFAULT_CLOCK: &str = "1767225600000";

fn test_usage() {
    eprint!(
        r#"Usage of test:
  -affected string
    	run only the tests that a change since this git revision can affect
  -cases int
    	cases of a property test (a test with generated parameters) (default 100)
  -clock int
    	fixed time of the mock &Clock (ms since the epoch) (default 1767225600000)
  -db string
    	JSON file with the initial contents of the mock &Db store: {{"key": "value"}}
  -j int
    	run this many tests at a time (default: the number of CPUs)
  -json
    	print the results as JSON
  -net string
    	JSON file of canned &Net responses: {{"GET url": "body", "POST url": {{"error": "msg"}}}}
  -no-cache
    	run every test, ignoring cached results (also KEK_TEST_CACHE=0)
  -run string
    	run only tests whose name matches this extended regular expression
  -seed int
    	seed of the mock &Random and of the inputs of property tests
"#
    );
}

fn usage_error(msg: &str) -> Result<i32> {
    eprintln!("{msg}");
    test_usage();
    Ok(2)
}

struct TestFlags {
    run: String,
    seed: String,
    clock: String,
    net: String,
    db: String,
    cases: String,
    jobs: usize,
    json: bool,
    nocache: bool,
    affected: String,
}

/// parse_duration: a wall-clock limit as `wasmtime -W timeout` takes it
/// (10s, 500ms, 2m; a bare number is seconds).
pub fn parse_duration(s: &str) -> Option<Duration> {
    let s = s.trim();
    let split = s.find(|c: char| !c.is_ascii_digit()).unwrap_or(s.len());
    let (n, unit) = s.split_at(split);
    let n: u64 = n.parse().ok()?;
    match unit.trim() {
        "ms" => Some(Duration::from_millis(n)),
        "" | "s" => Some(Duration::from_secs(n)),
        "m" | "min" => Some(Duration::from_secs(n * 60)),
        "h" => Some(Duration::from_secs(n * 3600)),
        _ => None,
    }
}

/// How kek similar -semantic runs kek test: its own output, a default time
/// limit, no diagnostics.
#[derive(Default)]
pub struct TestCtx {
    pub timeout_default: Option<String>,
    pub quiet: bool,
}

pub fn cmd_test(
    kek: &mut Kek,
    args: &[String],
    out: &mut dyn Write,
    ctx: Option<&TestCtx>,
) -> Result<i32> {
    let default_ctx = TestCtx::default();
    let ctx = ctx.unwrap_or(&default_ctx);
    let mut f = TestFlags {
        run: String::new(),
        seed: "0".into(),
        clock: DEFAULT_CLOCK.into(),
        net: String::new(),
        db: String::new(),
        cases: "100".into(),
        jobs: 0,
        json: false,
        nocache: false,
        affected: String::new(),
    };
    let mut i = 0;
    while i < args.len() {
        let a = &args[i];
        if a == "--" {
            i += 1;
            break;
        }
        if !a.starts_with('-') || a.len() < 2 {
            break;
        }
        let name = a.strip_prefix('-').unwrap();
        let name = name.strip_prefix('-').unwrap_or(name);
        let (name, val) = match name.split_once('=') {
            Some((n, v)) => (n, Some(v.to_string())),
            None => (name, None),
        };
        match name {
            "h" | "help" => {
                test_usage();
                return Ok(0);
            }
            "json" | "no-cache" => {
                let b = match val.as_deref().unwrap_or("true") {
                    "true" | "1" => true,
                    "false" | "0" => false,
                    v => {
                        return usage_error(&format!(
                            "invalid boolean value \"{v}\" for -{name}: parse error"
                        ));
                    }
                };
                if name == "json" {
                    f.json = b;
                } else {
                    f.nocache = b;
                }
                i += 1;
                continue;
            }
            "run" | "seed" | "clock" | "net" | "db" | "cases" | "j" | "affected" => {}
            _ => return usage_error(&format!("flag provided but not defined: -{name}")),
        }
        let v = match val {
            Some(v) => v,
            None => {
                let Some(v) = args.get(i + 1) else {
                    return usage_error(&format!("flag needs an argument: -{name}"));
                };
                i += 1;
                v.clone()
            }
        };
        match name {
            "seed" | "clock" if !flags::is_int64(&v) => {
                return usage_error(&format!(
                    "invalid value \"{v}\" for flag -{name}: parse error"
                ));
            }
            "cases" | "j" if !flags::is_count(&v) => {
                return usage_error(&format!(
                    "invalid value \"{v}\" for flag -{name}: want a positive integer"
                ));
            }
            _ => {}
        }
        match name {
            "run" => f.run = v,
            "seed" => f.seed = v,
            "clock" => f.clock = v,
            "net" => f.net = v,
            "db" => f.db = v,
            "cases" => f.cases = v,
            "j" => f.jobs = v.parse().unwrap(),
            _ => f.affected = v,
        }
        i += 1;
    }
    let rest = &args[i..];
    if rest.len() != 1 {
        die!("kek test: expected exactly one .kek file");
    }
    let file = rest[0].clone();
    if !Path::new(&file).exists() {
        die!("stat {file}: no such file or directory");
    }
    if f.clock.trim_start_matches('+') == "0" {
        f.clock = DEFAULT_CLOCK.into();
    }
    if std::env::var("KEK_TEST_CACHE").is_ok_and(|v| v == "0") {
        f.nocache = true;
    }
    if f.jobs == 0 {
        f.jobs = flags::ncpu();
    }
    run_tests(kek, &f, &file, out, ctx)
}

/// compiler runs a compiler command; with ctx.quiet its output is dropped.
fn compiler(kek: &mut Kek, args: &[String], ctx: &TestCtx) -> Result<wasm::Output> {
    kek.run_compiler(args, ctx.quiet)
}

/// One test of the plan (a line of tests.tsv).
struct Planned {
    name: String,
    hash: String,
    caps: String,
    /// the cache key: <hash>-<cases>-<name>
    key: String,
}

fn run_tests(
    kek: &mut Kek,
    f: &TestFlags,
    file: &str,
    out: &mut dyn Write,
    ctx: &TestCtx,
) -> Result<i32> {
    let cache_dir = cache::dir()?;
    let tmp = tempfile::Builder::new().prefix("kek-test-").tempdir()?;
    let dir = tmp.path();

    // the test list (names, hashes, parameters) is an action keyed by the
    // program's files: unchanged sources need no compiler run
    let lists = ["tests.txt", "tests.tsv", "tests.json"];
    let ld =
        cache_dir
            .join("test-list")
            .join(build::src_digest(kek, file, &["test-list".to_string()])?);
    remote::action_fetch(&ld)?;
    if ld.join("ok").is_file() {
        for n in lists {
            fs::copy(ld.join(n), dir.join(n))?;
        }
    } else {
        let a = ["test-build", file, &dir.to_string_lossy(), "-list"].map(String::from);
        let r = compiler(kek, &a, ctx)?;
        if r.code != 0 {
            return Ok(r.code);
        }
        fs::create_dir_all(ld.parent().unwrap())?;
        let t = tempfile::tempdir_in(ld.parent().unwrap())?;
        for n in lists {
            if dir.join(n).is_file() {
                fs::copy(dir.join(n), t.path().join(n))?;
            }
        }
        remote::action_put(t, &ld)?;
    }
    // errors in the options are reported after the program's diagnostics
    for p in [&f.net, &f.db] {
        if !p.is_empty() && !Path::new(p).is_file() {
            die!("open {p}: no such file or directory");
        }
    }
    let tests = fs::read_to_string(dir.join("tests.txt"))?;
    let Some(mut selected) = flags::select(&tests, &f.run) else {
        die!("kek test: -run: invalid regular expression: {}", f.run);
    };
    if !f.affected.is_empty() {
        // only the tests a change since the git revision can affect
        let bs = dir.join("base-src");
        fs::create_dir(&bs)?;
        let base = diff::extract_rev(&f.affected, Path::new(file), &bs)?;
        let a = ["affected", "-tests_only", "-base", &base, file].map(String::from);
        let r = kek.run_compiler(&a, true)?;
        if !ctx.quiet {
            std::io::stderr().write_all(&r.stderr)?;
        }
        if r.code != 0 {
            return Ok(r.code);
        }
        let aff: HashSet<String> = String::from_utf8_lossy(&r.stdout)
            .lines()
            .map(String::from)
            .collect();
        selected.retain(|t| aff.contains(t));
    }
    let count = selected.len();
    if count == 0 {
        if f.json {
            writeln!(
                out,
                "{{\"file\":{},\"passed\":0,\"failed\":0,\"cached\":0,\"tests\":[]}}",
                esc(file)
            )?;
        } else {
            writeln!(out, "{file}: no tests")?;
        }
        return Ok(0);
    }

    // the cache directory of these options
    let mut opts = format!(
        "kek-test-1\n{}\n{}\n{}\nnet\n",
        kek.stage_key(),
        f.seed,
        f.clock
    )
    .into_bytes();
    if !f.net.is_empty() {
        opts.extend(fs::read(&f.net)?);
    }
    opts.extend(b"\ndb\n");
    if !f.db.is_empty() {
        opts.extend(fs::read(&f.db)?);
    }
    let tc = cache_dir.join("test").join(cache::sha_hex(&opts, 24));

    // plan: the selected tests in declaration order
    let sel: HashSet<&str> = selected.iter().map(|s| s.as_str()).collect();
    let cases: i64 = f.cases.parse().unwrap_or(100);
    let mut plan = Vec::new();
    for l in fs::read_to_string(dir.join("tests.tsv"))?.lines() {
        let c: Vec<&str> = l.split('\t').collect();
        let name = c.first().copied().unwrap_or("");
        if !sel.contains(name) {
            continue;
        }
        let np = awk_num(c.get(2).copied().unwrap_or(""));
        let mut n = awk_num(c.get(3).copied().unwrap_or(""));
        if np == 0 {
            n = 1;
        } else if n <= 0 {
            n = cases;
        }
        let hash = c.get(1).copied().unwrap_or("").to_string();
        plan.push(Planned {
            key: format!("{hash}-{n}-{name}"),
            name: name.to_string(),
            hash,
            caps: c.get(4).copied().unwrap_or("").to_string(),
        });
    }
    if !f.nocache {
        for p in &plan {
            remote::file_fetch(&tc.join(&p.key))?;
        }
    }
    let cached: HashMap<&str, Vec<String>> = if f.nocache {
        HashMap::new()
    } else {
        plan.iter()
            .filter_map(|p| {
                let s = fs::read_to_string(tc.join(&p.key)).ok()?;
                let lines: Vec<String> = s.lines().map(String::from).collect();
                (!lines.is_empty()).then_some((p.name.as_str(), lines))
            })
            .collect()
    };
    let to_run: Vec<String> = plan
        .iter()
        .filter(|p| !cached.contains_key(p.name.as_str()))
        .map(|p| p.name.clone())
        .collect();

    let timeout = match std::env::var("KEK_TEST_TIMEOUT")
        .ok()
        .filter(|t| !t.is_empty())
    {
        Some(t) => Some(t),
        None => ctx.timeout_default.clone(),
    };
    let timeout = match timeout {
        Some(t) => match parse_duration(&t) {
            Some(d) => Some(d),
            None => die!("kek test: invalid KEK_TEST_TIMEOUT {t} (use 10s or 500ms)"),
        },
        None => None,
    };
    let mut env = vec![
        ("KEK_TEST".to_string(), "1".to_string()),
        ("KEK_TEST_CLOCK".to_string(), f.clock.clone()),
        ("KEK_TEST_SEED".to_string(), f.seed.clone()),
        ("KEK_TEST_CASES".to_string(), f.cases.clone()),
    ];
    if !f.net.is_empty() {
        env.push(("KEK_TEST_NET".into(), f.net.clone()));
    }
    if !f.db.is_empty() {
        env.push(("KEK_TEST_DB".into(), f.db.clone()));
    }
    let mut module = None;
    if !to_run.is_empty() {
        let mut a = vec![
            "test-build".to_string(),
            file.to_string(),
            dir.to_string_lossy().into_owned(),
            "-run".into(),
        ];
        a.extend(to_run.iter().cloned());
        let r = compiler(kek, &a, ctx)?;
        if r.code != 0 {
            return Ok(r.code);
        }
        let engine = wasm::shared_engine(if timeout.is_some() {
            Kind::Timed
        } else {
            Kind::Program
        })?;
        let m = Module::from_file(&engine, dir.join("module.wasm"))?;
        if !f.net.is_empty() || !f.db.is_empty() {
            // the -net / -db files are checked before the tests run
            let o = RunOpts {
                env: env.clone(),
                timeout,
                ..Default::default()
            };
            let argv = [
                dir.join("module.wasm").to_string_lossy().into_owned(),
                String::new(),
            ];
            if wasm::run_with(&engine, &m, &argv, &o)?.code != 0 {
                return Ok(1);
            }
        }
        module = Some((engine, m));
    }
    if !f.json {
        if count == 1 {
            writeln!(out, "running 1 test from {file}")?;
        } else {
            writeln!(out, "running {count} tests from {file}")?;
        }
        out.flush()?;
    }
    let results = match &module {
        Some((engine, m)) => {
            let o = RunOpts {
                env,
                capture: true,
                no_stdin: true,
                timeout,
            };
            run_batches(engine, m, dir, &to_run, f.jobs, &o)?
        }
        None => HashMap::new(),
    };

    // report in declaration order: cached results from tc, new ones from
    // the batch runs (added to the cache)
    if !f.nocache {
        fs::create_dir_all(&tc)?;
    }
    let (mut passed, mut failed, mut ncached) = (0, 0, 0);
    let mut objs = Vec::new();
    let mut new_entries = Vec::new();
    for p in &plan {
        let (st, lines, was_cached) = match cached.get(p.name.as_str()) {
            Some(l) => {
                ncached += 1;
                (l[0].clone(), l[1..].to_vec(), true)
            }
            None => {
                let (st, body) = match results.get(&p.name) {
                    Some(r) => (
                        r.st.clone(),
                        r.body.lines().map(String::from).collect::<Vec<_>>(),
                    ),
                    None => ("error".to_string(), Vec::new()),
                };
                if st == "error" {
                    ("trapped".to_string(), body, false)
                } else {
                    if !f.nocache {
                        new_entries.push((p.key.clone(), st.clone(), body.clone()));
                    }
                    (st, body, false)
                }
            }
        };
        if st == "ok" {
            passed += 1;
        } else {
            failed += 1;
        }
        if f.json {
            objs.push(test_json(p, &st, was_cached, &lines));
        } else {
            for (i, l) in lines.iter().enumerate() {
                let note = if i == 0 && was_cached {
                    " (cached)"
                } else {
                    ""
                };
                writeln!(out, "{l}{note}")?;
            }
        }
    }
    if f.json {
        writeln!(
            out,
            "{{\"file\":{},\"passed\":{passed},\"failed\":{failed},\"cached\":{ncached},\"tests\":[{}]}}",
            esc(file),
            objs.join(",")
        )?;
    } else {
        let note = if ncached > 0 {
            format!(" ({ncached} cached)")
        } else {
            String::new()
        };
        writeln!(out)?;
        let verdict = if failed == 0 { "ok" } else { "FAILED" };
        writeln!(
            out,
            "test result: {verdict}. {passed} passed; {failed} failed{note}"
        )?;
    }
    out.flush()?;
    for (key, st, body) in new_entries {
        let mut s = format!("{st}\n");
        for l in body {
            s.push_str(&l);
            s.push('\n');
        }
        let path = tc.join(key);
        cache::write_atomic(&path, s.as_bytes())?;
        remote::file_push(&path)?;
    }
    Ok(if failed > 0 { 1 } else { 0 })
}

/// awk's string-to-number: the leading integer, 0 when none.
fn awk_num(s: &str) -> i64 {
    let s = s.trim_start();
    let end = s
        .char_indices()
        .find(|&(i, c)| !(c.is_ascii_digit() || (i == 0 && (c == '-' || c == '+'))))
        .map_or(s.len(), |(i, _)| i);
    s[..end].parse().unwrap_or(0)
}

/// A JSON string literal, as ./kek's report escapes it.
pub fn esc(s: &str) -> String {
    let mut o = String::from("\"");
    for c in s.chars() {
        match c {
            '\\' => o.push_str("\\\\"),
            '"' => o.push_str("\\\""),
            '\t' => o.push_str("\\t"),
            '\r' => o.push_str("\\r"),
            c if (c as u32) >= 1 && (c as u32) < 32 => o.push_str(&format!("\\u{:04x}", c as u32)),
            c => o.push(c),
        }
    }
    o.push('"');
    o
}

fn test_json(p: &Planned, st: &str, cached: bool, out: &[String]) -> String {
    let mut ms = "null".to_string();
    if let Some(first) = out.first()
        && let Some(m) = ms_of(first)
    {
        ms = m;
    }
    let mut ce = "null".to_string();
    let mut lines = Vec::new();
    for l in out.iter().skip(1) {
        let line = l.strip_prefix("    ").unwrap_or(l);
        lines.push(esc(line));
        if let Some(c) = line.strip_prefix("counterexample: ") {
            ce = esc(c);
        }
    }
    let caps: Vec<String> = if p.caps.is_empty() {
        Vec::new()
    } else {
        p.caps.split(',').map(esc).collect()
    };
    format!(
        "{{\"name\":{},\"status\":\"{st}\",\"cached\":{cached},\"pure\":{},\"caps\":[{}],\"hash\":{},\"ms\":{ms},\"output\":[{}],\"counterexample\":{ce}}}",
        esc(&p.name),
        caps.is_empty(),
        caps.join(","),
        esc(&p.hash),
        lines.join(",")
    )
}

/// The time of a test's first line: `...; 1.23ms)`.
fn ms_of(line: &str) -> Option<String> {
    let s = line.strip_suffix("ms)")?;
    let i = s.rfind("; ")?;
    let num = &s[i + 2..];
    let (a, b) = num.split_once('.')?;
    let digits = |x: &str| !x.is_empty() && x.bytes().all(|c| c.is_ascii_digit());
    (digits(a) && digits(b)).then(|| num.to_string())
}

/// The result of a test run in a batch.
struct TestResult {
    st: String,
    body: String,
}

/// run_batches runs the tests in `jobs` batches (round robin), in parallel,
/// and returns each test's status (ok, failed or trapped) and output.
fn run_batches(
    engine: &Engine,
    module: &Module,
    dir: &Path,
    tests: &[String],
    jobs: usize,
    opts: &RunOpts,
) -> Result<HashMap<String, TestResult>> {
    let n = jobs.min(tests.len()).max(1);
    let mut chunks = vec![Vec::new(); n];
    for (i, t) in tests.iter().enumerate() {
        chunks[i % n].push(format!("0 {t}"));
    }
    let logs: Vec<Result<Vec<String>>> = std::thread::scope(|s| {
        let hs: Vec<_> = chunks
            .into_iter()
            .enumerate()
            .map(|(c, lines)| s.spawn(move || test_batch(engine, module, dir, c, lines, opts)))
            .collect();
        hs.into_iter().map(|h| h.join().unwrap()).collect()
    });
    let mut all = Vec::new();
    for l in logs {
        all.extend(l?);
    }
    Ok(parse_log(&all))
}

/// run_batch runs the module with `--batch` on the plan `lines` (written to
/// <dir>/rest.<c>, KEK_BATCH).
pub fn run_batch(
    engine: &Engine,
    module: &Module,
    dir: &Path,
    name: &str,
    lines: &[String],
    opts: &RunOpts,
) -> Result<wasm::Output> {
    let rest: PathBuf = dir.join(name);
    let mut s = lines.join("\n");
    s.push('\n');
    fs::write(&rest, s)?;
    let mut o = opts.clone();
    o.env
        .push(("KEK_BATCH".into(), rest.to_string_lossy().into_owned()));
    let argv = [
        dir.join("module.wasm").to_string_lossy().into_owned(),
        "--batch".into(),
    ];
    wasm::run_with(engine, module, &argv, &o)
}

pub fn text_lines(b: &[u8]) -> Vec<String> {
    String::from_utf8_lossy(b)
        .lines()
        .map(String::from)
        .collect()
}

/// test_batch runs the tests of a chunk (lines `0 name`) with `module
/// --batch`, many tests per instance, and returns the whole log. A test that
/// traps or times out ends its instance: `kek-crash` and its stderr
/// (`kek-err` lines) follow, and the remaining tests run in a new instance.
/// The wall-clock limit is per instance, so a test interrupted after
/// others ran in its instance first runs again alone.
fn test_batch(
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
        let o = text_lines(&r.stdout);
        let e = String::from_utf8_lossy(&r.stderr).into_owned();
        let n = o.iter().filter(|l| l.starts_with("kek-start")).count();
        if r.code != 0 && n > 1 && e.contains("wasm trap: interrupt") {
            let mut k = 0;
            for l in &o {
                if l.starts_with("kek-start") {
                    k += 1;
                }
                if k >= n {
                    break;
                }
                log.push(l.clone());
            }
            rest.drain(..n - 1);
            continue;
        }
        log.extend(o);
        if r.code == 0 {
            break;
        }
        let n = n.max(1);
        log.push("kek-crash".into());
        for l in e.lines() {
            log.push(format!("kek-err {l}"));
        }
        rest.drain(..n.min(rest.len()));
    }
    Ok(log)
}

/// parse_log turns the batch logs into each test's status and output, as one
/// process per test would have printed them.
fn parse_log(lines: &[String]) -> HashMap<String, TestResult> {
    #[derive(Default)]
    struct Cur {
        t: String,
        body: String,
        st: String,
        crashed: bool,
        trap: String,
        last: String,
        other: String,
        panic: String,
    }
    fn finish(c: &mut Cur, res: &mut HashMap<String, TestResult>) {
        if c.t.is_empty() {
            return;
        }
        let t = c.t.clone();
        if c.crashed && !c.panic.is_empty() {
            c.body
                .push_str(&format!("test {t} ... FAILED (panicked)\n{}", c.panic));
            if !c.last.is_empty() {
                c.body.push_str(&format!("    last input: {}\n", c.last));
            }
        } else if c.crashed {
            let trap = if c.trap.is_empty() { &c.other } else { &c.trap };
            c.body.push_str(&format!(
                "test {t} ... FAILED (trapped)\n    trap: {trap}\n"
            ));
            if !c.last.is_empty() {
                c.body.push_str(&format!("    last input: {}\n", c.last));
            }
        }
        res.insert(
            t,
            TestResult {
                st: c.st.clone(),
                body: std::mem::take(&mut c.body),
            },
        );
        c.t.clear();
    }
    let mut res = HashMap::new();
    let mut c = Cur::default();
    for l in lines {
        let field = |i: usize| l.split_whitespace().nth(i).unwrap_or("").to_string();
        if l.starts_with("kek-start ") {
            continue;
        }
        if l.starts_with("kek-test ") {
            finish(&mut c, &mut res);
            c = Cur {
                t: field(1),
                st: "error".into(),
                ..Default::default()
            };
            continue;
        }
        if l.starts_with("kek-base ") {
            c.st = field(2);
            continue;
        }
        if l.starts_with("kek-crash") {
            c.crashed = true;
            c.st = "trapped".into();
            continue;
        }
        if let Some(e) = l.strip_prefix("kek-err ") {
            // a failed assert! / panic! (__panic_report in lib/prelude/test.kek)
            if e.starts_with("panicked at ") {
                c.panic = format!("    {e}\n");
            } else if !c.panic.is_empty() {
                c.panic.push_str(&format!("    {e}\n"));
            } else if let Some(i) = e.rfind("wasm trap: ") {
                if c.trap.is_empty() {
                    c.trap = e[i + "wasm trap: ".len()..].to_string();
                }
            } else if let Some(x) = e.strip_prefix("kek-case: ") {
                c.last = x.to_string();
            } else if !e.is_empty() {
                c.other = e.to_string();
            }
            continue;
        }
        if !c.crashed {
            c.body.push_str(l);
            c.body.push('\n');
        }
    }
    finish(&mut c, &mut res);
    res
}

/// kek similar: with -semantic, the pairs of functions of the semantic
/// kind are first compared as property tests (each compares the two
/// functions' results on generated inputs), and the pairs that agreed on
/// every input are passed to the compiler (-equiv).
pub fn cmd_similar(kek: &mut Kek, args: &[String]) -> Result<i32> {
    const USAGE: &str =
        "kek similar [-json] [-threshold pct] [-all] [-tests] [-base path | -diff rev] <file|dir>";
    let mut sem = false;
    let mut sem_flags = Vec::new();
    let mut sem_path = String::new();
    let mut rest = Vec::new();
    for a in args {
        match a.as_str() {
            "-semantic" | "--semantic" => sem = true,
            _ => {
                match a.as_str() {
                    "-all" | "--all" | "-tests" | "--tests" => sem_flags.push(a.clone()),
                    x if x.starts_with('-') => {}
                    _ => sem_path = a.clone(),
                }
                rest.push(a.clone());
            }
        }
    }
    if !sem {
        return diff::with_diff(kek, "similar", USAGE, args);
    }
    if sem_path.is_empty() {
        die!("usage: kek similar -semantic [flags] <file|dir>");
    }
    let sd = tempfile::Builder::new().prefix("kek-semantic-").tempdir()?;
    let sdir = sd.path();
    fs::create_dir(sdir.join("prog"))?;
    let mut a = vec![
        "similar".to_string(),
        "-semantic_plan".into(),
        sdir.to_string_lossy().into_owned(),
    ];
    a.extend(sem_flags);
    a.push(sem_path);
    if kek.run_compiler(&a, false)?.code != 0 {
        return Ok(2);
    }
    let equiv = sdir.join("equiv.txt");
    let mut eq = String::new();
    let pairs = fs::read_to_string(sdir.join("pairs.tsv")).unwrap_or_default();
    if !pairs.is_empty() {
        let mut result = Vec::new();
        let targs = [
            "-json",
            "-run",
            "^kek_sim_eq_[0-9]+$",
            &sdir.join("prog").to_string_lossy(),
        ]
        .map(String::from);
        let ctx = TestCtx {
            timeout_default: Some("10s".into()),
            quiet: true,
        };
        let _ = cmd_test(kek, &targs, &mut result, Some(&ctx));
        let result = String::from_utf8_lossy(&result);
        for l in pairs.lines() {
            let c: Vec<&str> = l.split('\t').collect();
            if c.len() < 3 {
                continue;
            }
            if result.contains(&format!(
                "\"name\":\"kek_sim_eq_{}\",\"status\":\"ok\"",
                c[0]
            )) {
                eq.push_str(&format!("{}\t{}\n", c[1], c[2]));
            }
        }
    }
    fs::write(&equiv, eq)?;
    let mut full = vec!["-equiv".to_string(), equiv.to_string_lossy().into_owned()];
    full.extend(rest);
    diff::with_diff(kek, "similar", USAGE, &full)
}
