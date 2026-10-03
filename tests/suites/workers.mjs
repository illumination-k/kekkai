// workers: testdata/e2e/bank.kek runs on workerd through
// `wrangler dev --local` (Miniflare, local D1 and Durable Objects; no
// network or Cloudflare account) and is driven over HTTP by
// tests/workers/bank.workers.mjs. It covers loading the WasmGC module in
// workerd, the generated worker.js, D1KvStore and DurableObjectStore on the
// real local backends, a forced optimistic conflict on each, real
// concurrent requests, and the outbox (KEKKAI_OUTBOX=log).
//
// Skipped when wrangler is not installed or with --short.
import { spawn } from "node:child_process";
import { copyFile, writeFile } from "node:fs/promises";
import net from "node:net";
import path from "node:path";
import { build, exec, exists, fail, have, node, root, skip, withTmp } from "../lib.mjs";

const wranglerToml = `name = "kekkai-e2e"
main = "test_worker.js"
compatibility_date = "2026-09-01"

[vars]
KEKKAI_OUTBOX = "log"
KEKKAI_DO_SHARD = "global"

[[d1_databases]]
binding = "DB"
database_name = "kekkai-e2e"
database_id = "local"

[[durable_objects.bindings]]
name = "TEST_DO"
class_name = "TestKekkaiObject"

[[migrations]]
tag = "v1"
new_sqlite_classes = ["TestKekkaiObject"]
`;

// Seed D1 the way a user would: the adapter's schema plus rows. The clock
// row starts at the highest version present.
const seedSql = `CREATE TABLE IF NOT EXISTS kekkai_kv (k TEXT PRIMARY KEY, v TEXT NOT NULL, ver INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS kekkai_kv_guard (conflict INTEGER CHECK (conflict = 0));
CREATE TABLE IF NOT EXISTS kekkai_kv_clock (id INTEGER PRIMARY KEY CHECK (id = 0), n INTEGER NOT NULL);
INSERT INTO kekkai_kv (k, v, ver) VALUES ('balance:alice', '100', 1), ('balance:bob', '5', 1);
INSERT OR IGNORE INTO kekkai_kv_clock (id, n) SELECT 0, COALESCE(MAX(ver), 0) FROM kekkai_kv;
`;

function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer();
    s.on("error", reject);
    s.listen(0, "127.0.0.1", () => {
      const { port } = s.address();
      s.close(() => resolve(port));
    });
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

async function bankOnWorkerd() {
  return await withTmp(async (dir) => {
    await build(path.join(root, "testdata/e2e/bank.kek"), dir);
    if (!(await exists(path.join(dir, "worker.js")))) skip("./kek build does not emit worker.js yet");
    await copyFile(path.join(root, "tests/workers/test_worker.js"), path.join(dir, "test_worker.js"));
    await writeFile(path.join(dir, "wrangler.toml"), wranglerToml);
    await writeFile(path.join(dir, "seed.sql"), seedSql);
    const env = { ...process.env, WRANGLER_SEND_METRICS: "false", CI: "1", NO_COLOR: "1" };
    const state = path.join(dir, "state");
    const seed = await exec("wrangler", ["d1", "execute", "DB", "--local", "--persist-to", state, "--file", "seed.sql", "-y"], { cwd: dir, env });
    if (seed.code !== 0) fail(`seeding D1: exit ${seed.code}\n${seed.out}`);

    const port = await freePort(), inspector = await freePort();
    let logs = "";
    const dev = spawn("wrangler", ["dev", "--local", "--persist-to", state, "--ip", "127.0.0.1", "--port", String(port),
      "--inspector-port", String(inspector), "--show-interactive-dev-session=false"],
    { cwd: dir, env, detached: true, stdio: ["ignore", "pipe", "pipe"] });
    dev.stdout.setEncoding("utf8").on("data", (d) => { logs += d; });
    dev.stderr.setEncoding("utf8").on("data", (d) => { logs += d; });
    const exited = new Promise((r) => dev.on("exit", r));
    const kill = (sig) => { try { process.kill(-dev.pid, sig); } catch { /* gone */ } };
    try {
      const base = `http://127.0.0.1:${port}`;
      const deadline = Date.now() + 90000;
      for (;;) {
        if (logs.includes("Ready on")) {
          try { await (await fetch(base + "/")).text(); break; } catch { /* not yet */ }
        }
        if (Date.now() > deadline || dev.exitCode !== null || /\[ERROR\]/.test(logs)) fail(`wrangler dev did not start:\n${logs}`);
        await sleep(200);
      }
      const r = await exec(node, [path.join(root, "tests/workers/bank.workers.mjs"), base]);
      if (r.code !== 0) fail(`${r.out}\n--- wrangler output ---\n${logs}`);
      // Outbox entries were delivered (logged) after each committed
      // transfer, and never for rolled-back or conflicting ones.
      await sleep(300);
      const errs = [];
      const entry = `kekkai outbox: POST https://hooks.example/transfer "alice->bob:30"`;
      const n = logs.split(entry).length - 1;
      if (n !== 2) errs.push(`expected the first transfer's outbox entry twice (one per backend), found ${n}`);
      if (logs.includes(`"alice->bob:5"`)) errs.push("outbox entry of a conflicting transaction was delivered");
      if (logs.includes("handler failed")) errs.push("handler failures in wrangler output");
      if (errs.length) fail(`${errs.join("\n")}\n--- wrangler output ---\n${logs}`);
      return r.out;
    } finally {
      kill("SIGTERM");
      const t = setTimeout(() => kill("SIGKILL"), 10000);
      await exited;
      clearTimeout(t);
    }
  });
}

export async function workersCases(opts) {
  if (opts.short) return { skip: "--short" };
  if (!(await have("wrangler"))) return { skip: "wrangler not found (installed by mise: `mise run e2e-workers`)" };
  return [{ name: "bank", run: bankOnWorkerd, timeout: 240000 }];
}
