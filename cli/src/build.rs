// kek build / kek run, through the build cache of ./kek (cached_build):
//
// 1. the action cache: build/src-<digest> holds the outputs and the
//    diagnostics of a build, keyed by the compiler, the path, the
//    arguments and the contents of the program's files. Equal digests give
//    equal outputs, so the compiler need not run at all.
// 2. otherwise the compiler checks the program and reuses module.wasm from
//    build/<compiler>/<key> when its definition hashes are unchanged
//    (`-cache`; comment-only edits).
//
// KEK_BUILD_CACHE=0 disables both.
use sha2::{Digest, Sha256};
use std::fs;
use std::io::Write;
use std::path::{Path, PathBuf};
use wasmtime::Result;
use wasmtime::error::Context;

use crate::{Kek, cache, die, embedded, remote, wasm};

const BUILD_USAGE: &str = "usage: kek build [-o dir] [-target d1|do] <file|dir>";

pub fn cmd_build(kek: &mut Kek, args: &[String]) -> Result<i32> {
    let mut out = "out".to_string();
    let mut target = "d1".to_string();
    let mut paths = Vec::new();
    let mut i = 0;
    while i < args.len() {
        let a = args[i].as_str();
        match a {
            "-o" | "--o" | "-target" | "--target" => {
                let Some(v) = args.get(i + 1) else {
                    eprintln!("{BUILD_USAGE}");
                    return Ok(2);
                };
                if a.ends_with('o') {
                    out = v.clone();
                } else {
                    target = v.clone();
                }
                i += 1;
            }
            _ if a.starts_with("-o=") || a.starts_with("--o=") => out = after_eq(a),
            _ if a.starts_with("-target=") || a.starts_with("--target=") => target = after_eq(a),
            _ => paths.push(a.to_string()),
        }
        i += 1;
    }
    if paths.len() != 1 {
        eprintln!("{BUILD_USAGE}");
        return Ok(2);
    }
    let out = PathBuf::from(out);
    fs::create_dir_all(&out)?;
    let code = cached_build(kek, &paths[0], &out, &["-target".to_string(), target])?;
    if code != 0 {
        return Ok(code);
    }
    if out.join("kekkai_meta.js").is_file() {
        fs::write(out.join("kekkai_runtime.js"), embedded::RUNTIME_JS)?;
    }
    let size = fs::metadata(out.join("module.wasm"))?.len();
    println!("{} -> {} ({size} bytes of wasm)", paths[0], out.display());
    Ok(0)
}

pub fn cmd_run(kek: &mut Kek, args: &[String]) -> Result<i32> {
    let Some((src, rest)) = args.split_first() else {
        eprintln!("usage: kek run <file|dir> [args...]");
        return Ok(2);
    };
    let out = tempfile::Builder::new().prefix("kek-run-").tempdir()?;
    let code = cached_build(kek, src, out.path(), &[])?;
    if code != 0 {
        return Ok(code);
    }
    if out.path().join("kekkai_meta.js").is_file() {
        die!(
            "kek run: {src} is a #[handler] program (build it with kek build and serve it with workerd or wrangler)"
        );
    }
    let module_path = out.path().join("module.wasm");
    let engine = wasm::shared_engine(wasm::Kind::Program)?;
    let module = wasmtime::Module::from_file(&engine, &module_path)?;
    let mut argv = vec![module_path.to_string_lossy().into_owned()];
    argv.extend_from_slice(rest);
    Ok(wasm::run(&engine, &module, &argv, false)?.code)
}

fn after_eq(a: &str) -> String {
    a.split_once('=').map(|x| x.1).unwrap_or("").to_string()
}

/// cached_build runs `compiler build <path> <out> [args...]` through the
/// build cache and returns its exit status.
pub fn cached_build(kek: &mut Kek, path: &str, out: &Path, args: &[String]) -> Result<i32> {
    let mut build = vec![
        "build".to_string(),
        path.to_string(),
        out.to_string_lossy().into_owned(),
    ];
    build.extend_from_slice(args);
    if std::env::var("KEK_BUILD_CACHE").is_ok_and(|v| v == "0") {
        return kek.compiler_cmd(&build);
    }
    let root = cache::dir()?.join("build");

    // 1. unchanged inputs: reuse the outputs without the compiler (its
    // diagnostics are replayed). wrangler.toml is only written when the
    // output directory has none, as the compiler does.
    let bd = root.join(format!("src-{}", src_digest(kek, path, args)?));
    remote::action_fetch(&bd)?;
    let has_toml = out.join("wrangler.toml").is_file();
    if bd.join("ok").is_file()
        && (!bd.join("worker.js").is_file() || has_toml || bd.join("wrangler.toml").is_file())
    {
        for f in ["module.wasm", "kekkai_meta.js", "worker.js"] {
            copy_if(&bd.join(f), &out.join(f))?;
        }
        if !has_toml {
            copy_if(&bd.join("wrangler.toml"), &out.join("wrangler.toml"))?;
        }
        if let Ok(e) = fs::read(bd.join("stderr")) {
            std::io::stderr().write_all(&e)?;
        }
        return Ok(0);
    }

    // 2. the compiler reuses module.wasm when the definition hashes are
    // unchanged
    let bc = root.join(kek.stage_key());
    build.push("-cache".to_string());
    build.push(bc.to_string_lossy().into_owned());
    let res = kek.run_compiler(&build, true)?;
    std::io::stderr().write_all(&res.stderr)?;
    let stdout = String::from_utf8_lossy(&res.stdout);
    let mut cache_line = None;
    {
        let mut o = std::io::stdout().lock();
        for line in stdout.lines() {
            if let Some(l) = line.strip_prefix("kek-build-cache: ") {
                cache_line = Some(l.to_string());
            } else if !line.is_empty() {
                writeln!(o, "{line}")?;
            }
        }
    }
    if res.code != 0 {
        return Ok(res.code);
    }
    // kek-build-cache: hit|miss <key> <entry>
    let fields: Vec<&str> = cache_line
        .as_deref()
        .unwrap_or("")
        .split_whitespace()
        .collect();
    let (status, key, entry) = (
        fields.first().copied().unwrap_or(""),
        fields.get(1).copied().unwrap_or(""),
        fields.get(2).copied().unwrap_or(""),
    );
    if !key.is_empty() {
        match status {
            "hit" => {
                fs::copy(bc.join(key).join("module.wasm"), out.join("module.wasm"))
                    .context("the build cache")?;
            }
            "miss" => {
                fs::create_dir_all(&bc)?;
                let t = tempfile::tempdir_in(&bc)?;
                fs::copy(out.join("module.wasm"), t.path().join("module.wasm"))?;
                fs::write(t.path().join("ok"), "")?;
                cache::put_dir(t, &bc.join(key))?;
            }
            _ => {}
        }
    }

    // record the action: its outputs and diagnostics
    fs::create_dir_all(&root)?;
    let t = tempfile::tempdir_in(&root)?;
    fs::copy(out.join("module.wasm"), t.path().join("module.wasm"))?;
    if entry == "handler" {
        for f in ["kekkai_meta.js", "worker.js"] {
            fs::copy(out.join(f), t.path().join(f))?;
        }
        if !has_toml {
            copy_if(&out.join("wrangler.toml"), &t.path().join("wrangler.toml"))?;
        }
    }
    fs::write(t.path().join("stderr"), &res.stderr)?;
    remote::action_put(t, &bd)?;
    Ok(0)
}

fn copy_if(from: &Path, to: &Path) -> Result<()> {
    if from.is_file() {
        fs::copy(from, to)?;
    }
    Ok(())
}

/// src_digest: the digest of a build's inputs (Bazel's action key).
pub fn src_digest(kek: &Kek, path: &str, args: &[String]) -> Result<String> {
    let mut h = Sha256::new();
    h.update(format!(
        "{}\n{}\n{}\n",
        kek.stage_key(),
        path,
        args.join(" ")
    ));
    let p = Path::new(path);
    if p.is_dir() {
        let mut files = Vec::new();
        prog_files(p, &mut files)?;
        for f in files {
            h.update(f.strip_prefix(p).unwrap_or(&f).to_string_lossy().as_bytes());
            h.update([0]);
            h.update(fs::read(&f)?);
            h.update([0]);
        }
    } else {
        h.update(fs::read(p).with_context(|| format!("stat {path}"))?);
    }
    Ok(cache::hex(&h.finalize())[..64].to_string())
}

/// prog_files: the .kek files of the program in `dir`, as the compiler
/// reads them: the directory's own, sorted, then those of each
/// subdirectory (a module) in turn. Directories starting with "." and
/// node_modules are skipped.
pub fn prog_files(dir: &Path, files: &mut Vec<PathBuf>) -> Result<()> {
    let mut entries: Vec<PathBuf> = fs::read_dir(dir)?
        .filter_map(|e| e.ok().map(|e| e.path()))
        .collect();
    entries.sort();
    for e in &entries {
        if e.is_file() && e.extension().is_some_and(|x| x == "kek") {
            files.push(e.clone());
        }
    }
    for e in &entries {
        let name = e.file_name().unwrap_or_default().to_string_lossy();
        if e.is_dir() && name != "node_modules" && !name.starts_with('.') {
            prog_files(e, files)?;
        }
    }
    Ok(())
}
