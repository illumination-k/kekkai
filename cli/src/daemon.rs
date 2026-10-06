// The compiler daemon (kek daemon), as in ./kek:
//
// `kek daemon start` keeps the compiler running (`serve`,
// compiler/serve.kek) with the parses of source files and of the core
// library in memory, so a build parses only what changed and no compiler
// starts. Requests are lines appended to <dir>/req.log, which this binary
// (`kek __daemon <dir>`, a process of its own) feeds to the compiler's
// standard input; its output goes to <dir>/out.log and err.log, and
// <dir>/resp-<seq> holds the exit status. One request runs at a time: a
// launch that finds the daemon busy (<dir>/lock) runs the compiler itself.
use std::fs::{self, OpenOptions};
use std::io::{Read, Write};
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;
use wasmtime::Result;

use crate::wasm::Output;
use crate::{Kek, cache, die};

fn daemon_dir(kek: &Kek) -> Result<PathBuf> {
    Ok(cache::dir()?.join("daemon").join(kek.stage_key()))
}

fn pid_of(dir: &Path) -> Option<i32> {
    fs::read_to_string(dir.join("pid"))
        .ok()?
        .trim()
        .parse()
        .ok()
}

fn alive(dir: &Path) -> bool {
    // SAFETY: kill with signal 0 only checks that the process exists.
    pid_of(dir).is_some_and(|p| unsafe { libc::kill(p, 0) } == 0)
}

/// The fields of a request: tab-separated, with \t \n \\ escaped.
fn escape(s: &str) -> String {
    s.replace('\\', "\\\\")
        .replace('\t', "\\t")
        .replace('\n', "\\n")
}

/// call runs a compiler command in the daemon. None (and the caller runs
/// the compiler itself) when there is no daemon, it is busy or it died.
pub fn call(kek: &Kek, args: &[String], capture: bool) -> Result<Option<Output>> {
    static SEQ: AtomicUsize = AtomicUsize::new(0);
    if std::env::var("KEK_DAEMON").is_ok_and(|v| v == "0") {
        return Ok(None);
    }
    let dd = daemon_dir(kek)?;
    if !alive(&dd) {
        return Ok(None);
    }
    let cwd = std::env::current_dir()?;
    let mut line = String::new();
    for a in std::iter::once(cwd.to_string_lossy().as_ref()).chain(args.iter().map(|s| s.as_str()))
    {
        line.push('\t');
        line.push_str(&escape(a));
    }
    let lock = dd.join("lock");
    if fs::create_dir(&lock).is_err() {
        return Ok(None);
    }
    let seq = format!(
        "{}-{}",
        std::process::id(),
        SEQ.fetch_add(1, Ordering::SeqCst) + 1
    );
    let res = (|| -> Result<Option<Output>> {
        fs::write(dd.join("out.log"), "")?;
        fs::write(dd.join("err.log"), "")?;
        let mut req = OpenOptions::new().append(true).open(dd.join("req.log"))?;
        req.write_all(format!("{seq}{line}\n").as_bytes())?;
        let resp = dd.join(format!("resp-{seq}"));
        let mut n = 0u64;
        while fs::metadata(&resp).map_or(true, |m| m.len() == 0) {
            n += 1;
            if n.is_multiple_of(50) && !alive(&dd) {
                return Ok(None);
            }
            std::thread::sleep(Duration::from_millis(5));
        }
        let stdout = fs::read(dd.join("out.log"))?;
        let stderr = fs::read(dd.join("err.log"))?;
        let code = fs::read_to_string(&resp)?.trim().parse().unwrap_or(1);
        let _ = fs::remove_file(&resp);
        if !capture {
            std::io::stdout().write_all(&stdout)?;
            std::io::stdout().flush()?;
            std::io::stderr().write_all(&stderr)?;
        }
        Ok(Some(Output {
            code,
            stdout,
            stderr,
        }))
    })();
    let _ = fs::remove_dir(&lock);
    res
}

pub fn cmd_daemon(kek: &mut Kek, args: &[String]) -> Result<i32> {
    let dd = daemon_dir(kek)?;
    match args.first().map(|s| s.as_str()).unwrap_or("status") {
        "start" => {
            if alive(&dd) {
                println!("kek daemon: running (pid {})", pid_of(&dd).unwrap());
                return Ok(0);
            }
            fs::create_dir_all(&dd)?;
            let _ = fs::remove_dir(dd.join("lock"));
            for e in fs::read_dir(&dd)?.flatten() {
                if e.file_name().to_string_lossy().starts_with("resp-") {
                    let _ = fs::remove_file(e.path());
                }
            }
            for f in ["req.log", "out.log", "err.log"] {
                fs::write(dd.join(f), "")?;
            }
            let log = |f: &str| OpenOptions::new().append(true).open(dd.join(f));
            let mut cmd = Command::new(std::env::current_exe()?);
            cmd.arg("__daemon")
                .arg(&dd)
                .stdin(Stdio::null())
                .stdout(log("out.log")?)
                .stderr(log("err.log")?);
            // its own process group: the terminal's ^C does not reach it
            std::os::unix::process::CommandExt::process_group(&mut cmd, 0);
            let child = cmd.spawn()?;
            fs::write(dd.join("pid"), format!("{}\n", child.id()))?;
            println!("kek daemon: started (pid {})", child.id());
            Ok(0)
        }
        "stop" => {
            if !alive(&dd) {
                println!("kek daemon: not running");
                return Ok(0);
            }
            let _ = call(kek, &["quit".to_string()], true);
            if let Some(p) = pid_of(&dd) {
                // SAFETY: a signal to the daemon's process.
                unsafe { libc::kill(p, libc::SIGTERM) };
            }
            let _ = fs::remove_file(dd.join("pid"));
            println!("kek daemon: stopped");
            Ok(0)
        }
        "status" => {
            if alive(&dd) {
                println!(
                    "kek daemon: running (pid {}, compiler {})",
                    pid_of(&dd).unwrap(),
                    kek.stage_key()
                );
                Ok(0)
            } else {
                println!("kek daemon: not running");
                Ok(1)
            }
        }
        "stats" => match call(kek, &["serve-stats".to_string()], false)? {
            Some(o) => Ok(o.code),
            None => die!("kek daemon: not running or busy"),
        },
        _ => die!("usage: kek daemon start|stop|status|stats"),
    }
}

/// serve (`kek __daemon <dir>`): the daemon process. The compiler's
/// standard input is a pipe fed with what is appended to <dir>/req.log
/// (`tail -f`).
pub fn serve(kek: &mut Kek, args: &[String]) -> Result<i32> {
    let Some(dir) = args.first() else {
        die!("usage: kek __daemon <dir>");
    };
    let req = Path::new(dir).join("req.log");
    let mut fds = [0; 2];
    // SAFETY: pipe fills fds; dup2 makes its read end the standard input.
    unsafe {
        if libc::pipe(fds.as_mut_ptr()) != 0 || libc::dup2(fds[0], 0) < 0 {
            die!("kek daemon: cannot create the request pipe");
        }
        libc::close(fds[0]);
    }
    let wfd = fds[1];
    std::thread::spawn(move || {
        // SAFETY: wfd is the pipe's write end, owned by this thread.
        let mut w = unsafe { <fs::File as std::os::fd::FromRawFd>::from_raw_fd(wfd) };
        // from the start: `kek daemon start` emptied it before the first
        // client could write
        let Ok(mut f) = fs::File::open(&req) else {
            return;
        };
        let mut buf = vec![0; 1 << 16];
        loop {
            match f.read(&mut buf) {
                Ok(0) => std::thread::sleep(Duration::from_millis(2)),
                Ok(n) => {
                    if w.write_all(&buf[..n]).is_err() {
                        return;
                    }
                }
                Err(_) => return,
            }
        }
    });
    let r = kek.run_compiler_here(&["serve".to_string(), dir.clone()], false)?;
    Ok(r.code)
}
