// Runs a compiled #[main] program on Node: node run.mjs <outdir> [args...]
// Capabilities granted to command-line programs are created here.
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
import path from "node:path";

const [outDir, ...argv] = process.argv.slice(2);
const url = (p) => pathToFileURL(path.resolve(p)).href;
const rt = await import(url(path.join(outDir, "kekkai_runtime.js")));
const meta = (await import(url(path.join(outDir, "kekkai_meta.js")))).default;
const module = new WebAssembly.Module(await readFile(path.join(outDir, "module.wasm")));
const app = rt.createKekkai(module, meta);

const caps = {};
for (const p of meta.handlerParams) {
  switch (p.kind) {
    case "Fs": caps[p.name] = await rt.nodeFs(); break;
    case "Log": caps[p.name] = rt.consoleLog(); break;
    case "Net": caps[p.name] = rt.fetchNet(); break;
    case "Clock": caps[p.name] = rt.systemClock(); break;
    case "Random": caps[p.name] = rt.cryptoRandom(); break;
    case "Db": caps[p.name] = rt.dbCap(new rt.MemoryStore()); break;
  }
}
process.exitCode = await app.main(argv, caps);
