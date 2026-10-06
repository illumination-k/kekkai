// The cache directory: $KEK_CACHE, else $XDG_CACHE_HOME/kek, else
// ~/.cache/kek. Entries are written aside and renamed into place, so
// concurrent runs need no lock.
use sha2::{Digest, Sha256};
use std::path::{Path, PathBuf};
use std::{env, fs};
use wasmtime::Result;
use wasmtime::error::Context;

pub fn dir() -> Result<PathBuf> {
    if let Some(d) = env::var_os("KEK_CACHE").filter(|d| !d.is_empty()) {
        return Ok(PathBuf::from(d));
    }
    if let Some(d) = env::var_os("XDG_CACHE_HOME").filter(|d| !d.is_empty()) {
        return Ok(PathBuf::from(d).join("kek"));
    }
    let home = env::var_os("HOME").context("kek: neither KEK_CACHE nor HOME is set")?;
    Ok(PathBuf::from(home).join(".cache/kek"))
}

/// The first `n` hex digits of the SHA-256 of `data`.
pub fn sha_hex(data: &[u8], n: usize) -> String {
    hex(&Sha256::digest(data))[..n].to_string()
}

pub fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}

/// write_atomic writes `path` through a temporary file in its directory.
pub fn write_atomic(path: &Path, data: &[u8]) -> Result<()> {
    let parent = path.parent().unwrap();
    fs::create_dir_all(parent).with_context(|| format!("mkdir {}", parent.display()))?;
    let tmp = tempfile::NamedTempFile::new_in(parent)?;
    fs::write(tmp.path(), data)?;
    tmp.persist(path)?;
    Ok(())
}

/// put_dir moves the directory `tmp` to `dest` unless `dest` already
/// exists (another run won the race: `tmp` is removed). Returns whether
/// this run put it.
pub fn put_dir(tmp: tempfile::TempDir, dest: &Path) -> Result<bool> {
    if dest.exists() {
        return Ok(false);
    }
    let tmp = tmp.keep();
    if fs::rename(&tmp, dest).is_err() {
        let _ = fs::remove_dir_all(&tmp);
        return Ok(false);
    }
    Ok(true)
}
