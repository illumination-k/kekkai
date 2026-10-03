export default async function ({ createApp, runtime, assert, recordingLog, recordingOutbox, call, allStores }) {
  const { MemoryStore, TxConflict, TxError, dbCap } = runtime;
  const app = createApp();

  const store = new MemoryStore({ "balance:alice": "100", "balance:bob": "5" });
  const outbox = recordingOutbox();
  const log = recordingLog();
  const caps = { db: dbCap(store, outbox), log };

  let r = await call(app, caps, "GET", "/balance/alice");
  assert.deepEqual([r.status, r.body], [200, "100"]);
  r = await call(app, caps, "GET", "/balance/carol");
  assert.equal(r.status, 404);

  // successful transfer: committed, outbox flushed after commit
  r = await call(app, caps, "POST", "/transfer?from=alice&to=bob&amount=30");
  assert.deepEqual([r.status, r.body], [200, '{"left":70}']);
  assert.equal(r.headers.get("content-type"), "application/json");
  assert.deepEqual(store.snapshot(), { "balance:alice": "70", "balance:bob": "35" });
  assert.deepEqual(outbox.sent, [{ url: "https://hooks.example/transfer", body: "alice->bob:30" }]);
  assert.deepEqual(log.lines, [["info", "transfer alice -> bob"]]);

  // explicit rollback: nothing written, outbox not flushed
  r = await call(app, caps, "POST", "/transfer?from=alice&to=bob&amount=1000");
  assert.deepEqual([r.status, r.body], [409, "insufficient funds"]);
  assert.deepEqual(store.snapshot(), { "balance:alice": "70", "balance:bob": "35" });
  assert.equal(outbox.sent.length, 1);

  r = await call(app, caps, "POST", "/transfer?from=nobody&to=bob&amount=1");
  assert.deepEqual([r.status, r.body], [404, "no account nobody"]);
  r = await call(app, caps, "POST", "/transfer?from=alice&to=bob&amount=abc");
  assert.deepEqual([r.status, r.body], [400, "bad amount"]);
  r = await call(app, caps, "GET", "/transfer?from=alice&to=bob&amount=1");
  assert.equal(r.status, 404);

  // optimistic-concurrency conflict at commit: retryable error, nothing applied
  const conflicting = {
    get: (k) => store.get(k),
    async begin() {
      const tx = await store.begin();
      return { ...tx, commit: async () => { throw new TxConflict(); } };
    },
  };
  const outbox2 = recordingOutbox();
  r = await call(app, { db: dbCap(conflicting, outbox2), log }, "POST", "/transfer?from=alice&to=bob&amount=1");
  assert.deepEqual([r.status, r.body], [503, "conflict, retry"]);
  assert.deepEqual(store.snapshot(), { "balance:alice": "70", "balance:bob": "35" });
  assert.equal(outbox2.sent.length, 0);

  // a store error in the middle of the body (`?`): automatic rollback
  let rolledBack = 0;
  const failing = {
    get: (k) => store.get(k),
    async begin() {
      const tx = await store.begin();
      let puts = 0;
      return {
        ...tx,
        put: async (k, v) => { if (++puts === 2) throw new TxError("disk full"); return tx.put(k, v); },
        rollback: async () => { rolledBack++; return tx.rollback(); },
      };
    },
  };
  r = await call(app, { db: dbCap(failing, outbox2), log }, "POST", "/transfer?from=alice&to=bob&amount=1");
  assert.deepEqual([r.status, r.body], [500, "disk full"]);
  assert.equal(rolledBack, 1);
  assert.deepEqual(store.snapshot(), { "balance:alice": "70", "balance:bob": "35" });
  assert.equal(outbox2.sent.length, 0);

  // a real race between two transactions on the in-memory OCC store
  const t1 = await store.begin();
  const t2 = await store.begin();
  await t1.get("balance:bob"); await t2.get("balance:bob");
  await t1.put("balance:bob", "1"); await t2.put("balance:bob", "2");
  await t1.commit();
  await assert.rejects(t2.commit(), (e) => e.retryable === true);

  // The same program against every store adapter (D1 on SQLite,
  // Durable Object storage, remote OCC gateway): sequential transfers,
  // then concurrent transfers that race on the same balances.
  const { list, close } = await allStores({ "balance:alice": "100", "balance:bob": "5" });
  try {
    for (const { name, store } of list) {
      const ob = recordingOutbox();
      const c = { db: dbCap(store, ob), log: recordingLog() };
      r = await call(app, c, "POST", "/transfer?from=alice&to=bob&amount=30");
      assert.deepEqual([name, r.status, r.body], [name, 200, '{"left":70}']);
      r = await call(app, c, "POST", "/transfer?from=bob&to=alice&amount=1000");
      assert.deepEqual([name, r.status], [name, 409]);
      const N = 10;
      const results = await Promise.all(Array.from({ length: N }, (_, i) =>
        call(app, c, "POST", i % 2 ? "/transfer?from=alice&to=bob&amount=1" : "/transfer?from=bob&to=alice&amount=2")));
      const statuses = results.map((x) => x.status);
      assert.ok(statuses.every((s) => s === 200 || s === 503), `${name}: ${statuses}`);
      let alice = 70, bob = 35;
      results.forEach((x, i) => {
        if (x.status !== 200) return;
        if (i % 2) { alice -= 1; bob += 1; } else { bob -= 2; alice += 2; }
      });
      const a = (await call(app, c, "GET", "/balance/alice")).body;
      const b = (await call(app, c, "GET", "/balance/bob")).body;
      assert.deepEqual([name, a, b], [name, String(alice), String(bob)], "every committed transfer applied exactly once");
      assert.equal(ob.sent.length, 1 + statuses.filter((s) => s === 200).length, `${name}: one outbox entry per commit`);
      console.log(`  bank on ${name}: concurrent statuses ${statuses.join(",")}`);
    }
  } finally {
    await close();
  }
}
