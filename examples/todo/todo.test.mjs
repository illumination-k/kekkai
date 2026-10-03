export default async function ({ createApp, runtime, assert, recordingLog, call, allStores }) {
  const { dbCap, TxConflict } = runtime;
  const app = createApp();
  const { list, close } = await allStores();
  try {
    for (const { name, store } of list) {
      const log = recordingLog();
      const caps = { db: dbCap(store), log };
      const at = (m) => `${name}: ${m}`;

      let r = await call(app, caps, "GET", "/todos");
      assert.deepEqual([r.status, r.body], [200, '{"count":0}'], at("empty"));
      r = await call(app, caps, "POST", "/todos", "buy milk");
      assert.deepEqual([r.status, r.body], [201, '{"id":1}'], at("create"));
      r = await call(app, caps, "POST", "/todos", "  write docs  ");
      assert.deepEqual([r.status, r.body], [201, '{"id":2}']);
      r = await call(app, caps, "GET", "/todos/2");
      assert.deepEqual([r.status, r.body], [200, '{"id":2,"title":"write docs","done":false}']);
      assert.equal(r.headers.get("content-type"), "application/json");

      r = await call(app, caps, "POST", "/todos", 'say "hi"');
      assert.equal(r.status, 400, at("titles needing JSON escapes are rejected"));
      r = await call(app, caps, "POST", "/todos", "   ");
      assert.equal(r.status, 400);

      r = await call(app, caps, "POST", "/todos/1/done");
      assert.equal(r.status, 204);
      r = await call(app, caps, "GET", "/todos/1");
      assert.deepEqual(JSON.parse(r.body), { id: 1, title: "buy milk", done: true });
      r = await call(app, caps, "POST", "/todos/99/done");
      assert.equal(r.status, 404, at("done on a missing todo rolls back"));

      r = await call(app, caps, "DELETE", "/todos/1");
      assert.equal(r.status, 204);
      r = await call(app, caps, "GET", "/todos/1");
      assert.equal(r.status, 404);
      r = await call(app, caps, "DELETE", "/todos/1");
      assert.equal(r.status, 404);
      r = await call(app, caps, "GET", "/todos");
      assert.equal(r.body, '{"count":1}');
      r = await call(app, caps, "GET", "/todos/abc");
      assert.equal(r.status, 400);
      r = await call(app, caps, "PUT", "/todos");
      assert.equal(r.status, 405);

      // Concurrent creates race on todo:next; conflicts are retried, and
      // whatever commits gets a distinct id.
      const N = 8;
      const rs = await Promise.all(Array.from({ length: N }, (_, i) => call(app, caps, "POST", "/todos", "task " + i)));
      const created = rs.filter((x) => x.status === 201).map((x) => JSON.parse(x.body).id);
      assert.ok(rs.every((x) => x.status === 201 || x.status === 503), at(rs.map((x) => x.status)));
      assert.equal(new Set(created).size, created.length, at("ids are unique"));
      r = await call(app, caps, "GET", "/todos");
      assert.equal(JSON.parse(r.body).count, 1 + created.length, at("count matches committed creates"));
      console.log(`  todo on ${name}: ${created.length}/${N} concurrent creates committed (with up to 3 attempts)`);
    }

    // Retry behaviour, deterministically: the first two commits conflict.
    const memory = list[0].store;
    let failures = 2, commits = 0;
    const flaky = {
      get: (k) => memory.get(k),
      async begin() {
        const tx = await memory.begin();
        return { ...tx, commit: async () => { commits++; if (failures-- > 0) throw new TxConflict(); return tx.commit(); } };
      },
    };
    let r = await call(app, { db: dbCap(flaky), log: recordingLog() }, "POST", "/todos", "eventually");
    assert.equal(r.status, 201);
    assert.equal(commits, 3, "two conflicts, then success");
    failures = 5; commits = 0;
    r = await call(app, { db: dbCap(flaky), log: recordingLog() }, "POST", "/todos", "never");
    assert.deepEqual([r.status, r.headers.get("retry-after")], [503, "1"]);
    assert.equal(commits, 3, "gives up after 3 attempts");
  } finally {
    await close();
  }
}
