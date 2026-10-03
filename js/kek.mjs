#!/usr/bin/env node
// kek launcher: runs the self-hosted Kekkai compiler (compiler/) on Node.
//
// The compiler that runs is built from the current compiler/ sources by the
// committed bootstrap compiler (bootstrap/), and cached under
// .kek-cache/stage-<hash>/ keyed by the hash of the bootstrap and the
// sources. Cache entries are written to a temporary directory and renamed
// into place atomically, so concurrent launches never need a lock.
//
//   kek <command> [args...]         run a compiler command (check, ir, build, ...)
//   kek run <file|dir> [args...]    build a #[main] program and run it
//   kek test [flags] <file|dir>     run #[test] functions with mocks (js/kek_test.mjs)
//   KEK_STAGE=bootstrap kek ...     use the bootstrap compiler directly
//
// Maintenance:
//   kek bootstrap-check   rebuild the compiler with itself; require a fixed point
//   kek bootstrap-update  replace bootstrap/ with the current compiler (after the check)
import { createHash } from "node:crypto";
import { mkdtemp, mkdir, readFile, readdir, rename, rm, writeFile, copyFile, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { spawnSync } from "node:child_process";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "..");
const bootstrapDir = path.join(root, "bootstrap");
const compilerDir = path.join(root, "compiler");
const cacheDir = process.env.KEK_CACHE || path.join(root, ".kek-cache");
const rt = await import(pathToFileURL(path.join(here, "kekkai_runtime.js")).href);

async function exists(p) {
  try { await stat(p); return true; } catch { return false; }
}

/** Run a compiled #[main] program in-process; returns the exit code. */
async function runStage(dir, argv) {
  const meta = (await import(pathToFileURL(path.join(dir, "kekkai_meta.js")).href)).default;
  const module = new WebAssembly.Module(await readFile(path.join(dir, dir === bootstrapDir ? "kek.wasm" : "module.wasm")));
  const app = rt.createKekkai(module, meta);
  const caps = {};
  for (const p of meta.handlerParams || []) {
    switch (p.kind) {
      case "Fs": caps[p.name] = await rt.nodeFs(); break;
      case "Log": caps[p.name] = rt.consoleLog(); break;
      case "Net": caps[p.name] = rt.fetchNet(); break;
      case "Clock": caps[p.name] = rt.systemClock(); break;
      case "Random": caps[p.name] = rt.cryptoRandom(); break;
      case "Db": caps[p.name] = rt.dbCap(new rt.MemoryStore()); break;
    }
  }
  return await app.main(argv, caps);
}

async function sourcesHash() {
  const h = createHash("sha256");
  h.update(await readFile(path.join(bootstrapDir, "kek.wasm")));
  h.update(await readFile(path.join(bootstrapDir, "kekkai_meta.js")));
  for (const f of (await readdir(compilerDir)).filter((f) => f.endsWith(".kek")).sort()) {
    h.update(f + "\0");
    h.update(await readFile(path.join(compilerDir, f)));
    h.update("\0");
  }
  return h.digest("hex").slice(0, 24);
}

/** Build compiler/ with the compiler in `withDir` into a fresh directory. */
async function buildCompiler(withDir) {
  await mkdir(cacheDir, { recursive: true });
  const tmp = await mkdtemp(path.join(cacheDir, "tmp-"));
  const code = await runStage(withDir, ["build", compilerDir, tmp]);
  if (code !== 0) {
    await rm(tmp, { recursive: true, force: true });
    throw new Error(`building the compiler failed (exit ${code})`);
  }
  return tmp;
}

async function currentStage() {
  if (process.env.KEK_STAGE === "bootstrap") return bootstrapDir;
  const dir = path.join(cacheDir, "stage-" + (await sourcesHash()));
  if (await exists(path.join(dir, "module.wasm"))) return dir;
  const tmp = await buildCompiler(bootstrapDir);
  try {
    await rename(tmp, dir); // atomic publish; losing a race is fine
  } catch {
    await rm(tmp, { recursive: true, force: true });
  }
  return dir;
}

async function same(a, b) {
  return Buffer.compare(await readFile(a), await readFile(b)) === 0;
}

async function bootstrapCheck() {
  const s1 = await currentStage();
  const s2 = await buildCompiler(s1);
  try {
    for (const f of ["module.wasm", "kekkai_meta.js"]) {
      if (!(await same(path.join(s1, f), path.join(s2, f)))) {
        console.error(`bootstrap-check: no fixed point: ${f} differs between stage1 and stage2`);
        return { ok: false, s1 };
      }
    }
    console.log(`bootstrap-check: fixed point (${(await stat(path.join(s1, "module.wasm"))).size} bytes)`);
    return { ok: true, s1 };
  } finally {
    await rm(s2, { recursive: true, force: true });
  }
}

const [cmd, ...args] = process.argv.slice(2);
switch (cmd) {
  case "bootstrap-check": {
    process.exitCode = (await bootstrapCheck()).ok ? 0 : 1;
    break;
  }
  case "bootstrap-update": {
    const { ok, s1 } = await bootstrapCheck();
    if (!ok) { process.exitCode = 1; break; }
    await copyFile(path.join(s1, "module.wasm"), path.join(bootstrapDir, "kek.wasm"));
    await copyFile(path.join(s1, "kekkai_meta.js"), path.join(bootstrapDir, "kekkai_meta.js"));
    console.log("bootstrap/ updated");
    break;
  }
  case "build": {
    // kek build [-o dir] [-target d1|do] <file|dir>: the compiler writes
    // module.wasm and kekkai_meta.js, and worker.js and wrangler.toml for
    // #[handler] programs (an existing wrangler.toml is kept); the launcher
    // adds the runtime.
    let out = "out";
    let target = "d1";
    const rest = [];
    for (let i = 0; i < args.length; i++) {
      const a = args[i].replace(/^--/, "-");
      if (a === "-o") out = args[++i];
      else if (a.startsWith("-o=")) out = a.slice(3);
      else if (a === "-target") target = args[++i];
      else if (a.startsWith("-target=")) target = a.slice(8);
      else rest.push(args[i]);
    }
    if (rest.length !== 1 || out === undefined || target === undefined) {
      console.error("usage: kek build [-o dir] [-target d1|do] <file|dir>");
      process.exitCode = 2;
      break;
    }
    await mkdir(out, { recursive: true });
    const code = await runStage(await currentStage(), ["build", rest[0], out, "-target", target]);
    if (code === 0) {
      await copyFile(path.join(here, "kekkai_runtime.js"), path.join(out, "kekkai_runtime.js"));
      console.log(`${rest[0]} -> ${out} (${(await stat(path.join(out, "module.wasm"))).size} bytes of wasm)`);
    }
    process.exitCode = code;
    break;
  }
  case "run": {
    if (args.length < 1) { console.error("usage: kek run <file|dir> [args...]"); process.exitCode = 2; break; }
    const out = await mkdtemp(path.join(tmpdir(), "kek-run-"));
    try {
      const code = await runStage(await currentStage(), ["build", args[0], out]);
      if (code !== 0) { process.exitCode = code; break; }
      await copyFile(path.join(here, "kekkai_runtime.js"), path.join(out, "kekkai_runtime.js"));
      await copyFile(path.join(here, "run.mjs"), path.join(out, "run.mjs"));
      const r = spawnSync(process.execPath, [path.join(out, "run.mjs"), out, ...args.slice(1)], { stdio: "inherit" });
      process.exitCode = r.status ?? 1;
    } finally {
      await rm(out, { recursive: true, force: true });
    }
    break;
  }
  case "test": {
    const { kekTest } = await import(pathToFileURL(path.join(here, "kek_test.mjs")).href);
    const stage = await currentStage();
    process.exitCode = await kekTest(args, { compile: (argv) => runStage(stage, argv), jsDir: here });
    break;
  }
  case undefined:
  case "help":
  case "-h":
  case "--help":
    console.log(`kek — the Kekkai toolchain (self-hosted)

Usage:
  kek check <file|dir>            type-check (capabilities, effects, transactions)
  kek ir <file|dir>               print the intermediate representation
  kek build [-o dir] <file|dir>   compile to WasmGC + JS glue
  kek run <file|dir> [args...]    compile a #[main] program and run it on Node
  kek test [-run re] [-seed n] [-clock ms] [-net f.json] [-db f.json] <file|dir>
                                  run the #[test] functions on Node with mock capabilities

Compiler internals:
  kek lex <file> | kek ast <file> token / syntax tree dumps
  kek ir2wasm <ir.json> <out.wasm> [<meta.json>]
  kek test-build <file|dir> <outdir> [-list | -run name...]

Maintenance:
  kek bootstrap-check             rebuild the compiler with itself; require a fixed point
  kek bootstrap-update            replace bootstrap/ with the current compiler (after the check)`);
    break;
  default:
    process.exitCode = await runStage(await currentStage(), [cmd, ...args]);
}
