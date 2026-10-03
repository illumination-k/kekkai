// fmt-roundtrip: `./kek fmt` over every testdata/**/*.kek, compiler/*.kek
// and random generated programs (tests/difftest/gen.mjs) must be
// idempotent, keep the AST (`./kek ast` with positions stripped), keep the
// comments, and keep the `./kek check` diagnostics (positions stripped).
// The golden files and the -check / -w behaviour are covered by
// tests/fmt.test.mjs.
import { mkdir, readdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import { Gen } from "../difftest/gen.mjs";
import { fail, rel, root, runKek, withTmp } from "../lib.mjs";

// The comments of a Kekkai source (line and block comments, with their
// delimiters), sorted.
export function comments(src) {
  const out = [];
  let i = 0;
  const n = src.length;
  while (i < n) {
    const c = src[i];
    if (c === '"') {
      i++;
      while (i < n && src[i] !== '"' && src[i] !== "\n") i += src[i] === "\\" ? 2 : 1;
      i++;
    } else if (c === "/" && src[i + 1] === "/") {
      let j = src.indexOf("\n", i);
      if (j < 0) j = n;
      out.push(src.slice(i, j).replace(/\r+$/, ""));
      i = j;
    } else if (c === "/" && src[i + 1] === "*") {
      let j = src.indexOf("*/", i + 2);
      j = j < 0 ? n : j + 2;
      out.push(src.slice(i, j).replace(/\r+$/, ""));
      i = j;
    } else {
      i++;
    }
  }
  return out.sort();
}

const stripAst = (s) => s.replace(/ @\d+:\d+/g, "").replace(/ end=\d+:\d+/g, "");

/** Diagnostics of `./kek check` without file and positions, sorted. */
async function checkMsgs(file) {
  const r = await runKek(["check", file]);
  return r.stderr.split("\n").filter((l) => l.trim() !== "")
    .map((l) => l.replace(/^.*?:\d+:\d+: /, "")).sort();
}

async function fmt(file) {
  const r = await runKek(["fmt", file]);
  if (r.code !== 0) fail(`kek fmt ${rel(file)} failed (exit ${r.code}):\n${r.stderr}`);
  return r.stdout;
}

async function roundTrip(name, src) {
  await withTmp(async (dir) => {
    const before = path.join(dir, "before", name), after = path.join(dir, "after", name);
    await mkdir(path.dirname(before), { recursive: true });
    await mkdir(path.dirname(after), { recursive: true });
    await writeFile(before, src);
    const out = await fmt(before);
    await writeFile(after, out);
    const again = await fmt(after);
    const errs = [];
    if (again !== out) errs.push(`not idempotent:\n--- first\n${out}\n--- second\n${again}`);
    const [a1, a2] = await Promise.all([runKek(["ast", before]), runKek(["ast", after])]);
    if (stripAst(a1.stdout) !== stripAst(a2.stdout) || a1.code !== a2.code) {
      errs.push(`AST changed:\n--- before\n${a1.out}\n--- after\n${a2.out}\n--- output\n${out}`);
    }
    const c1 = comments(src), c2 = comments(out);
    if (JSON.stringify(c1) !== JSON.stringify(c2)) errs.push(`comments changed:\n${JSON.stringify(c1)}\n${JSON.stringify(c2)}`);
    const [e1, e2] = await Promise.all([checkMsgs(before), checkMsgs(after)]);
    if (JSON.stringify(e1) !== JSON.stringify(e2)) errs.push(`type-check result changed:\n${JSON.stringify(e1)}\n${JSON.stringify(e2)}`);
    if (errs.length) fail(errs.join("\n"));
  });
}

async function walk(dir, out = []) {
  for (const e of await readdir(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) await walk(p, out);
    else if (e.name.endsWith(".kek")) out.push(p);
  }
  return out;
}

export async function fmtCases(opts) {
  const files = [...(await walk(path.join(root, "testdata"))), ...(await walk(path.join(root, "compiler")))].sort();
  if (files.length === 0) throw new Error("no .kek files");
  const cases = files.map((f) => ({
    name: rel(f),
    run: async () => roundTrip(path.basename(f), await readFile(f, "utf8")),
  }));
  for (let s = 1; s <= opts.fmtGenerated; s++) {
    cases.push({ name: `generated/seed${s}`, run: () => roundTrip("prog.kek", new Gen(s).generate().source) });
  }
  return cases;
}
