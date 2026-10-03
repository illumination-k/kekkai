export default async function ({ createApp, runtime, assert, recordingLog, recordingOutbox, call, allStores }) {
  const { dbCap, TxConflict } = runtime;
  const app = createApp();

  const pay = (caps, key, qs) => {
    const headers = key === null ? {} : { "idempotency-key": key };
    return app.handle(new Request("http://test/payments?" + qs, { method: "POST", headers }), caps)
      .then(async (res) => ({ status: res.status, body: await res.text(), headers: res.headers }));
  };

  const { list, close } = await allStores();
  try {
    for (const { name, store } of list) {
      const at = (m) => `${name}: ${m}`;
      const outbox = recordingOutbox();
      const caps = { db: dbCap(store, outbox), log: recordingLog() };

      let r = await call(app, caps, "POST", "/accounts/acme/deposit?amount=100");
      assert.deepEqual([r.status, r.body], [200, '{"balance":100}'], at("deposit"));

      // first attempt commits, charges once
      r = await pay(caps, "k1", "account=acme&amount=30&merchant=shop");
      assert.equal(r.status, 201, at(r.body));
      const created = JSON.parse(r.body);
      assert.deepEqual(created, { payment: "k1", account: "acme", amount: 30, merchant: "shop", balance: 70 });
      assert.deepEqual(outbox.sent.map((e) => e.url), ["https://processor.example/charges"]);
      assert.deepEqual(JSON.parse(outbox.sent[0].body), created);

      // a retry with the same key replays the stored result
      r = await pay(caps, "k1", "account=acme&amount=30&merchant=shop");
      assert.deepEqual([r.status, r.headers.get("idempotent-replay")], [200, "true"], at("replay"));
      assert.deepEqual(JSON.parse(r.body), created);
      assert.equal(outbox.sent.length, 1, at("no second charge"));
      r = await call(app, caps, "GET", "/accounts/acme");
      assert.equal(r.body, '{"balance":70}', at("debited once"));
      r = await call(app, caps, "GET", "/payments/k1");
      assert.deepEqual(JSON.parse(r.body), created);

      // validation and business errors write nothing and send nothing
      r = await pay(caps, null, "account=acme&amount=1&merchant=shop");
      assert.equal(r.status, 428);
      r = await pay(caps, "k2", "account=acme&amount=1000&merchant=shop");
      assert.deepEqual([r.status, r.body], [402, '{"error":"insufficient funds","balance":70}']);
      r = await pay(caps, "k3", "account=nobody&amount=1&merchant=shop");
      assert.equal(r.status, 404);
      r = await pay(caps, "k4", "account=acme&amount=-5&merchant=shop");
      assert.equal(r.status, 400);
      r = await pay(caps, "bad key", "account=acme&amount=1&merchant=shop");
      assert.equal(r.status, 400);
      assert.equal(outbox.sent.length, 1);
      r = await call(app, caps, "GET", "/payments/k2");
      assert.equal(r.status, 404, at("failed payments are not recorded"));

      // The same payment submitted concurrently (a client retrying on a
      // timeout while the first request is still running): exactly one
      // debit and one charge, whatever the interleaving.
      const rs = await Promise.all(Array.from({ length: 6 }, () => pay(caps, "k5", "account=acme&amount=10&merchant=shop")));
      const statuses = rs.map((x) => x.status);
      assert.ok(statuses.every((s) => [200, 201, 503].includes(s)), at(statuses));
      assert.equal(statuses.filter((s) => s === 201).length, 1, at(`exactly one creation: ${statuses}`));
      assert.equal(outbox.sent.length, 2, at("exactly one more charge"));
      r = await call(app, caps, "GET", "/accounts/acme");
      assert.equal(r.body, '{"balance":60}');
      // retrying the 503s later converges on the stored result
      r = await pay(caps, "k5", "account=acme&amount=10&merchant=shop");
      assert.equal(r.status, 200);
      console.log(`  payments on ${name}: concurrent same-key statuses ${statuses.join(",")}`);
    }

    // A conflicting commit charges nothing; the retry then succeeds once.
    const memory = list[0].store;
    let conflictNext = true;
    const racy = {
      get: (k) => memory.get(k),
      async begin() {
        const tx = await memory.begin();
        return { ...tx, commit: async () => { if (conflictNext) { conflictNext = false; throw new TxConflict(); } return tx.commit(); } };
      },
    };
    const outbox = recordingOutbox();
    const caps = { db: dbCap(racy, outbox), log: recordingLog() };
    let r = await pay(caps, "k9", "account=acme&amount=5&merchant=shop");
    assert.deepEqual([r.status, r.headers.get("retry-after")], [503, "1"]);
    assert.equal(outbox.sent.length, 0, "outbox is discarded when the commit fails");
    r = await pay(caps, "k9", "account=acme&amount=5&merchant=shop");
    assert.equal(r.status, 201);
    assert.equal(outbox.sent.length, 1);
    r = await pay(caps, "k9", "account=acme&amount=5&merchant=shop");
    assert.equal(r.status, 200);
    assert.equal(outbox.sent.length, 1);
  } finally {
    await close();
  }
}
