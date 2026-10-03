// node-test: the node:test files tests/*.test.mjs (kek fmt goldens and
// -check/-w, kek test, ...), one case per file run with `node --test`.
import { readdir } from "node:fs/promises";
import path from "node:path";
import { exec, fail, node, root } from "../lib.mjs";

export async function nodeTestCases() {
  const dir = path.join(root, "tests");
  const files = (await readdir(dir)).filter((f) => f.endsWith(".test.mjs")).sort();
  return files.map((f) => ({
    name: f,
    run: async () => {
      const r = await exec(node, ["--test", "--test-reporter=dot", path.join(dir, f)]);
      if (r.code !== 0) {
        // rerun with the spec reporter for a readable failure report
        const s = await exec(node, ["--test", "--test-reporter=spec", path.join(dir, f)]);
        fail(`node --test ${f}: exit ${r.code}\n${s.out}`);
      }
      return r.out;
    },
  }));
}
