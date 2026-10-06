// Running WasmGC + WASI (preview 1) modules: the compiler and the programs
// it builds, as `wasmtime run -W gc=y,function-references=y --dir /` does
// in ./kek.
//
// The compiler is precompiled once per (compiler, engine) into the cache
// (modules/<key>.cwasm); programs are compiled on each run, except the
// test modules of kek mutate (cached with their build).
use std::hash::{Hash, Hasher};
use std::path::Path;
use std::sync::{Once, OnceLock};
use std::time::Duration;
use wasmtime::error::Context;
use wasmtime::{Collector, Config, Engine, Linker, Module, Result, Store};
use wasmtime_wasi::p2::pipe::MemoryOutputPipe;
use wasmtime_wasi::{FsPerms, I32Exit, WasiCtxBuilder};

use crate::cache;

/// The exit status of a module that trapped (wasmtime's: 128 + SIGABRT).
pub const EXIT_TRAP: i32 = 134;

/// The resolution of the wall-clock limit (`RunOpts::timeout`).
const TICK: Duration = Duration::from_millis(10);

#[derive(Clone, Copy, PartialEq)]
pub enum Kind {
    /// The compiler: a large initial GC heap (reserved, not touched).
    Compiler,
    /// A program built by the compiler.
    Program,
    /// A program run with a wall-clock limit (epoch interruption compiled
    /// in, as `wasmtime -W timeout` needs).
    Timed,
}

/// The garbage collector: the copying collector is several times faster
/// than wasmtime's default (deferred reference counting) on the compiler
/// and on programs alike.
fn engine(kind: Kind) -> Result<Engine> {
    let mut c = Config::new();
    c.wasm_gc(true)
        .wasm_function_references(true)
        .collector(Collector::Copying);
    match kind {
        Kind::Compiler => {
            c.gc_heap_initial_size(1 << 30);
        }
        Kind::Program => {}
        Kind::Timed => {
            c.epoch_interruption(true);
        }
    }
    Engine::new(&c)
}

/// The engine of a kind, one for the whole process.
pub fn shared_engine(kind: Kind) -> Result<Engine> {
    static ENGINES: [OnceLock<Engine>; 3] = [OnceLock::new(), OnceLock::new(), OnceLock::new()];
    let slot = &ENGINES[kind as usize];
    if let Some(e) = slot.get() {
        return Ok(e.clone());
    }
    let e = engine(kind)?;
    Ok(slot.get_or_init(|| e).clone())
}

/// precompiled loads `wasm` through the cache of precompiled modules
/// (modules/<key>.cwasm, keyed by the module and the engine).
pub fn precompiled(engine: &Engine, wasm: &[u8]) -> Result<Module> {
    let mut h = std::collections::hash_map::DefaultHasher::new();
    engine.precompile_compatibility_hash().hash(&mut h);
    let key = format!("{}-{:016x}", cache::sha_hex(wasm, 24), h.finish());
    let path = cache::dir()?.join("modules").join(format!("{key}.cwasm"));
    if path.is_file() {
        // SAFETY: the file is our own precompilation of `wasm` for this
        // engine configuration (the key covers both).
        if let Ok(m) = unsafe { Module::deserialize_file(engine, &path) } {
            return Ok(m);
        }
    }
    let bytes = engine
        .precompile_module(wasm)
        .context("compiling a module")?;
    cache::write_atomic(&path, &bytes)?;
    // SAFETY: produced by precompile_module of this engine just above.
    unsafe { Module::deserialize(engine, &bytes) }
}

#[derive(Default, Clone)]
pub struct RunOpts {
    /// Environment variables besides PWD.
    pub env: Vec<(String, String)>,
    /// Return stdout and stderr instead of inheriting them.
    pub capture: bool,
    /// Do not read the standard input (parallel runs).
    pub no_stdin: bool,
    /// A wall-clock limit (a module of the Kind::Timed engine): the module
    /// traps with `wasm trap: interrupt`.
    pub timeout: Option<Duration>,
}

pub struct Output {
    pub code: i32,
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
}

/// The ticker of the wall-clock limits: one thread advancing the epoch of
/// the timed engine.
fn start_ticker(engine: &Engine) {
    static TICKER: Once = Once::new();
    let e = engine.clone();
    TICKER.call_once(move || {
        std::thread::spawn(move || {
            loop {
                std::thread::sleep(TICK);
                e.increment_epoch();
            }
        });
    });
}

/// run <module> with argv <args> (args[0] is the program name), the whole
/// file system preopened at / and PWD set to the working directory.
pub fn run(engine: &Engine, module: &Module, args: &[String], capture: bool) -> Result<Output> {
    let opts = RunOpts {
        capture,
        ..Default::default()
    };
    run_with(engine, module, args, &opts)
}

pub fn run_with(
    engine: &Engine,
    module: &Module,
    args: &[String],
    opts: &RunOpts,
) -> Result<Output> {
    let cwd = std::env::current_dir().context("working directory")?;
    let mut b = WasiCtxBuilder::new();
    if !opts.no_stdin {
        b.inherit_stdin();
    }
    let pipes = if opts.capture {
        let out = MemoryOutputPipe::new(usize::MAX);
        let err = MemoryOutputPipe::new(usize::MAX);
        b.stdout(out.clone()).stderr(err.clone());
        Some((out, err))
    } else {
        b.inherit_stdout().inherit_stderr();
        None
    };
    b.args(args).env("PWD", cwd.to_string_lossy());
    for (k, v) in &opts.env {
        b.env(k, v);
    }
    b.preopened_dir(Path::new("/"), "/", FsPerms::ReadWrite)?;
    let mut store = Store::new(engine, b.build_p1());
    if let Some(t) = opts.timeout {
        start_ticker(engine);
        let ticks = t.as_millis().div_ceil(TICK.as_millis()).max(1) as u64;
        store.set_epoch_deadline(ticks);
        store.epoch_deadline_trap();
    }
    let mut linker = Linker::new(engine);
    wasmtime_wasi::p1::add_to_linker_sync(&mut linker, |c| c)?;
    let res = linker.instantiate(&mut store, module).and_then(|instance| {
        instance
            .get_typed_func::<(), ()>(&mut store, "_start")?
            .call(&mut store, ())
    });
    let mut trap_msg = None;
    let code = match res {
        Ok(()) => 0,
        Err(e) => match e.downcast_ref::<I32Exit>() {
            Some(x) => x.0,
            None => {
                trap_msg = Some(format!("Error: {e:?}\n"));
                EXIT_TRAP
            }
        },
    };
    drop(store);
    let (stdout, mut stderr) = match pipes {
        Some((o, e)) => (o.contents().to_vec(), e.contents().to_vec()),
        None => (Vec::new(), Vec::new()),
    };
    if let Some(m) = trap_msg {
        if opts.capture {
            stderr.extend_from_slice(m.as_bytes());
        } else {
            eprint!("{m}");
        }
    }
    Ok(Output {
        code,
        stdout,
        stderr,
    })
}
