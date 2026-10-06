// kek cover, as ./kek runs it (cmd_cover): cover-build instruments the
// program, each selected test runs in an instance of its own (-j at a
// time, threads of this process) and writes the probes it hit
// (KEK_COVER_OUT), and cover-report aggregates them. A test's coverage is
// cached by the compiler, the probe table, the seed and the test's trans
// hash (everything it can reach).
use std::collections::HashMap;
use std::fs;
use std::path::Path;
use std::sync::Mutex;
use wasmtime::{Module, Result};

use crate::testcmd::DEFAULT_CLOCK;
use crate::wasm::{self, Kind, RunOpts};
use crate::{Kek, cache, die, flags};

fn cover_usage() {
    eprint!(
        r#"Usage of cover: kek cover [flags] <file|dir>
  -j int
    	parallel test processes (default: number of CPUs)
  -json
    	print the report as JSON
  -lcov string
    	also write an lcov tracefile to this file
  -run string
    	run only tests whose name matches this extended regular expression
  -seed int
    	seed of the mock &Random
"#
    );
}

pub fn cmd_cover(kek: &mut Kek, args: &[String]) -> Result<i32> {
    let f = match flags::parse("json lcov= run= seed# j#", cover_usage, args) {
        Ok(f) => f,
        Err(code) => return Ok(code),
    };
    if f.rest.len() != 1 {
        cover_usage();
        return Ok(2);
    }
    let seed = f.get("seed").unwrap_or("0").to_string();
    let jobs = f
        .get("j")
        .and_then(|j| j.parse::<usize>().ok())
        .filter(|&j| j > 0)
        .unwrap_or_else(flags::ncpu);
    let file = &f.rest[0];
    if !Path::new(file).exists() {
        die!("stat {file}: no such file or directory");
    }
    let tmp = tempfile::Builder::new().prefix("kek-cover-").tempdir()?;
    let dir = tmp.path();
    let a = ["cover-build", file, &dir.to_string_lossy()].map(String::from);
    let r = kek.run_compiler(&a, false)?;
    if r.code != 0 {
        return Ok(r.code);
    }
    let run = f.get("run").unwrap_or("");
    let tests = fs::read_to_string(dir.join("tests.txt")).unwrap_or_default();
    let Some(selected) = flags::select(&tests, run) else {
        die!("kek: -run: invalid regular expression: {run}");
    };
    fs::write(dir.join("selected.txt"), lines(&selected))?;
    fs::create_dir_all(dir.join("ids"))?;
    let mut hits = vec![String::new(); selected.len()];
    if !selected.is_empty() {
        let keys: HashMap<String, String> = fs::read_to_string(dir.join("testkeys.txt"))
            .unwrap_or_default()
            .lines()
            .filter_map(|l| {
                l.split_once('\t')
                    .map(|(a, b)| (a.to_string(), b.to_string()))
            })
            .collect();
        let sites = fs::read(dir.join("sites.json")).unwrap_or_default();
        let wc = cache::dir()?.join("cover").join(format!(
            "{}-{}-{seed}",
            kek.stage_key(),
            cache::sha_hex(&sites, 24)
        ));
        let mut torun = Vec::new();
        for (i, t) in selected.iter().enumerate() {
            let tk = keys.get(t).cloned().unwrap_or_default();
            let cached = (!tk.is_empty())
                .then(|| fs::read_to_string(entry(&wc, &tk)).ok())
                .flatten();
            match cached {
                Some(h) => hits[i] = h,
                None => torun.push((i, t.clone(), tk)),
            }
        }
        if !torun.is_empty() {
            fs::create_dir_all(wc.parent().unwrap())?;
            let engine = wasm::shared_engine(Kind::Program)?;
            let module = Module::from_file(&engine, dir.join("module.wasm"))?;
            let env = vec![
                ("KEK_TEST".to_string(), "1".to_string()),
                ("KEK_TEST_CLOCK".to_string(), DEFAULT_CLOCK.to_string()),
                ("KEK_TEST_SEED".to_string(), seed.clone()),
                ("KEK_TEST_CASES".to_string(), "100".to_string()),
            ];
            let queue = Mutex::new(torun.into_iter());
            let done = Mutex::new(Vec::new());
            let worker = || -> Result<()> {
                loop {
                    let Some((i, t, tk)) = queue.lock().unwrap().next() else {
                        return Ok(());
                    };
                    let ids = dir.join("ids").join(format!("{:05}", i + 1));
                    let mut e = env.clone();
                    e.push(("KEK_COVER_OUT".into(), ids.to_string_lossy().into_owned()));
                    let o = RunOpts {
                        env: e,
                        capture: true,
                        no_stdin: true,
                        timeout: None,
                    };
                    let argv = [
                        dir.join("module.wasm").to_string_lossy().into_owned(),
                        t.clone(),
                    ];
                    let code = wasm::run_with(&engine, &module, &argv, &o)?.code;
                    let st = match code {
                        0 => "ok",
                        1 => "failed",
                        _ => "trapped",
                    };
                    let probes = fs::read_to_string(&ids).unwrap_or_default();
                    let line = format!("{t}\t{st}\t{}\n", probes.trim_end_matches('\n'));
                    if !tk.is_empty() {
                        cache::write_atomic(&entry(&wc, &tk), line.as_bytes())?;
                    }
                    done.lock().unwrap().push((i, line));
                }
            };
            let res: Vec<Result<()>> = std::thread::scope(|s| {
                let hs: Vec<_> = (0..jobs).map(|_| s.spawn(worker)).collect();
                hs.into_iter().map(|h| h.join().unwrap()).collect()
            });
            for r in res {
                r?;
            }
            for (i, line) in done.into_inner().unwrap() {
                hits[i] = line;
            }
        }
    }
    fs::write(dir.join("hits.txt"), hits.concat())?;
    let mut a = vec!["cover-report".to_string()];
    if let Some(l) = f.get("lcov").filter(|l| !l.is_empty()) {
        a.push("-lcov".into());
        a.push(l.to_string());
    }
    if f.bool("json") {
        a.push("-json".into());
    }
    a.push(dir.to_string_lossy().into_owned());
    kek.compiler_cmd(&a)
}

/// The cache entry of a test: <prefix>-<test key>.
fn entry(prefix: &Path, tk: &str) -> std::path::PathBuf {
    let mut s = prefix.as_os_str().to_owned();
    s.push(format!("-{tk}"));
    s.into()
}

pub fn lines(v: &[String]) -> String {
    v.iter().map(|l| format!("{l}\n")).collect()
}
