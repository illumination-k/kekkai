// Embeds the compiler (a WasmGC + WASI module built from compiler/ by
// ./kek), the Workers runtime (js/kekkai_runtime.js) and the core library
// sources (lib/core, for the language server's go-to-definition).
//
// The compiler comes from $KEK_COMPILER_WASM or cli/embed/kek.wasm, which
// scripts/dist.sh writes (`./kek stage`).
use std::{env, fs, path::PathBuf};

fn main() {
    let manifest = PathBuf::from(env::var("CARGO_MANIFEST_DIR").unwrap());
    let repo = manifest.parent().unwrap().to_path_buf();
    let out = PathBuf::from(env::var("OUT_DIR").unwrap());

    println!("cargo:rerun-if-env-changed=KEK_COMPILER_WASM");
    let wasm = env::var_os("KEK_COMPILER_WASM")
        .map(PathBuf::from)
        .unwrap_or_else(|| manifest.join("embed/kek.wasm"));
    if !wasm.is_file() {
        panic!(
            "{} not found: run scripts/dist.sh (or set KEK_COMPILER_WASM to the compiler's module.wasm, `./kek stage`)",
            wasm.display()
        );
    }
    println!("cargo:rerun-if-changed={}", wasm.display());

    let runtime_js = repo.join("js/kekkai_runtime.js");
    println!("cargo:rerun-if-changed={}", runtime_js.display());

    let core = repo.join("lib/core");
    println!("cargo:rerun-if-changed={}", core.display());
    let mut files: Vec<PathBuf> = fs::read_dir(&core)
        .unwrap()
        .map(|e| e.unwrap().path())
        .filter(|p| p.extension().is_some_and(|x| x == "kek"))
        .collect();
    files.sort();

    let mut src = String::new();
    src.push_str(&format!(
        "pub const COMPILER_WASM: &[u8] = include_bytes!({:?});\n",
        wasm.canonicalize().unwrap()
    ));
    src.push_str(&format!(
        "pub const RUNTIME_JS: &[u8] = include_bytes!({:?});\n",
        runtime_js
    ));
    src.push_str("pub const CORE_LIB: &[(&str, &[u8])] = &[\n");
    for f in &files {
        println!("cargo:rerun-if-changed={}", f.display());
        let name = f.file_name().unwrap().to_str().unwrap();
        src.push_str(&format!("    ({name:?}, include_bytes!({f:?})),\n"));
    }
    src.push_str("];\n");
    fs::write(out.join("embedded.rs"), src).unwrap();

    let rev = std::process::Command::new("git")
        .args(["rev-parse", "--short=12", "HEAD"])
        .current_dir(&repo)
        .output()
        .ok()
        .filter(|o| o.status.success())
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .unwrap_or_default();
    println!("cargo:rustc-env=KEK_GIT_REV={rev}");
    // the release workflow sets KEK_VERSION from the tag
    println!("cargo:rerun-if-env-changed=KEK_VERSION");
    let version = env::var("KEK_VERSION").unwrap_or_else(|_| env::var("CARGO_PKG_VERSION").unwrap());
    println!("cargo:rustc-env=KEK_VERSION={version}");
    println!(
        "cargo:rerun-if-changed={}",
        repo.join(".git/HEAD").display()
    );
}
