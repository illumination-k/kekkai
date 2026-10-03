// `kek test [-run re] [-seed n] [-clock ms] [-net f.json] [-db f.json] <path>`
// for the launcher (js/kek.mjs). The compiler's `test-build` subcommand
// (compiler/testrun.kek) type-checks the program, discovers its #[test]
// functions and compiles them behind a synthesized #[handler]; this module
// selects tests (-run is a JavaScript regular expression), writes the plan
// and runs js/test_runner.mjs, which drives the tests with mock capabilities.
//
// Output and exit codes follow the former Go `kek test`: 0 when every test
// passed (or there are none), 1 on a failing test or an error, 2 on a usage
// error.
import { copyFile, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { spawnSync } from "node:child_process";

export const DEFAULT_CLOCK = 1767225600000n; // 2026-01-01T00:00:00Z

const flags = {
  clock: { type: "int", usage: "fixed time of the mock &Clock (ms since the epoch)", def: String(DEFAULT_CLOCK) },
  db: { type: "string", usage: `JSON file with the initial contents of the mock &Db store: {"key": "value"}` },
  net: { type: "string", usage: `JSON file of canned &Net responses: {"GET url": "body", "POST url": {"error": "msg"}}` },
  run: { type: "string", usage: "run only tests whose name matches this regular expression" },
  seed: { type: "int", usage: "seed of the mock &Random" },
};

function usage() {
  let s = "Usage of test:\n";
  for (const [name, f] of Object.entries(flags)) {
    s += `  -${name} ${f.type}\n    \t${f.usage}${f.def ? ` (default ${f.def})` : ""}\n`;
  }
  return s;
}

class UsageError extends Error {}

// An int64 literal (decimal, or 0x/0o/0b prefixed).
function parseInt64(s) {
  const m = /^([+-]?)(0[xX][0-9a-fA-F](?:_?[0-9a-fA-F])*|0[bB][01](?:_?[01])*|0[oO]?(?:_?[0-7])+|[1-9](?:_?[0-9])*|0)$/.exec(s);
  if (!m) throw new Error("parse error");
  let digits = m[2].replace(/_/g, "");
  if (/^0[0-7]+$/.test(digits)) digits = "0o" + digits.slice(1);
  let v = BigInt(digits.replace(/^0O/, "0o").replace(/^0X/, "0x").replace(/^0B/, "0b"));
  if (m[1] === "-") v = -v;
  if (v < -(1n << 63n) || v >= 1n << 63n) throw new Error("value out of range");
  return v;
}

// parseArgs: -name v, -name=v, --name; parsing
// stops at the first non-flag argument or "--".
function parseArgs(args) {
  const opts = { run: "", seed: 0n, clock: DEFAULT_CLOCK, net: "", db: "" };
  let i = 0;
  for (; i < args.length; i++) {
    const a = args[i];
    if (a.length < 2 || a[0] !== "-") break;
    if (a === "--") { i++; break; }
    let name = a.slice(a[1] === "-" ? 2 : 1);
    if (name === "" || name[0] === "-" || name[0] === "=") throw new UsageError(`bad flag syntax: ${a}`);
    let value;
    const eq = name.indexOf("=");
    if (eq >= 0) { value = name.slice(eq + 1); name = name.slice(0, eq); }
    if (name === "h" || name === "help") return { help: true };
    const f = flags[name];
    if (!f) throw new UsageError(`flag provided but not defined: -${name}`);
    if (value === undefined) {
      if (i + 1 >= args.length) throw new UsageError(`flag needs an argument: -${name}`);
      value = args[++i];
    }
    if (f.type === "int") {
      try {
        opts[name] = parseInt64(value);
      } catch (e) {
        throw new UsageError(`invalid value ${JSON.stringify(value)} for flag -${name}: ${e.message}`);
      }
    } else {
      opts[name] = value;
    }
  }
  opts.paths = args.slice(i);
  return opts;
}

async function readJSON(file) {
  if (!file) return null;
  let text;
  try {
    text = await readFile(file, "utf8");
  } catch (e) {
    throw new Error(e.code === "ENOENT" ? `open ${file}: no such file or directory` : `open ${file}: ${e.message}`);
  }
  try {
    return JSON.parse(text);
  } catch (e) {
    throw new Error(`${file}: ${e.message}`);
  }
}

/**
 * Runs `kek test`. `compile(argv)` runs a compiler command (the stage's
 * #[main]) and returns its exit code. Returns the process exit code.
 */
export async function kekTest(args, { compile, jsDir }) {
  let opts;
  try {
    opts = parseArgs(args);
  } catch (e) {
    if (!(e instanceof UsageError)) throw e;
    process.stderr.write(e.message + "\n" + usage());
    return 2;
  }
  if (opts.help) {
    process.stderr.write(usage());
    return 0;
  }
  if (opts.paths.length !== 1) {
    console.error("kek test: expected exactly one .kek file");
    return 1;
  }
  const file = opts.paths[0];
  try {
    await stat(file);
  } catch {
    console.error(`stat ${file}: no such file or directory`);
    return 1;
  }

  // Errors in the options are reported after the program's diagnostics.
  let pending = null;
  let re = null;
  let net = null;
  let db = null;
  try {
    if (opts.run !== "") {
      try {
        re = new RegExp(opts.run, "u");
      } catch (e) {
        throw new Error(`kek test: -run: ${e.message}`);
      }
    }
    net = await readJSON(opts.net);
    if (net !== null && (typeof net !== "object" || Array.isArray(net))) throw new Error(`${opts.net}: expected a JSON object`);
    db = await readJSON(opts.db);
    if (db !== null && (typeof db !== "object" || Array.isArray(db) || Object.values(db).some((v) => typeof v !== "string"))) {
      throw new Error(`${opts.db}: expected a JSON object of strings`);
    }
  } catch (e) {
    pending = e;
  }

  const dir = await mkdtemp(path.join(tmpdir(), "kek-test-"));
  try {
    const select = pending !== null || re !== null;
    let code = await compile(["test-build", file, dir, ...(select ? ["-list"] : [])]);
    if (code !== 0) return code;
    if (pending !== null) {
      console.error(pending.message);
      return 1;
    }
    let tests = JSON.parse(await readFile(path.join(dir, "tests.json"), "utf8"));
    if (re !== null) tests = tests.filter((t) => re.test(t.name));
    if (tests.length === 0) {
      console.log(`${file}: no tests`);
      return 0;
    }
    if (select) {
      code = await compile(["test-build", file, dir, "-run", ...tests.map((t) => t.name)]);
      if (code !== 0) return code;
    }
    await copyFile(path.join(jsDir, "kekkai_runtime.js"), path.join(dir, "kekkai_runtime.js"));
    const clock = opts.clock === 0n ? DEFAULT_CLOCK : opts.clock;
    // seed and clock are int64: written as raw JSON numbers
    const plan = JSON.stringify({ file, tests: tests.map(({ name, caps, result }) => ({ name, caps, result })), seed: "@SEED@", clock: "@CLOCK@", net, db })
      .replace('"@SEED@"', String(opts.seed))
      .replace('"@CLOCK@"', String(clock));
    await writeFile(path.join(dir, "plan.json"), plan);
    // stderr of the runner goes to stdout (one report stream)
    const r = spawnSync(process.execPath, [path.join(jsDir, "test_runner.mjs"), dir, path.join(dir, "plan.json")], { stdio: ["inherit", "inherit", 1] });
    if (r.status === 0) return 0;
    if (r.status === 1) return 1;
    console.error(`kek test: runner failed: ${r.signal ? "signal: " + r.signal : "exit status " + r.status}`);
    return 1;
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}
