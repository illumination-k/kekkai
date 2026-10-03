// Helpers shared by the test suites (tests/run.mjs).
import { spawn } from "node:child_process";
import { mkdtemp, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

export const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
// ./kek is a shell wrapper around js/kek.mjs; run the launcher directly.
export const kek = path.join(root, "js/kek.mjs");
export const node = process.execPath;

/** A failed expectation; the message is the whole report. */
export class TestFailure extends Error {}
export const fail = (msg) => { throw new TestFailure(msg); };
/** A case that cannot run here (missing tool or feature); reported as skipped. */
export class TestSkip extends Error {}
export const skip = (msg) => { throw new TestSkip(msg); };

export async function exists(p) {
  try { await stat(p); return true; } catch { return false; }
}

/**
 * Run a command; resolves to { code, stdout, stderr, out } (out = both
 * streams interleaved). Never rejects for a non-zero exit.
 */
export function exec(cmd, args, { cwd = root, env, input, timeout = 0, signal } = {}) {
  return new Promise((resolve, reject) => {
    const p = spawn(cmd, args, { cwd, env: env ?? process.env, signal, stdio: ["pipe", "pipe", "pipe"] });
    let stdout = "", stderr = "", out = "", timedOut = false;
    p.stdout.setEncoding("utf8").on("data", (d) => { stdout += d; out += d; });
    p.stderr.setEncoding("utf8").on("data", (d) => { stderr += d; out += d; });
    const t = timeout ? setTimeout(() => { timedOut = true; p.kill("SIGKILL"); }, timeout) : null;
    p.on("error", (e) => { if (t) clearTimeout(t); reject(e); });
    p.on("close", (code, sig) => {
      if (t) clearTimeout(t);
      resolve({ code: code ?? (timedOut ? "timeout" : sig), stdout, stderr, out, timedOut });
    });
    if (input !== undefined) p.stdin.end(input); else p.stdin.end();
  });
}

/** Run `./kek <args>`. */
export const runKek = (args, opts) => exec(node, [kek, ...args], opts);

/** Is `cmd --version` runnable? */
export async function have(cmd, args = ["--version"]) {
  try { return (await exec(cmd, args, { timeout: 60000 })).code === 0; } catch { return false; }
}

const tmpRoot = path.join(tmpdir(), "kekkai-tests-");
/** A fresh temporary directory, removed when `use` settles (kept on failure if keep is set). */
export async function withTmp(use, { keepOnFailure = false } = {}) {
  const dir = await mkdtemp(tmpRoot);
  let ok = false;
  try {
    const r = await use(dir);
    ok = true;
    return r;
  } finally {
    if (ok || !keepOnFailure) await rm(dir, { recursive: true, force: true });
  }
}

/** `./kek build -o dir file`; fails the test on a compile error. */
export async function build(file, dir) {
  const r = await runKek(["build", "-o", dir, file]);
  if (r.code !== 0) fail(`${path.relative(root, file)}: build failed (exit ${r.code})\n${r.out}`);
  return r;
}

export const rel = (p) => path.relative(root, p);
