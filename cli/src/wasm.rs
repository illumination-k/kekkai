// Running WasmGC + WASI (preview 1) modules: the compiler and the programs
// it builds, as `wasmtime run -W gc=y,function-references=y --dir /` does
// in ./kek.
//
// The compiler is precompiled once per (compiler, engine) into the cache
// (modules/<key>.cwasm); programs are compiled on each run.
use std::hash::{Hash, Hasher};
use std::path::Path;
use wasmtime::Result;
use wasmtime::error::Context;
use wasmtime::{Collector, Config, Engine, Linker, Module, Store};
use wasmtime_wasi::p2::pipe::MemoryOutputPipe;
use wasmtime_wasi::{FsPerms, I32Exit, WasiCtxBuilder};

use crate::cache;

/// The exit status of a module that trapped (wasmtime's: 128 + SIGABRT).
const EXIT_TRAP: i32 = 134;

#[derive(Clone, Copy, PartialEq)]
pub enum Kind {
    /// The compiler: a large initial GC heap (reserved, not touched).
    Compiler,
    /// A program built by the compiler.
    Program,
}

/// The garbage collector: the copying collector is several times faster
/// than wasmtime's default (deferred reference counting) on the compiler
/// and on programs alike.
pub fn engine(kind: Kind) -> Result<Engine> {
    let mut c = Config::new();
    c.wasm_gc(true)
        .wasm_function_references(true)
        .collector(Collector::Copying);
    if kind == Kind::Compiler {
        c.gc_heap_initial_size(1 << 30);
    }
    Engine::new(&c)
}

/// The compiler's module, precompiled into the cache on first use.
pub fn compiler_module(engine: &Engine, wasm: &[u8]) -> Result<Module> {
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
        .context("compiling the compiler")?;
    cache::write_atomic(&path, &bytes)?;
    // SAFETY: produced by precompile_module of this engine just above.
    unsafe { Module::deserialize(engine, &bytes) }
}

pub struct Output {
    pub code: i32,
    pub stdout: Vec<u8>,
    pub stderr: Vec<u8>,
}

/// run <module> with argv <args> (args[0] is the program name), the whole
/// file system preopened at / and PWD set to the working directory.
/// With `capture`, stdout and stderr are returned instead of inherited.
pub fn run(engine: &Engine, module: &Module, args: &[String], capture: bool) -> Result<Output> {
    let cwd = std::env::current_dir().context("working directory")?;
    let mut b = WasiCtxBuilder::new();
    b.inherit_stdin();
    let pipes = if capture {
        let out = MemoryOutputPipe::new(usize::MAX);
        let err = MemoryOutputPipe::new(usize::MAX);
        b.stdout(out.clone()).stderr(err.clone());
        Some((out, err))
    } else {
        b.inherit_stdout().inherit_stderr();
        None
    };
    b.args(args)
        .env("PWD", cwd.to_string_lossy())
        .preopened_dir(Path::new("/"), "/", FsPerms::ReadWrite)?;
    let mut store = Store::new(engine, b.build_p1());
    let mut linker = Linker::new(engine);
    wasmtime_wasi::p1::add_to_linker_sync(&mut linker, |c| c)?;
    let instance = linker.instantiate(&mut store, module)?;
    let start = instance.get_typed_func::<(), ()>(&mut store, "_start")?;
    let code = match start.call(&mut store, ()) {
        Ok(()) => 0,
        Err(e) => match e.downcast_ref::<I32Exit>() {
            Some(x) => x.0,
            None => {
                eprintln!("Error: {e:?}");
                EXIT_TRAP
            }
        },
    };
    drop(store);
    let (stdout, stderr) = match pipes {
        Some((o, e)) => (o.contents().to_vec(), e.contents().to_vec()),
        None => (Vec::new(), Vec::new()),
    };
    Ok(Output {
        code,
        stdout,
        stderr,
    })
}
