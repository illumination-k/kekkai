// kek: the Kekkai toolchain as a single binary.
//
// The self-hosted compiler (compiler/, a WasmGC + WASI module) is embedded
// and run in-process by wasmtime; this host does what ./kek does around it
// for the commands it supports. ./kek remains the launcher for working on
// the compiler itself (it rebuilds the compiler from the sources).
mod build;
mod cache;
mod cover;
mod daemon;
mod diff;
mod flags;
mod mutate;
mod remote;
mod testcmd;
mod today;
mod wasm;

use std::fmt;
use std::process::exit;
use wasmtime::{Engine, Module, Result};

mod embedded {
    include!(concat!(env!("OUT_DIR"), "/embedded.rs"));
}

/// The repository maintenance commands of ./kek.
const REPO_ONLY: &[&str] = &["bootstrap-check", "bootstrap-update", "stage"];

/// An error whose message is printed as it is (./kek's `die`).
#[derive(Debug)]
pub struct Die(pub String);

impl fmt::Display for Die {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for Die {}

/// die!(...) returns the error Die(format!(...)): the message, exit 1.
#[macro_export]
macro_rules! die {
    ($($t:tt)*) => {
        return Err($crate::Die(format!($($t)*)).into())
    };
}

pub struct Kek {
    compiler: Option<(Engine, Module)>,
}

impl Kek {
    fn new() -> Self {
        Kek { compiler: None }
    }

    /// The name of the compiler in cache keys.
    pub fn stage_key(&self) -> String {
        format!("bin-{}", cache::sha_hex(embedded::COMPILER_WASM, 24))
    }

    fn compiler(&mut self) -> Result<&(Engine, Module)> {
        if self.compiler.is_none() {
            let engine = wasm::shared_engine(wasm::Kind::Compiler)?;
            let module = wasm::precompiled(&engine, embedded::COMPILER_WASM)?;
            self.compiler = Some((engine, module));
        }
        Ok(self.compiler.as_ref().unwrap())
    }

    /// run_compiler runs a compiler command (args without argv[0]),
    /// through the daemon when one is running and free.
    pub fn run_compiler(&mut self, args: &[String], capture: bool) -> Result<wasm::Output> {
        if let Some(out) = daemon::call(self, args, capture)? {
            return Ok(out);
        }
        self.run_compiler_here(args, capture)
    }

    /// run_compiler_here runs a compiler command in this process.
    pub fn run_compiler_here(&mut self, args: &[String], capture: bool) -> Result<wasm::Output> {
        let (engine, module) = self.compiler()?;
        let mut argv = vec!["kek".to_string()];
        argv.extend_from_slice(args);
        wasm::run(engine, module, &argv, capture)
    }

    /// compiler runs a compiler command with the standard streams and
    /// returns its exit status.
    pub fn compiler_cmd(&mut self, args: &[String]) -> Result<i32> {
        Ok(self.run_compiler(args, false)?.code)
    }
}

fn version() -> String {
    let rev = env!("KEK_GIT_REV");
    let rev = if rev.is_empty() {
        String::new()
    } else {
        format!(" ({rev})")
    };
    format!(
        "kek {}{rev}, compiler {}",
        env!("KEK_VERSION"),
        cache::sha_hex(embedded::COMPILER_WASM, 12)
    )
}

fn usage() {
    print!(
        r#"kek — the Kekkai toolchain (single binary)

Usage:
  kek check <file|dir>            type-check (capabilities, effects, transactions)
  kek ir <file|dir>               print the intermediate representation
  kek build [-o dir] [-target d1|do] <file|dir>
                                  compile: a WASI command for #[main], a Worker for #[handler]
  kek run <file|dir> [args...]    compile a #[main] program and run it
  kek fmt [-w|-check] <paths>     format
  kek fix [-w] <paths>            add the `mut` the mutability rules ask for
  kek caps [-json] <file|dir>     capabilities (side effects) of each function
  kek search [-json] '<sig>' [path]
                                  search functions by type
  kek affected [-json] (-base <file|dir> | -diff <rev>) <file|dir>
                                  definitions, tests and build output a change affects
  kek hash [-json] <file|dir>     definition hashes
  kek config                      print ./kekkai.toml as JSON
  kek assure (plan|apply|check) [flags] <file|dir>
                                  the guarantee ledger kekkai.assure.lock
  kek similar [...] [-base path | -diff rev] <file|dir>
                                  duplicate and similar definitions (exit 1 when found)
  kek complexity [...] [-base path | -diff rev] <file|dir>
                                  cognitive / cyclomatic complexity and nesting
  kek merge [-p] [-no-ast] [-path p] [-marker-size n] <ours> <base> <theirs>
                                  three-way merge (the git merge driver)
  kek test [-run re] [-seed n] [-cases n] [-j n] [-json] [-no-cache] [-affected rev]
           [-clock ms] [-net f.json] [-db f.json] <file|dir>
                                  run the #[test] functions with mock capabilities
  kek cover [-json] [-lcov file] [-run re] [-seed n] <file|dir>
                                  line and branch coverage of the tests
  kek mutate [-json] [-run re] [-base <file|dir> | -diff rev] [-timeout 2s] <file|dir>
                                  mutation testing
  kek lsp                         language server on stdin/stdout
  kek daemon start|stop|status|stats
                                  keep the compiler running: parses stay in memory between builds
  kek version                     print the version

Environment:
  KEK_CACHE           cache directory (default $XDG_CACHE_HOME/kek or ~/.cache/kek)
  KEK_BUILD_CACHE=0   disable the build cache
  KEK_TEST_CACHE=0    run every test (kek test -no-cache)
  KEK_TEST_TIMEOUT    wall-clock limit of a kek test batch (e.g. 10s)
  KEK_REMOTE_CACHE    share the action cache over HTTP (Bazel's protocol) or file:// (needs curl)
  KEK_DAEMON=0        do not use a running daemon
  KEK_TODAY           the date kek assure uses (default today)
"#
    );
}

fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    match real_main(args) {
        Ok(code) => exit(code),
        Err(e) => {
            match e.downcast_ref::<Die>() {
                Some(d) => eprintln!("{d}"),
                None => eprintln!("kek: {e:#}"),
            }
            exit(1);
        }
    }
}

fn real_main(args: Vec<String>) -> Result<i32> {
    let cmd = args.first().cloned().unwrap_or_else(|| "help".to_string());
    let rest = if args.is_empty() { &[][..] } else { &args[1..] };
    let mut kek = Kek::new();
    match cmd.as_str() {
        "help" | "-h" | "--help" => {
            usage();
            Ok(0)
        }
        "version" | "-version" | "--version" | "-V" => {
            println!("{}", version());
            Ok(0)
        }
        "build" => build::cmd_build(&mut kek, rest),
        "run" => build::cmd_run(&mut kek, rest),
        "assure" => {
            // the compiler has no clock: pass today's date
            let mut a = vec!["assure".to_string()];
            if let Some((sub, more)) = rest.split_first() {
                let day = std::env::var("KEK_TODAY").unwrap_or_else(|_| today::local_date());
                a.push(sub.clone());
                a.push(format!("-today={day}"));
                a.extend_from_slice(more);
            }
            kek.compiler_cmd(&a)
        }
        "similar" => testcmd::cmd_similar(&mut kek, rest),
        "test" => testcmd::cmd_test(&mut kek, rest, &mut std::io::stdout(), None),
        "cover" => cover::cmd_cover(&mut kek, rest),
        "mutate" => mutate::cmd_mutate(&mut kek, rest),
        "daemon" => daemon::cmd_daemon(&mut kek, rest),
        "__daemon" => daemon::serve(&mut kek, rest),
        "complexity" => diff::with_diff(
            &mut kek,
            "complexity",
            "kek complexity [-json] [-all] [-tests] [-cognitive n] [-cyclomatic n] [-nesting n] [-lines n] [-base path | -diff rev] <file|dir>",
            rest,
        ),
        "affected" => diff::cmd_affected(&mut kek, rest),
        "lsp" => {
            // the language server reads lib/core (go to definition) under
            // the root it is given: the embedded sources, extracted. It
            // reads its standard input for as long as the
            // editor runs: never through the daemon
            let root = extract_core()?;
            Ok(kek
                .run_compiler_here(&["lsp".to_string(), root], false)?
                .code)
        }
        c if REPO_ONLY.contains(&c) => {
            eprintln!("kek {c}: a maintenance command of the repository's ./kek");
            Ok(2)
        }
        _ => kek.compiler_cmd(&args),
    }
}

/// extract_core writes the embedded lib/core into the cache (once per
/// compiler) and returns the root that contains it.
fn extract_core() -> Result<String> {
    let root = cache::dir()?
        .join("root")
        .join(cache::sha_hex(embedded::COMPILER_WASM, 24));
    let core = root.join("lib/core");
    if !core.is_dir() {
        let lib = root.join("lib");
        std::fs::create_dir_all(&lib)?;
        let tmp = tempfile::tempdir_in(&lib)?;
        for (name, data) in embedded::CORE_LIB {
            std::fs::write(tmp.path().join(name), data)?;
        }
        cache::put_dir(tmp, &core)?;
    }
    Ok(root.to_string_lossy().into_owned())
}
