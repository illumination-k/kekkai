// node run_pure.mjs <outdir> <calls.json>
// Calls exported pure functions of a compiled program and prints a JSON
// array of results: {"ok": "<i64 decimal>"} or {"error": "..."}.
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";

const [outDir, callsFile] = process.argv.slice(2);
const url = (p) => pathToFileURL(path.resolve(p)).href;
const runtime = await import(url(path.join(outDir, "kekkai_runtime.js")));
const meta = (await import(url(path.join(outDir, "kekkai_meta.js")))).default;
const module = new WebAssembly.Module(await readFile(path.join(outDir, "module.wasm")));
const app = runtime.createKekkai(module, meta);
const calls = JSON.parse(await readFile(callsFile, "utf8"));
const out = [];
for (const c of calls) {
  try {
    const f = app.exports["fn_" + c.fn];
    const args = c.args.map((a) => (typeof a === "boolean" ? (a ? 1 : 0) : BigInt(a)));
    out.push({ ok: f(...args).toString() });
  } catch (e) {
    out.push({ error: String(e) });
  }
}
console.log(JSON.stringify(out));
