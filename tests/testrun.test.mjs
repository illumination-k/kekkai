// Tests of `kek test` (compiler/testrun.kek + js/kek_test.mjs + js/test_runner.mjs),
// run through the launcher without Go:
//
//   node --test tests/testrun.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const kekBin = path.join(root, "js", "kek.mjs");

function kek(...args) {
  const r = spawnSync(process.execPath, [kekBin, ...args], { cwd: root, encoding: "utf8" });
  return { out: r.stdout + r.stderr, stdout: r.stdout, stderr: r.stderr, code: r.status };
}

async function withTmp(fn) {
  const dir = await mkdtemp(path.join(tmpdir(), "kek-testrun-"));
  try {
    return await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

function contains(out, wants) {
  for (const w of wants) assert.ok(out.includes(w), `missing ${JSON.stringify(w)} in:\n${out}`);
}

test("discover", () =>
  withTmp(async (dir) => {
    const r = kek("test-build", "testdata/test/counter.kek", dir, "-list");
    assert.equal(r.code, 0, r.out);
    const tests = JSON.parse(await readFile(path.join(dir, "tests.json"), "utf8"));
    const got = tests.map((t) => `${t.name}:${t.result}:${t.caps.join(",")}`).join(" ");
    assert.equal(
      got,
      "key_format:bool: parse_count_defaults_to_zero:result: visits_are_counted:result:Db,Log " +
        "greeting_falls_back_offline:bool:Net clock_is_fixed:bool:Clock random_in_range:bool:Random",
    );
    assert.deepEqual(tests.map((t) => t.pure), [true, true, false, false, false, false]);
  }));

// The synthesized harness compiles for every result kind and capability mix.
test("harness builds", () =>
  withTmp(async (dir) => {
    const src = path.join(dir, "h.kek");
    await writeFile(
      src,
      `#[test]
fn a() {}

#[test]
fn b(db: &Db, log: &Log) -> Result<(), String> {
    log.info("b");
    Ok(())
}

#[test]
fn c(net: &Net) -> Bool {
    true
}
`,
    );
    const out = path.join(dir, "out");
    await mkdir(out);
    const r = kek("test-build", src, out);
    assert.equal(r.code, 0, r.out);
    const meta = await readFile(path.join(out, "kekkai_meta.js"), "utf8");
    for (const k of ["request", "Log", "Db", "Net"]) assert.ok(meta.includes(`"kind": "${k}"`), meta);
    assert.ok(!meta.includes(`"kind": "Clock"`), meta);
  }));

test("run passing", () => {
  const r = kek("test", "testdata/test/counter.kek");
  assert.equal(r.code, 0, r.out);
  contains(r.stdout, [
    "running 6 tests from testdata/test/counter.kek",
    "test key_format ... ok (pure: hermetic, cacheable;",
    "test visits_are_counted ... ok (mock Db, Log;",
    "test result: ok. 6 passed; 0 failed",
  ]);
});

test("run failing", () =>
  withTmp(async (dir) => {
    let r = kek("test", "testdata/test/failing.kek");
    assert.equal(r.code, 1, r.out);
    contains(r.stdout, [
      "test passes ... ok",
      "test returns_false ... FAILED",
      "    returned false",
      "test returns_err ... FAILED (mock Log;",
      "    Err: expected 3, got 2",
      "    info: about to fail",
      "test unit_passes ... ok",
      "test canned_net ... FAILED",
      "no canned response for GET https://api.example/x",
      "test result: FAILED. 2 passed; 3 failed",
    ]);

    // canned &Net responses and -run selection
    const net = path.join(dir, "net.json");
    await writeFile(net, JSON.stringify({ "GET https://api.example/x": "hello" }));
    r = kek("test", "-run", "^canned_net$", "-net", net, "testdata/test/failing.kek");
    assert.equal(r.code, 0, r.out);
    contains(r.stdout, ["running 1 test from testdata/test/failing.kek", "test result: ok. 1 passed; 0 failed"]);
  }));

// A program's own #[handler] is demoted so the harness can take its place.
test("run with handler", () =>
  withTmp(async (dir) => {
    const src = path.join(dir, "bank.kek");
    await writeFile(
      src,
      (await readFile(path.join(root, "testdata/e2e/bank.kek"), "utf8")) +
        `
#[test]
fn amount_parsing() -> Bool {
    match parse_amount("12") {
        Some(n) => n == 12,
        None => false,
    }
}
`,
    );
    const r = kek("test", src);
    assert.equal(r.code, 0, r.out);
    contains(r.stdout, ["test amount_parsing ... ok", "test result: ok. 1 passed; 0 failed"]);
  }));

test("no tests", () => {
  let r = kek("test", "testdata/e2e/bank.kek");
  assert.equal(r.code, 0, r.out);
  assert.equal(r.stdout, "testdata/e2e/bank.kek: no tests\n");
  r = kek("test", "-run", "zzz", "testdata/test/counter.kek");
  assert.equal(r.code, 0, r.out);
  assert.equal(r.stdout, "testdata/test/counter.kek: no tests\n");
});

test("errors and usage", () => {
  let r = kek("test", "testdata/check/err_tx.kek");
  assert.equal(r.code, 1);
  assert.ok(r.stderr.startsWith("testdata/check/err_tx.kek:4:9: transaction `tx` is never committed"), r.out);
  r = kek("test");
  assert.equal(r.code, 1);
  assert.equal(r.stderr, "kek test: expected exactly one .kek file\n");
  r = kek("test", "-x", "a.kek");
  assert.equal(r.code, 2);
  assert.ok(r.stderr.startsWith("flag provided but not defined: -x\nUsage of test:\n"), r.out);
  r = kek("test", "-seed", "abc", "a.kek");
  assert.equal(r.code, 2);
  r = kek("test", "nofile.kek");
  assert.equal(r.code, 1);
  assert.equal(r.stderr, "stat nofile.kek: no such file or directory\n");
  r = kek("test", "-net", "nope.json", "testdata/test/counter.kek");
  assert.equal(r.code, 1);
  assert.equal(r.stderr, "open nope.json: no such file or directory\n");
});

test("mock options", () =>
  withTmp(async (dir) => {
    const src = path.join(dir, "opts.kek");
    await writeFile(
      src,
      `#[test]
fn clock_value(clock: &Clock) -> Bool {
    clock.now_ms() == 1000
}

#[test]
fn db_seeded(db: &Db) -> Result<(), String> {
    match db.get("k") {
        Ok(Some(v)) => if v == "v" { Ok(()) } else { Err("got " + v) },
        Ok(None) => Err("missing"),
        Err(e) => Err(e.message()),
    }
}
`,
    );
    const db = path.join(dir, "db.json");
    await writeFile(db, JSON.stringify({ k: "v" }));
    const r = kek("test", "-clock", "1000", "-db", db, src);
    assert.equal(r.code, 0, r.out);
    contains(r.stdout, ["test result: ok. 2 passed; 0 failed"]);
    const r2 = kek("test", src);
    assert.equal(r2.code, 1, r2.out);
    contains(r2.stdout, ["    returned false", "    Err: missing", "test result: FAILED. 0 passed; 2 failed"]);
  }));

test("help lists test", () => {
  const r = kek("help");
  assert.equal(r.code, 0);
  assert.ok(r.stdout.includes("kek test"), r.stdout);
});
