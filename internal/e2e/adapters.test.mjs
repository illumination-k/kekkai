// Store adapter conformance: node adapters.test.mjs
//
// Every adapter must give serializable transactions with commit-time
// conflict detection (TxConflict, retryable) and atomic commits. The same
// suite runs against MemoryStore, D1KvStore (on a node:sqlite fake D1),
// DurableObjectStore (on a fake DO storage) and RemoteKvStore (against
// the reference HTTP gateway).
import assert from "node:assert/strict";
import * as runtime from "../../js/kekkai_runtime.js";
import { allStores, FakeD1, snapshotOf } from "./fakes.mjs";

const { D1KvStore, TxConflict } = runtime;
const isConflict = (e) => e instanceof TxConflict && e.retryable === true;

async function commitOrConflict(tx) {
  try { await tx.commit(); return "ok"; } catch (e) { if (isConflict(e)) return "conflict"; throw e; }
}

const suite = {
  async "commit, rollback and non-transactional reads"(s) {
    let tx = await s.begin();
    assert.equal(await tx.get("a"), null);
    await tx.put("a", "1");
    assert.equal(await tx.get("a"), "1", "read your own writes");
    assert.equal(await s.get("a"), null, "uncommitted writes are invisible");
    await tx.commit();
    assert.equal(await s.get("a"), "1");

    tx = await s.begin();
    await tx.put("a", "2");
    await tx.delete("b");
    await tx.rollback();
    assert.equal(await s.get("a"), "1");

    tx = await s.begin();
    await tx.delete("a");
    assert.equal(await tx.get("a"), null);
    await tx.commit();
    assert.equal(await s.get("a"), null);
  },

  async "lost update is detected"(s) {
    let tx = await s.begin(); await tx.put("n", "0"); await tx.commit();
    const t1 = await s.begin(), t2 = await s.begin();
    const a = Number(await t1.get("n")), b = Number(await t2.get("n"));
    await t1.put("n", String(a + 1)); await t2.put("n", String(b + 1));
    assert.equal(await commitOrConflict(t1), "ok");
    assert.equal(await commitOrConflict(t2), "conflict");
    assert.equal(await s.get("n"), "1");
  },

  async "write skew is detected (serializable, not snapshot isolation)"(s) {
    let tx = await s.begin(); await tx.put("x", "1"); await tx.put("y", "1"); await tx.commit();
    const t1 = await s.begin(), t2 = await s.begin();
    await t1.get("x"); await t1.get("y"); await t1.put("x", "0");
    await t2.get("x"); await t2.get("y"); await t2.put("y", "0");
    assert.equal(await commitOrConflict(t1), "ok");
    assert.equal(await commitOrConflict(t2), "conflict");
    assert.deepEqual(await snapshotOf(s, ["x", "y"]), { x: "0", y: "1" });
  },

  async "phantom insert of an absent key is detected"(s) {
    const t1 = await s.begin(), t2 = await s.begin();
    assert.equal(await t1.get("user:1"), null);
    assert.equal(await t2.get("user:1"), null);
    await t1.put("user:1", "alice"); await t2.put("user:1", "bob");
    assert.equal(await commitOrConflict(t1), "ok");
    assert.equal(await commitOrConflict(t2), "conflict");
    assert.equal(await s.get("user:1"), "alice");
  },

  async "delete + re-create does not hide a change (no ABA)"(s) {
    let tx = await s.begin(); await tx.put("k", "100"); await tx.commit();
    const reader = await s.begin();
    assert.equal(await reader.get("k"), "100");
    tx = await s.begin(); await tx.delete("k"); await tx.commit();
    tx = await s.begin(); await tx.put("k", "5"); await tx.commit();
    await reader.put("copy", "100");
    assert.equal(await commitOrConflict(reader), "conflict");
    assert.equal(await s.get("copy"), null);
  },

  async "read-only transactions validate their reads"(s) {
    let tx = await s.begin(); await tx.put("r1", "a"); await tx.put("r2", "a"); await tx.commit();
    const ro = await s.begin();
    await ro.get("r1");
    tx = await s.begin(); await tx.get("r1"); await tx.put("r1", "b"); await tx.put("r2", "b"); await tx.commit();
    await ro.get("r2");
    assert.equal(await commitOrConflict(ro), "conflict", "r1 and r2 were read from different states");
  },

  async "blind writes do not conflict; last committer wins"(s) {
    const t1 = await s.begin(), t2 = await s.begin();
    await t1.put("blind", "1"); await t2.put("blind", "2");
    assert.equal(await commitOrConflict(t1), "ok");
    assert.equal(await commitOrConflict(t2), "ok");
    assert.equal(await s.get("blind"), "2");
  },

  async "a failed commit applies nothing (atomicity)"(s) {
    let tx = await s.begin(); await tx.put("p", "0"); await tx.put("q", "0"); await tx.commit();
    const t1 = await s.begin();
    await t1.get("p"); await t1.put("p", "1"); await t1.put("q", "1");
    tx = await s.begin(); await tx.put("p", "9"); await tx.commit(); // blind write bumps p
    assert.equal(await commitOrConflict(t1), "conflict");
    assert.deepEqual(await snapshotOf(s, ["p", "q"]), { p: "9", q: "0" });
  },

  async "concurrent increments with retry are all applied"(s) {
    let tx = await s.begin(); await tx.put("ctr", "0"); await tx.commit();
    const N = 12;
    let conflicts = 0;
    await Promise.all(Array.from({ length: N }, async () => {
      for (;;) {
        const t = await s.begin();
        const v = Number(await t.get("ctr"));
        await t.put("ctr", String(v + 1));
        if ((await commitOrConflict(t)) === "ok") return;
        conflicts++;
      }
    }));
    assert.equal(await s.get("ctr"), String(N));
    return { conflicts };
  },
};

const { list, close } = await allStores(runtime);
try {
  for (const { name, store } of list) {
    for (const [title, test] of Object.entries(suite)) {
      try {
        const info = await test(store);
        if (info) console.log(`  ${name}: ${title} ${JSON.stringify(info)}`);
      } catch (e) {
        e.message = `[${name}] ${title}: ${e.message}`;
        throw e;
      }
    }
  }
  // --- D1 specifics -------------------------------------------------------
  {
    // Rows written before the version clock existed (older runtimes, manual
    // seeding): the clock starts above them, so versions are never reused.
    const d1 = new FakeD1();
    d1.db.exec("CREATE TABLE kekkai_kv (k TEXT PRIMARY KEY, v TEXT NOT NULL, ver INTEGER NOT NULL)");
    d1.db.exec("INSERT INTO kekkai_kv VALUES ('a', 'old', 7), ('b', 'x', 1)");
    const s = new D1KvStore(d1);
    const reader = await s.begin();
    assert.equal(await reader.get("a"), "old");
    let tx = await s.begin(); await tx.delete("a"); await tx.commit();
    for (let i = 0; i < 7; i++) { tx = await s.begin(); await tx.put("a", "new" + i); await tx.commit(); }
    await reader.put("c", "1");
    assert.equal(await commitOrConflict(reader), "conflict");
    assert.ok(d1.rows("SELECT ver FROM kekkai_kv WHERE k = 'a'")[0].ver > 7);

    // Two adapter instances on one database (two isolates) agree.
    const s2 = new D1KvStore(d1);
    const t1 = await s.begin(), t2 = await s2.begin();
    await t1.get("b"); await t2.get("b");
    await t1.put("b", "1"); await t2.put("b", "2");
    assert.equal(await commitOrConflict(t2), "ok");
    assert.equal(await commitOrConflict(t1), "conflict");

    // A commit is exactly one batch; guard table stays empty.
    const before = d1.calls.batch;
    tx = await s.begin(); await tx.get("b"); await tx.put("b", "3"); await tx.put("z", "1"); await tx.commit();
    assert.equal(d1.calls.batch - before, 1);
    assert.deepEqual(d1.rows("SELECT * FROM kekkai_kv_guard"), []);

    // A concurrent writer injected between the reads and the commit batch.
    tx = await s.begin();
    const seen = await tx.get("b");
    d1.before = async (kind) => {
      if (kind !== "batch") return;
      d1.before = null;
      const w = await s2.begin(); await w.put("b", "from-elsewhere"); await w.commit();
    };
    await tx.put("b", seen + "!");
    assert.equal(await commitOrConflict(tx), "conflict");
    assert.equal(await s.get("b"), "from-elsewhere");

    // Non-conflict errors are not retryable.
    const broken = new D1KvStore({ prepare: () => ({ bind() { return this; }, first: async () => null }), batch: async () => { throw new Error("D1_ERROR: no such table: nope"); } });
    await assert.rejects(broken.get("x"), /no such table/);
    // init is retried after a failure
    let fail = true;
    const flaky = new FakeD1();
    const realBatch = flaky.batch.bind(flaky);
    flaky.batch = async (st) => { if (fail) { fail = false; throw new Error("D1_ERROR: network"); } return realBatch(st); };
    const fs = new D1KvStore(flaky);
    await assert.rejects(fs.get("x"), /network/);
    assert.equal(await fs.get("x"), null);
  }

  // --- Remote specifics -----------------------------------------------------
  {
    const { store, remote } = list.find((s) => s.name === "remote");
    remote.state.failNext = 503;
    const tx = await store.begin().catch((e) => e);
    assert.equal(tx.retryable, true, "503 is retryable");
    remote.state.failNext = 500;
    await assert.rejects(store.get("x"), (e) => e.retryable === false);
  }
} finally {
  await close();
}
console.log("ok");
