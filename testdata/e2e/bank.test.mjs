export default async function ({ createApp, runtime, assert, recordingLog, recordingOutbox, call }) {
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
}
