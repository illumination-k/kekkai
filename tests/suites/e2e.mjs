// e2e: every testdata/e2e/*.kek and every examples/*/*.kek with a sibling
// *.test.mjs is built with `./kek build`, its worker.js (if any) must
// parse, and the scenario runs against it through tests/e2e/harness.mjs.
//
// adapters: the store adapter conformance suite (MemoryStore, D1KvStore on
// a node:sqlite D1, DurableObjectStore, RemoteKvStore).
import { readdir } from "node:fs/promises";
import path from "node:path";
import { build, exec, exists, fail, node, rel, root, withTmp } from "../lib.mjs";

const harness = path.join(root, "tests/e2e/harness.mjs");

async function programs() {
  const files = [];
  const e2e = path.join(root, "testdata/e2e");
  for (const f of (await readdir(e2e)).sort()) if (f.endsWith(".kek")) files.push(path.join(e2e, f));
  const ex = path.join(root, "examples");
  for (const d of (await readdir(ex)).sort()) {
    let entries;
    try { entries = await readdir(path.join(ex, d)); } catch { continue; }
    for (const f of entries.sort()) {
      if (!f.endsWith(".kek") || f.endsWith(".bad.kek")) continue;
      const file = path.join(ex, d, f);
      if (await exists(file.replace(/\.kek$/, ".test.mjs"))) files.push(file);
    }
  }
  if (files.length === 0) throw new Error("no e2e programs");
  return files;
}

async function e2e(file) {
  await withTmp(async (dir) => {
    await build(file, dir);
    const worker = path.join(dir, "worker.js");
    if (await exists(worker)) {
      const r = await exec(node, ["--check", worker]);
      if (r.code !== 0) fail(`generated worker.js does not parse:\n${r.out}`);
    }
    const r = await exec(node, [harness, dir, file.replace(/\.kek$/, ".test.mjs")]);
    if (r.code !== 0) fail(`${rel(file)}: scenario failed (exit ${r.code})\n${r.out}`);
  });
}

export async function e2eCases() {
  return (await programs()).map((f) => ({ name: path.basename(f, ".kek"), run: () => e2e(f) }));
}

export async function adapterCases() {
  return [{
    name: "conformance",
    run: async () => {
      const r = await exec(node, [path.join(root, "tests/e2e/adapters.test.mjs")]);
      if (r.code !== 0) fail(`exit ${r.code}\n${r.out}`);
      return r.out;
    },
  }];
}
