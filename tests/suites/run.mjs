// run: the #[main] programs in testdata/run are run with `./kek run` and a
// temporary file path as the only argument; stdout must equal the .out
// file and the exit code the `// exit: N` comment on the first line.
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fail, root, runKek, withTmp } from "../lib.mjs";

async function runProgram(file) {
  const src = await readFile(file, "utf8");
  const want = await readFile(file.replace(/\.kek$/, ".out"), "utf8");
  const m = /^\/\/ exit: (-?\d+)/.exec(src);
  const wantCode = m ? Number(m[1]) : 0;
  await withTmp(async (dir) => {
    const r = await runKek(["run", file, path.join(dir, "scratch.txt")]);
    const errs = [];
    if (r.code !== wantCode) errs.push(`exit code ${r.code}, want ${wantCode}\n${r.stderr}`);
    if (r.stdout !== want) errs.push(`stdout:\n${r.stdout}\nwant:\n${want}\nstderr:\n${r.stderr}`);
    if (errs.length) fail(errs.join("\n"));
  });
}

export async function runCases() {
  const dir = path.join(root, "testdata/run");
  const files = (await readdir(dir)).filter((f) => f.endsWith(".kek")).sort();
  return files.map((f) => ({ name: f.replace(/\.kek$/, ""), run: () => runProgram(path.join(dir, f)) }));
}
