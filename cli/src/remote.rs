// The remote cache: KEK_REMOTE_CACHE=<url> shares the action cache between
// machines (CI runners, developers) over the HTTP protocol of Bazel's
// remote cache: GET and PUT <url>/ac/<sha256>. bazel-remote serves it
// (with --disable_http_ac_validation, since the entries are kek's own), and
// so does any server accepting PUT; a file:// URL names a shared
// directory. Transfers use curl, as ./kek does. A remote failure is a
// cache miss, never an error.
use std::fs;
use std::path::Path;
use std::process::{Command, Stdio};
use wasmtime::Result;

use crate::cache;

fn url() -> Option<String> {
    std::env::var("KEK_REMOTE_CACHE")
        .ok()
        .filter(|u| !u.is_empty())
        .map(|u| u.trim_end_matches('/').to_string())
}

/// The name of a cache entry: its path under the cache directory.
fn entry_url(base: &str, path: &Path) -> Result<String> {
    let dir = cache::dir()?;
    let name = path.strip_prefix(&dir).unwrap_or(path).to_string_lossy();
    Ok(format!("{base}/ac/{}", cache::sha_hex(name.as_bytes(), 64)))
}

fn curl(args: &[&str]) -> bool {
    Command::new("curl")
        .arg("-fsS")
        .args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .is_ok_and(|s| s.success())
}

/// get fetches the entry `path` into `dest` (true when found).
fn get(base: &str, path: &Path, dest: &Path) -> Result<bool> {
    let u = entry_url(base, path)?;
    Ok(curl(&["-o", &dest.to_string_lossy(), &u]))
}

/// put uploads `file` as the entry `path` (errors are ignored).
fn put(base: &str, path: &Path, file: &Path) -> Result<()> {
    if let Some(d) = base.strip_prefix("file://") {
        let _ = fs::create_dir_all(Path::new(d).join("ac"));
    }
    let u = entry_url(base, path)?;
    curl(&["-T", &file.to_string_lossy(), &u]);
    Ok(())
}

/// action_fetch: when the action directory `dir` is not in the local
/// cache, fetch it from the remote cache.
pub fn action_fetch(dir: &Path) -> Result<()> {
    let Some(base) = url() else { return Ok(()) };
    if dir.exists() {
        return Ok(());
    }
    let t = tempfile::Builder::new().prefix("kek-fetch-").tempdir()?;
    let tar = t.path().join("a.tar");
    let d = t.path().join("d");
    if !get(&base, dir, &tar)? || fs::create_dir(&d).is_err() {
        return Ok(());
    }
    let ok = fs::File::open(&tar)
        .ok()
        .is_some_and(|f| tar::Archive::new(f).unpack(&d).is_ok());
    if ok && d.join("ok").is_file() {
        if let Some(p) = dir.parent() {
            fs::create_dir_all(p)?;
        }
        if !dir.exists() {
            let _ = fs::rename(&d, dir);
        }
    }
    Ok(())
}

/// action_put stores the files in `tmp` as the outputs of the action
/// cached in `dest` (renamed into place), and uploads them.
pub fn action_put(tmp: tempfile::TempDir, dest: &Path) -> Result<()> {
    fs::write(tmp.path().join("ok"), "")?;
    if let Some(p) = dest.parent() {
        fs::create_dir_all(p)?;
    }
    if !cache::put_dir(tmp, dest)? {
        return Ok(());
    }
    let Some(base) = url() else { return Ok(()) };
    let t = tempfile::Builder::new().prefix("kek-put-").tempfile()?;
    let mut b = tar::Builder::new(t.reopen()?);
    b.append_dir_all(".", dest)?;
    b.into_inner()?;
    put(&base, dest, t.path())
}

/// file_fetch / file_push: the same for a single-file entry.
pub fn file_fetch(file: &Path) -> Result<()> {
    let Some(base) = url() else { return Ok(()) };
    if file.exists() {
        return Ok(());
    }
    if let Some(p) = file.parent() {
        fs::create_dir_all(p)?;
    }
    let t = tempfile::NamedTempFile::new_in(file.parent().unwrap())?;
    if get(&base, file, t.path())? {
        t.persist(file)?;
    }
    Ok(())
}

pub fn file_push(file: &Path) -> Result<()> {
    let Some(base) = url() else { return Ok(()) };
    put(&base, file, file)
}
