// difftest: random programs (tests/difftest/gen.mjs) are compiled with
// `./kek build`, run as WasmGC on Node (tests/difftest/run_pure.mjs) and,
// when the Lean reference interpreter is built (`mise run lean`, or
// KEKKAI_REF=<path>), interpreted from their `ir -json` and compared.
//
// Where the IR JSON comes from (--ir=MODE):
//   auto  (default) `./kek ir -json` if it works, else none
//   kek   `./kek ir -json <file>`
//   none  only check that the wasm runs without traps
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { Gen, argSets } from "../difftest/gen.mjs";
import { exec, exists, kek, fail, node, root, runKek, withTmp } from "../lib.mjs";

const runPure = path.join(root, "tests/difftest/run_pure.mjs");

async function refBinary() {
  const p = process.env.KEKKAI_REF || path.join(root, "lean/.lake/build/bin/kekkai-ref");
  return (await exists(p)) ? p : null;
}

const probeSrc = "fn f(a: Int, b: Int, c: Bool) -> Int {\n    a + b\n}\n";

async function irJsonWorks(cmd, prefix) {
  return await withTmp(async (dir) => {
    const f = path.join(dir, "probe.kek");
    await writeFile(f, probeSrc);
    const r = await exec(cmd, [...prefix, "ir", "-json", f]);
    if (r.code !== 0) return false;
    try { JSON.parse(r.stdout); return true; } catch { return false; }
  });
}

/** Resolve the IR source: returns { mode, irJson(file) -> string } or { mode: "none" }. */
async function irSource(mode) {
  if (mode === "none") return { mode };
  if (mode === "kek" || mode === "auto") {
    if (await irJsonWorks(node, [kek])) {
      return { mode: "kek", irJson: async (f) => runKek(["ir", "-json", f]) };
    }
    if (mode === "kek") throw new Error("--ir=kek: `./kek ir -json` failed");
  }
  return { mode: "none" };
}

/** Raw JSON text of the "ok" number in kekkai-ref output (exact, no float rounding). */
function refResult(out) {
  const ok = /"ok":(-?\d+)/.exec(out);
  if (ok) return { ok: ok[1] };
  const err = /"error":("(?:[^"\\]|\\.)*")/.exec(out);
  if (err) return { error: JSON.parse(err[1]) };
  return null;
}

async function oneSeed(seed, ref, ir, { keep }) {
  const prog = new Gen(seed).generate();
  const dir = await mkdtemp(path.join(tmpdir(), `kekkai-difftest-${seed}-`));
  let ok = false;
  try {
    const src = path.join(dir, "prog.kek");
    await writeFile(src, prog.source);
    const b = await runKek(["build", "-o", dir, src]);
    if (b.code !== 0) fail(`seed ${seed}: generated program does not compile:\n${b.out}\nprogram kept at ${src}`);
    const calls = [];
    for (const fn of prog.funcs) for (const args of argSets) calls.push({ fn, args });
    await writeFile(path.join(dir, "calls.json"), JSON.stringify(calls));
    // generated programs terminate: a hang in wasm is a bug
    const r = await exec(node, [runPure, dir, path.join(dir, "calls.json")], { timeout: 120000 });
    if (r.code !== 0) fail(`seed ${seed}: node: exit ${r.code}\n${r.out}\nprogram kept at ${src}`);
    const wasm = JSON.parse(r.stdout);
    const errs = [];
    calls.forEach((c, i) => {
      if (wasm[i].error !== undefined) errs.push(`${c.fn}(${c.args.join(", ")}) trapped in wasm: ${wasm[i].error}`);
    });
    if (ref && ir.mode !== "none" && errs.length === 0) {
      const irFile = path.join(dir, "ir.json");
      const j = await ir.irJson(src);
      if (j.code !== 0) fail(`seed ${seed}: ir -json failed (${ir.mode}):\n${j.out}`);
      await writeFile(irFile, j.stdout);
      for (const [i, c] of calls.entries()) {
        const rr = await exec(ref, [irFile, c.fn, ...c.args.map(String)]);
        const res = refResult(rr.stdout);
        if (!res) fail(`seed ${seed}: bad kekkai-ref output (exit ${rr.code}): ${rr.out}`);
        // all generated functions return Int: compare the exact JSON number
        if (res.error !== undefined || res.ok !== wasm[i].ok) {
          errs.push(`${c.fn}(${c.args.join(", ")}): wasm=${wasm[i].ok} reference=${res.ok ?? ""}${res.error ?? ""}`);
        }
      }
    }
    if (errs.length) fail(`seed ${seed}:\n${errs.join("\n")}\nprogram kept at ${src}`);
    ok = true;
  } finally {
    if (ok && !keep) await rm(dir, { recursive: true, force: true });
  }
  return null;
}

export async function difftestCases(opts) {
  const ref = await refBinary();
  const ir = ref ? await irSource(opts.ir) : { mode: "none" };
  const note = !ref
    ? "Lean reference interpreter not built (mise run lean, or KEKKAI_REF=...): checking that wasm runs without traps only"
    : ir.mode === "none"
      ? "no `ir -json` available: checking that wasm runs without traps only"
      : `comparing with ${ref} (IR from ./kek ir -json)`;
  const cases = [];
  for (let s = opts.seed; s < opts.seed + opts.n; s++) {
    cases.push({ name: `seed${s}`, run: () => oneSeed(s, ref, ir, opts) });
  }
  return { note, cases };
}
