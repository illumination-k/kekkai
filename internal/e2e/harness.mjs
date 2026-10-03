// E2E harness: node harness.mjs <outdir> <test.mjs>
// Loads a compiled Kekkai program and runs the test module against it.
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import assert from "node:assert/strict";
import path from "node:path";

const [outDir, testFile] = process.argv.slice(2);
const url = (p) => pathToFileURL(path.resolve(p)).href;
const runtime = await import(url(path.join(outDir, "kekkai_runtime.js")));
const meta = (await import(url(path.join(outDir, "kekkai_meta.js")))).default;
const module = new WebAssembly.Module(await readFile(path.join(outDir, "module.wasm")));
const test = await import(url(testFile));

// Helpers shared by tests.
function recordingLog() {
  const lines = [];
  return { lines, info: (m) => lines.push(["info", m]), warn: (m) => lines.push(["warn", m]), error: (m) => lines.push(["error", m]) };
}
function recordingOutbox() {
  const sent = [];
  const deliver = async (e) => { sent.push(e); };
  deliver.sent = sent;
  return deliver;
}
async function call(app, caps, method, pathAndQuery, body) {
  const req = new Request("http://test" + pathAndQuery, { method, body });
  const res = await app.handle(req, caps);
  return { status: res.status, body: await res.text(), headers: res.headers };
}

await test.default({
  createApp: () => runtime.createKekkai(module, meta),
  runtime, meta, module, assert, recordingLog, recordingOutbox, call,
});
console.log("ok");
