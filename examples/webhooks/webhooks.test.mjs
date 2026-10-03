export default async function ({ createApp, runtime, assert, recordingLog, recordingOutbox, call, fakeNet, allStores }) {
  const { dbCap, TxConflict, TxError } = runtime;
  const app = createApp();
  const challenge = (u) => (u.endsWith("?challenge=kekkai") && !u.includes("liar") ? "kekkai\n" : "nope");

  const { list, close } = await allStores();
  try {
    for (const { name, store } of list) {
      const at = (m) => `${name}: ${m}`;
      const outbox = recordingOutbox();
      const net = fakeNet(challenge);
      const caps = { db: dbCap(store, outbox), net, log: recordingLog() };

      // no subscribers yet: the event is stored, nothing to deliver
      let r = await call(app, caps, "POST", "/events", "warmup");
      assert.deepEqual([r.status, r.body], [202, '{"event":1,"deliveries":0}'], at("publish"));

      r = await call(app, caps, "POST", "/subscribers", "https://a.example/hook");
      assert.deepEqual([r.status, r.body], [201, '{"id":0}'], at("subscribe"));
      assert.deepEqual(net.calls, [{ method: "GET", url: "https://a.example/hook?challenge=kekkai", body: undefined }]);
      r = await call(app, caps, "POST", "/subscribers", "https://b.example/hook");
      assert.equal(r.status, 201);
      r = await call(app, caps, "POST", "/subscribers", "http://insecure.example");
      assert.equal(r.status, 400);
      r = await call(app, caps, "POST", "/subscribers", "https://liar.example/hook");
      assert.deepEqual([r.status, r.body], [422, "subscriber not verified: wrong challenge answer"]);
      // the subscriber is unreachable: the Net error is reported, nothing stored
      const down = fakeNet({});
      r = await call(app, { ...caps, net: down }, "POST", "/subscribers", "https://down.example/hook");
      assert.equal(r.status, 422);
      assert.equal(outbox.sent.length, 0, at("subscribing sends nothing"));

      // fan-out happens after the commit, once per subscriber
      r = await call(app, caps, "POST", "/events", "order-42-shipped");
      assert.deepEqual([r.status, r.body], [202, '{"event":2,"deliveries":2}'], at("fan-out"));
      const envelope = '{"event":2,"payload":"order-42-shipped"}';
      assert.deepEqual(outbox.sent, [
        { url: "https://a.example/hook", body: envelope },
        { url: "https://b.example/hook", body: envelope },
      ]);
      r = await call(app, caps, "GET", "/events/2");
      assert.deepEqual([r.status, r.body], [200, "order-42-shipped"]);
      r = await call(app, caps, "POST", "/events", 'has "quotes"');
      assert.equal(r.status, 400);
      console.log(`  webhooks on ${name}: ${outbox.sent.length} deliveries queued after commit`);
    }

    // A failed commit delivers nothing: no subscriber hears about an event
    // that was rolled back.
    for (const failure of [new TxConflict(), new TxError("disk full")]) {
      const memory = list[0].store;
      const failing = {
        get: (k) => memory.get(k),
        async begin() {
          const tx = await memory.begin();
          return { ...tx, commit: async () => { throw failure; } };
        },
      };
      const outbox = recordingOutbox();
      const r = await call(app, { db: dbCap(failing, outbox), net: fakeNet({}), log: recordingLog() }, "POST", "/events", "ghost");
      assert.equal(r.status, failure.retryable ? 503 : 500);
      assert.equal(outbox.sent.length, 0, "no deliveries for an uncommitted event");
      assert.equal(await memory.get("event:3"), null);
    }
  } finally {
    await close();
  }
}
