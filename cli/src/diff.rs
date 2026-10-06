// -diff <rev>: the program as of a git revision, extracted into a
// temporary directory and passed to the compiler as -base (kek similar,
// kek complexity, kek affected). Needs git.
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use wasmtime::Result;
use wasmtime::error::Context;

use crate::{Kek, die};

fn git(dir: &Path, args: &[&str]) -> Result<std::process::Output> {
    Command::new("git")
        .current_dir(dir)
        .args(args)
        .stdin(Stdio::null())
        .output()
        .context("running git")
}

/// unpack extracts the tar output of `git archive` into `dest`.
fn unpack(tar: &[u8], dest: &Path) -> Result<()> {
    tar::Archive::new(tar).unpack(dest)?;
    Ok(())
}

/// with_diff runs the compiler command `cmd` (similar, complexity); with
/// -diff <rev>, only findings with a new or changed definition since rev
/// are reported (the CI mode for pull requests).
pub fn with_diff(kek: &mut Kek, cmd: &str, usage: &str, args: &[String]) -> Result<i32> {
    let mut rev = None;
    let mut path = None;
    let mut rest = Vec::new();
    let mut i = 0;
    while i < args.len() {
        let a = &args[i];
        if a == "-diff" || a == "--diff" {
            let Some(r) = args.get(i + 1) else {
                eprintln!("flag needs an argument: -diff\nusage: {usage}");
                return Ok(2);
            };
            rev = Some(r.clone());
            i += 2;
            continue;
        }
        if let Some(r) = a.strip_prefix("-diff=").or(a.strip_prefix("--diff=")) {
            rev = Some(r.to_string());
        } else {
            path = Some(a.clone());
            rest.push(a.clone());
        }
        i += 1;
    }
    let mut full = vec![cmd.to_string()];
    let Some(rev) = rev else {
        full.extend_from_slice(args);
        return kek.compiler_cmd(&full);
    };
    if rev.is_empty() {
        eprintln!("flag needs an argument: -diff\nusage: {usage}");
        return Ok(2);
    }
    let path = path.unwrap_or_default();
    let p = Path::new(&path);
    if path.is_empty() || !p.exists() {
        eprintln!(
            "kek {cmd} -diff: no such file or directory: {}",
            if path.is_empty() { "<file|dir>" } else { &path }
        );
        return Ok(2);
    }
    let cwd = std::env::current_dir()?;
    let top = git(&cwd, &["rev-parse", "--show-toplevel"])?;
    if !top.status.success() {
        eprintln!("kek {cmd} -diff: not in a git repository");
        return Ok(2);
    }
    let top = PathBuf::from(String::from_utf8_lossy(&top.stdout).trim()).canonicalize()?;
    let ok = git(
        &cwd,
        &[
            "rev-parse",
            "--verify",
            "--quiet",
            &format!("{rev}^{{commit}}"),
        ],
    )?;
    if !ok.status.success() {
        eprintln!("kek {cmd} -diff: unknown revision {rev}");
        return Ok(2);
    }
    let abs = p.canonicalize()?;
    let Ok(rel) = abs.strip_prefix(&top) else {
        eprintln!(
            "kek {cmd} -diff: {path} is outside the repository {}",
            top.display()
        );
        return Ok(2);
    };
    let rel = if rel.as_os_str().is_empty() {
        PathBuf::from(".")
    } else {
        rel.to_path_buf()
    };
    let base = tempfile::Builder::new()
        .prefix(&format!("kek-{cmd}-"))
        .tempdir()?;
    let ar = git(
        &top,
        &[
            "archive",
            "--format=tar",
            &rev,
            "--",
            &rel.to_string_lossy(),
        ],
    )?;
    let based = base.path().join(&rel);
    if ar.status.success() && unpack(&ar.stdout, base.path()).is_ok() && based.exists() {
        full.push("-base".to_string());
        full.push(based.to_string_lossy().into_owned());
        full.push("-base_name".to_string());
        full.push(format!("git:{rev}"));
    }
    // otherwise the path did not exist at rev: every definition is new
    full.extend(rest);
    kek.compiler_cmd(&full)
}

const AFFECTED_USAGE: &str =
    "usage: kek affected [-json] (-base <file|dir> | -diff <rev>) <file|dir>";

pub fn cmd_affected(kek: &mut Kek, args: &[String]) -> Result<i32> {
    let mut json = false;
    let mut base = None;
    let mut rev = None;
    let mut i = 0;
    while i < args.len() {
        let a = args[i].as_str();
        match a {
            "-json" | "--json" => json = true,
            "-base" | "--base" | "-diff" | "--diff" => {
                let v = args.get(i + 1).cloned().unwrap_or_default();
                if a.ends_with("base") {
                    base = Some(v)
                } else {
                    rev = Some(v)
                }
                i += 1;
            }
            "-h" | "-help" | "--help" => {
                eprintln!("{AFFECTED_USAGE}");
                return Ok(0);
            }
            _ if a.starts_with("-base=") || a.starts_with("--base=") => {
                base = a.split_once('=').map(|x| x.1.to_string())
            }
            _ if a.starts_with("-diff=") || a.starts_with("--diff=") => {
                rev = a.split_once('=').map(|x| x.1.to_string())
            }
            _ if a.starts_with('-') => die!("flag provided but not defined: {a}"),
            _ => break,
        }
        i += 1;
    }
    let rest = &args[i..];
    if rest.len() != 1 {
        die!("{AFFECTED_USAGE}");
    }
    let target = &rest[0];
    if !Path::new(target).exists() {
        die!("stat {target}: no such file or directory");
    }
    let tmp = tempfile::Builder::new().prefix("kek-affected-").tempdir()?;
    if let Some(rev) = rev.filter(|r| !r.is_empty()) {
        if base.as_ref().is_some_and(|b| !b.is_empty()) {
            die!("kek affected: -base and -diff are exclusive");
        }
        base = Some(extract_rev(&rev, Path::new(target), tmp.path())?);
    }
    let Some(base) = base.filter(|b| !b.is_empty()) else {
        die!("kek affected: -base or -diff is required");
    };
    let mut full = vec!["affected".to_string()];
    if json {
        full.push("-json".to_string());
    }
    full.extend(["-base".to_string(), base, target.clone()]);
    kek.compiler_cmd(&full)
}

/// extract_rev writes `path` (a file or a directory) as of `rev` under
/// `dest` and returns the path of the copy.
pub fn extract_rev(rev: &str, path: &Path, dest: &Path) -> Result<String> {
    let fail = || format!("kek mutate: -diff: cannot read {} at {rev}", path.display());
    if path.is_dir() {
        let ar = git(path, &["archive", "--format=tar", &format!("{rev}:./")])?;
        if !ar.status.success() {
            die!("{}", fail());
        }
        if unpack(&ar.stdout, dest).is_err() {
            die!("{}", fail());
        }
        Ok(dest.to_string_lossy().into_owned())
    } else {
        let name = path
            .file_name()
            .context("no file name")?
            .to_string_lossy()
            .into_owned();
        let dir = match path.parent() {
            Some(d) if !d.as_os_str().is_empty() => d,
            _ => Path::new("."),
        };
        let show = git(dir, &["show", &format!("{rev}:./{name}")])?;
        if !show.status.success() {
            die!("{}", fail());
        }
        let f = dest.join(&name);
        std::fs::write(&f, &show.stdout)?;
        Ok(f.to_string_lossy().into_owned())
    }
}
