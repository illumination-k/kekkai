#!/usr/bin/env node
// Test entry point: needs only Node and ./kek (the self-hosted compiler).
//
//   node tests/run.mjs [options] [filter...]
//
// Suites (a filter selects the cases whose "suite/name" contains it):
//   bootstrap     ./kek bootstrap-check: the self-hosting fixed point
//   check         testdata/check/*.kek against their `// ERROR "..."` annotations
//   bad-examples  examples/*/*.bad.kek are rejected (`// kek check: ...`)
//   run           testdata/run/*.kek: stdout == .out, exit code == `// exit: N`
//   e2e           testdata/e2e and examples/ programs against their *.test.mjs
//   adapters      the store adapter conformance suite
//   difftest      random programs: WasmGC vs the Lean reference interpreter
//   workers       bank.kek on workerd via wrangler (skipped without wrangler)
//   fmt           kek fmt round trips: idempotent, same AST, comments and diagnostics
//   node-test     node --test tests/*.test.mjs (kek fmt goldens, kek test, ...)
//
// Options:
//   -j N          parallel jobs (default: available parallelism)
//   -n N          difftest: number of random programs (default 60)
//   --fmt-gen N   fmt: number of random programs to round-trip (default 40)
//   --seed S      difftest: first seed (default 1)
//   --ir MODE     difftest: where `ir -json` comes from: auto|kek|none (default auto)
//   --keep        difftest: keep the generated programs
//   --short       skip the slow suites (workers)
//   -v            print the output of passing cases too
import os from "node:os";
import { TestFailure, TestSkip } from "./lib.mjs";
import { bootstrapCases } from "./suites/bootstrap.mjs";
import { badExampleCases, checkCases } from "./suites/check.mjs";
import { difftestCases } from "./suites/difftest.mjs";
import { fmtCases } from "./suites/fmt.mjs";
import { nodeTestCases } from "./suites/node_test.mjs";
import { adapterCases, e2eCases } from "./suites/e2e.mjs";
import { runCases } from "./suites/run.mjs";
import { workersCases } from "./suites/workers.mjs";

const opts = { jobs: os.availableParallelism(), n: 60, seed: 1, ir: "auto", keep: false, short: false, verbose: false, fmtGenerated: 40 };
const filters = [];
const argv = process.argv.slice(2);
for (let i = 0; i < argv.length; i++) {
  const a = argv[i];
  const val = () => {
    const v = argv[++i];
    if (v === undefined) { console.error(`${a}: missing value`); process.exit(2); }
    return v;
  };
  if (a === "-j") opts.jobs = Math.max(1, Number(val()));
  else if (a === "-n") opts.n = Number(val());
  else if (a === "--seed") opts.seed = Number(val());
  else if (a === "--fmt-gen") opts.fmtGenerated = Number(val());
  else if (a === "--ir") opts.ir = val();
  else if (a.startsWith("--ir=")) opts.ir = a.slice(5);
  else if (a === "--keep") opts.keep = true;
  else if (a === "--short") opts.short = true;
  else if (a === "-v") opts.verbose = true;
  else if (a === "-h" || a === "--help") {
    const src = await import("node:fs").then((fs) => fs.readFileSync(new URL(import.meta.url), "utf8"));
    console.log(src.split("\n").slice(1).filter((l) => l.startsWith("//")).map((l) => l.slice(3)).join("\n"));
    process.exit(0);
  } else if (a.startsWith("-")) { console.error(`unknown option ${a}`); process.exit(2); }
  else filters.push(a);
}
if (!["auto", "kek", "none"].includes(opts.ir)) { console.error(`--ir: unknown mode ${opts.ir}`); process.exit(2); }

const suites = [
  ["bootstrap", bootstrapCases],
  ["workers", workersCases], // slowest: start it early
  ["node-test", nodeTestCases],
  ["difftest", difftestCases],
  ["fmt", fmtCases],
  ["check", checkCases],
  ["bad-examples", badExampleCases],
  ["run", runCases],
  ["e2e", e2eCases],
  ["adapters", adapterCases],
];

const selected = (id) => filters.length === 0 || filters.some((f) => id.includes(f));
const t0 = Date.now();
const results = { pass: 0, fail: 0, skip: 0 };
const failures = [];
const secs = (ms) => (ms / 1000).toFixed(1) + "s";

function report(id, ms, err, out) {
  if (err instanceof TestSkip) {
    results.skip++;
    console.log(`skip ${id}: ${err.message}`);
  } else if (err) {
    results.fail++;
    const msg = err instanceof TestFailure ? err.message : (err.stack || String(err));
    failures.push(id);
    console.log(`FAIL ${id} (${secs(ms)})\n${msg.replace(/^/gm, "     ")}`);
  } else {
    results.pass++;
    console.log(`ok   ${id} (${secs(ms)})`);
    if (opts.verbose && out) console.log(String(out).replace(/^/gm, "     "));
  }
}

async function runCase(id, c) {
  const start = Date.now();
  let timer;
  try {
    const limit = c.timeout ?? 600000;
    const out = await Promise.race([
      c.run(),
      new Promise((_, rej) => { timer = setTimeout(() => rej(new TestFailure(`timed out after ${secs(limit)}`)), limit); }),
    ]);
    report(id, Date.now() - start, null, out);
  } catch (e) {
    report(id, Date.now() - start, e);
  } finally {
    clearTimeout(timer);
  }
}

// Collect cases. The bootstrap check runs alone first: it builds (and
// caches) the compiler every other case uses.
const first = [], rest = [];
for (const [suite, make] of suites) {
  let made;
  try {
    made = await make(opts);
  } catch (e) {
    report(suite, 0, e);
    continue;
  }
  if (made.skip) {
    if (selected(suite)) { results.skip++; console.log(`skip ${suite}: ${made.skip}`); }
    continue;
  }
  const cases = Array.isArray(made) ? made : made.cases;
  const chosen = cases.filter((c) => selected(`${suite}/${c.name}`));
  if (made.note && chosen.length) console.log(`note ${suite}: ${made.note}`);
  for (const c of chosen) (suite === "bootstrap" ? first : rest).push([`${suite}/${c.name}`, c]);
}

for (const [id, c] of first) await runCase(id, c);
if (first.length === 0 && rest.length > 0) {
  // warm the compiler cache once instead of in every parallel job
  const { runKek } = await import("./lib.mjs");
  await runKek(["check", new URL("../testdata/check/ok_basic.kek", import.meta.url).pathname]);
}

let next = 0;
await Promise.all(Array.from({ length: Math.min(opts.jobs, rest.length) }, async () => {
  while (next < rest.length) {
    const [id, c] = rest[next++];
    await runCase(id, c);
  }
}));

console.log(`\n${results.pass} passed, ${results.fail} failed, ${results.skip} skipped in ${secs(Date.now() - t0)}`);
if (failures.length) console.log(`failed:\n  ${failures.join("\n  ")}`);
process.exitCode = results.fail ? 1 : 0;
