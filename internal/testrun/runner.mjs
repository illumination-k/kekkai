// kek test runner: node runner.mjs <outdir> <plan.json>
//
// The compiled program has a synthesized #[handler] that dispatches on the
// request path to each #[test] function. Every test runs in a fresh wasm
// instance with fresh MOCK capabilities, so tests are isolated and
// deterministic:
//   &Log     records lines (shown when the test fails)
//   &Clock   fixed time (plan.clock, ms since the epoch)
//   &Random  seeded PRNG (plan.seed mixed with the test name)
//   &Db      in-memory MemoryStore (seeded from plan.db), outbox recorded
//   &Net     canned responses from plan.net ("GET url" / "POST url" keys);
//            any other request fails with NetError
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";

process.on("uncaughtException", (e) => {
  console.error("kek test: runner error:", e);
  process.exit(2);
});
process.on("unhandledRejection", (e) => {
  console.error("kek test: runner error:", e);
  process.exit(2);
});

const [outDir, planFile] = process.argv.slice(2);
const url = (p) => pathToFileURL(path.resolve(p)).href;
const runtime = await import(url(path.join(outDir, "kekkai_runtime.js")));
const meta = (await import(url(path.join(outDir, "kekkai_meta.js")))).default;
const module = new WebAssembly.Module(await readFile(path.join(outDir, "module.wasm")));
const plan = JSON.parse(await readFile(planFile, "utf8"));
const { createKekkai, MemoryStore, NetError, dbCap } = runtime;

function hash(s) {
  let h = 0xcbf29ce484222325n;
  for (const c of new TextEncoder().encode(s)) {
    h = BigInt.asUintN(64, (h ^ BigInt(c)) * 0x100000001b3n);
  }
  return h;
}

// SplitMix64 over BigInt: deterministic for a given (seed, test name).
function seededRandom(seed) {
  let state = BigInt.asUintN(64, seed);
  const next = () => {
    state = BigInt.asUintN(64, state + 0x9e3779b97f4a7c15n);
    let z = state;
    z = BigInt.asUintN(64, (z ^ (z >> 30n)) * 0xbf58476d1ce4e5b9n);
    z = BigInt.asUintN(64, (z ^ (z >> 27n)) * 0x94d049bb133111ebn);
    return z ^ (z >> 31n);
  };
  return {
    int(lo, hi) {
      if (hi <= lo) return lo;
      return lo + (next() % (hi - lo));
    },
  };
}

function mocks(test) {
  const lines = [];
  const log = {
    info: (m) => lines.push("info: " + m),
    warn: (m) => lines.push("warn: " + m),
    error: (m) => lines.push("error: " + m),
  };
  const sent = [];
  const store = new MemoryStore(plan.db || {});
  const net = {
    async get(u) { return canned("GET", u); },
    async post(u, body) { return canned("POST", u, body); },
  };
  function canned(method, u, body) {
    const key = method + " " + u;
    lines.push("net: " + key + (body === undefined ? "" : " " + JSON.stringify(body)));
    const r = (plan.net || {})[key];
    if (r === undefined) throw new NetError(`network access is mocked in tests: no canned response for ${key}`);
    if (typeof r === "object" && r !== null && "error" in r) throw new NetError(String(r.error));
    return String(r);
  }
  const byKind = {
    Log: log,
    Clock: { now: () => plan.clock },
    Random: seededRandom(BigInt(plan.seed) ^ hash(test.name)),
    Db: dbCap(store, async (e) => { sent.push(e); }),
    Net: net,
  };
  const caps = {};
  for (const p of meta.handlerParams) {
    if (p.kind !== "request") caps[p.name] = byKind[p.kind];
  }
  return { caps, lines, sent, store };
}

let passed = 0;
let failed = 0;
const n = plan.tests.length;
console.log(`running ${n} test${n === 1 ? "" : "s"} from ${plan.file}`);
for (const test of plan.tests) {
  const m = mocks(test);
  const app = createKekkai(module, meta);
  const errors = [];
  const consoleError = console.error;
  console.error = (...args) => errors.push(args.map((a) => (a instanceof Error ? a.message : String(a))).join(" "));
  const t0 = performance.now();
  let status, body;
  try {
    const res = await app.handle(new Request("http://kek.test/" + test.name), m.caps);
    status = res.status;
    body = await res.text();
  } catch (e) {
    status = 500;
    body = String(e && e.message ? e.message : e);
  } finally {
    console.error = consoleError;
  }
  const ms = (performance.now() - t0).toFixed(1);
  const kind = test.caps.length === 0 ? "pure: hermetic, cacheable" : "mock " + test.caps.join(", ");
  if (status === 200) {
    passed++;
    console.log(`test ${test.name} ... ok (${kind}; ${ms}ms)`);
    continue;
  }
  failed++;
  console.log(`test ${test.name} ... FAILED (${kind}; ${ms}ms)`);
  if (errors.length > 0) {
    // the runtime reports traps as "kekkai: handler failed: <error>"
    console.log("    trap: " + errors.join("; ").replace(/^kekkai: handler failed: /, ""));
  } else if (test.result === "bool") {
    console.log("    returned false");
  } else {
    console.log("    Err: " + body);
  }
  for (const l of m.lines) console.log("    " + l);
  for (const e of m.sent) console.log(`    outbox: ${e.url} ${JSON.stringify(e.body)}`);
}
console.log(`\ntest result: ${failed === 0 ? "ok" : "FAILED"}. ${passed} passed; ${failed} failed`);
process.exitCode = failed === 0 ? 0 : 1;
