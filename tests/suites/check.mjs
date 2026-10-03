// check: every testdata/check/*.kek is run through `./kek check`. Lines
// annotated with `// ERROR "substring"` must produce a matching diagnostic
// on that line; no other diagnostics are allowed.
//
// bad-examples: the *.bad.kek files under examples/ must be rejected with
// the diagnostic named in their first line (`// kek check: <substring>`).
import { readFile, readdir } from "node:fs/promises";
import path from "node:path";
import { fail, rel, root, runKek } from "../lib.mjs";

const errorRe = /ERROR "([^"]*)"/g;
const diagRe = /^(.*?):(\d+):(\d+): (.*)$/;

/** Parse `file:line:col: message` lines; other non-empty lines are kept as-is. */
export function parseDiagnostics(text) {
  const out = [];
  for (const line of text.split("\n")) {
    if (line.trim() === "") continue;
    const m = diagRe.exec(line);
    out.push(m ? { line: Number(m[2]), col: Number(m[3]), msg: m[4], text: line } : { line: 0, col: 0, msg: line, text: line });
  }
  return out;
}

async function checkFile(file) {
  const src = await readFile(file, "utf8");
  const want = [];
  src.split("\n").forEach((l, i) => {
    for (const m of l.matchAll(errorRe)) want.push({ line: i + 1, sub: m[1] });
  });
  const r = await runKek(["check", file]);
  const got = parseDiagnostics(r.stderr);
  const errs = [];
  if (r.code !== 0 && got.length === 0) errs.push(`check exited ${r.code} without diagnostics\n${r.out}`);
  if (r.code === 0 && got.length > 0) errs.push(`check exited 0 but printed diagnostics`);
  const matched = new Set();
  for (const { line, sub } of want) {
    const i = got.findIndex((e, i) => !matched.has(i) && e.line === line && e.msg.includes(sub));
    if (i < 0) errs.push(`${line}: missing error matching ${JSON.stringify(sub)}`);
    else matched.add(i);
  }
  got.forEach((e, i) => { if (!matched.has(i)) errs.push(`unexpected error: ${e.text}`); });
  if (errs.length) fail(errs.join("\n"));
}

async function badExample(file) {
  const src = await readFile(file, "utf8");
  const first = src.split("\n", 1)[0];
  const prefix = "// kek check: ";
  if (!first.startsWith(prefix)) fail("first line must be `// kek check: <expected error>`");
  const want = first.slice(prefix.length);
  const r = await runKek(["check", file]);
  if (r.code === 0) fail(`expected a type error containing ${JSON.stringify(want)}`);
  if (!r.stderr.includes(want)) fail(`expected error containing ${JSON.stringify(want)}, got:\n${r.out}`);
}

export async function checkCases() {
  const dir = path.join(root, "testdata/check");
  const files = (await readdir(dir)).filter((f) => f.endsWith(".kek")).sort();
  if (files.length === 0) throw new Error("no testdata/check files");
  return files.map((f) => ({ name: f, run: () => checkFile(path.join(dir, f)) }));
}

export async function badExampleCases() {
  const out = [];
  const ex = path.join(root, "examples");
  for (const d of (await readdir(ex)).sort()) {
    let entries;
    try { entries = await readdir(path.join(ex, d)); } catch { continue; }
    for (const f of entries.filter((f) => f.endsWith(".bad.kek")).sort()) {
      const file = path.join(ex, d, f);
      out.push({ name: rel(file), run: () => badExample(file) });
    }
  }
  return out;
}
