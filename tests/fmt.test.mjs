// Tests of `kek fmt` (compiler/fmt_*.kek) through the ./kek launcher, with
// no Go toolchain: golden files, idempotence, and -check / -w over
// directories.
//
//   node --test tests/
import { test } from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, mkdir, readFile, readdir, rm, writeFile, copyFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const kek = path.join(root, "js", "kek.mjs");
const goldenDir = path.join(root, "tests", "fmt");

function run(...args) {
  const r = spawnSync(process.execPath, [kek, ...args], { cwd: root, encoding: "utf8" });
  return { code: r.status, stdout: r.stdout, stderr: r.stderr };
}

async function withTemp(fn) {
  const dir = await mkdtemp(path.join(tmpdir(), "kek-fmt-"));
  try {
    return await fn(dir);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
}

const inputs = (await readdir(goldenDir)).filter((f) => f.endsWith(".in.kek")).sort();

test("golden inputs exist", () => {
  assert.ok(inputs.length > 0);
});

for (const input of inputs) {
  const name = input.slice(0, -".in.kek".length);
  test(`golden ${name}`, async () => {
    const want = await readFile(path.join(goldenDir, name + ".golden"), "utf8");
    const r = run("fmt", path.join("tests", "fmt", input));
    assert.equal(r.code, 0, r.stderr);
    assert.equal(r.stdout, want);
    // formatting is idempotent: the golden file is already formatted
    await withTemp(async (dir) => {
      const f = path.join(dir, name + ".kek");
      await copyFile(path.join(goldenDir, name + ".golden"), f);
      const c = run("fmt", "-check", f);
      assert.equal(c.code, 0, c.stdout + c.stderr);
    });
  });
}

test("fmt -check passes on the formatted test programs", () => {
  const r = run("fmt", "-check", "testdata/test");
  assert.equal(r.code, 0, r.stdout + r.stderr);
});

test("fmt -check / -w over directories", async () => {
  await withTemp(async (dir) => {
    const ugly = "fn main()->Int{1}\n";
    await mkdir(path.join(dir, "sub"));
    await mkdir(path.join(dir, ".hidden"));
    await mkdir(path.join(dir, "node_modules"));
    await writeFile(path.join(dir, "sub", "a.kek"), ugly);
    await writeFile(path.join(dir, ".hidden", "b.kek"), ugly);
    await writeFile(path.join(dir, "node_modules", "c.kek"), ugly);
    await writeFile(path.join(dir, "d.txt"), ugly);
    await writeFile(path.join(dir, "ok.kek"), "fn main() -> Int {\n    1\n}\n");

    const c = run("fmt", "-check", dir);
    assert.equal(c.code, 1);
    assert.equal(c.stdout, path.join(dir, "sub", "a.kek") + "\n");
    assert.match(c.stderr, /1 file\(s\) need formatting/);

    const w = run("fmt", "-w", dir);
    assert.equal(w.code, 0, w.stderr);
    assert.equal(await readFile(path.join(dir, "sub", "a.kek"), "utf8"), "fn main() -> Int {\n    1\n}\n");
    assert.equal(await readFile(path.join(dir, ".hidden", "b.kek"), "utf8"), ugly);
    assert.equal(run("fmt", "-check", dir).code, 0);
  });
});

test("fmt reports parse errors", async () => {
  await withTemp(async (dir) => {
    const f = path.join(dir, "bad.kek");
    await writeFile(f, "fn main( {\n");
    const r = run("fmt", f);
    assert.equal(r.code, 1);
    assert.match(r.stderr, new RegExp(`^${f.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}:1:`));
    assert.match(r.stderr, /some files could not be parsed/);
  });
});

test("fmt without paths is an error", () => {
  assert.equal(run("fmt").code, 1);
  assert.equal(run("fmt", "-bogus", "x.kek").code, 2);
});
