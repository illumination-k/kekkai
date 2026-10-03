// Main module of the "worker" e2e service in workerd (generated config:
// tests/suites/e2e.sh): it serves a program built with `./kek build
// -target do` through its generated worker.js, unchanged, and adds test
// hooks around it:
//
//   x-kekkai-backend: do      route the request into the Durable Object
//                             (binds TEST_DO as KEKKAI_DO); default is D1
//   x-test-conflict: <key>    simulate a concurrent writer: just before the
//                             transaction's commit reaches storage, another
//                             transaction adds 1000 to <key> and commits
//   POST /__test/seed         body {key: value}: seed the backend (D1: raw
//                             SQL creating the adapter's tables by hand;
//                             Durable Object: a transaction)
//
// The D1 binding DB is a FakeD1 (D1 API over a FakeD1Object Durable Object
// with SQLite storage), since workerd alone has no D1 service; the Durable
// Object backend uses real Durable Object storage.
import generated, { KekkaiObject } from "./worker.js";
import { D1KvStore, DurableObjectStore } from "./kekkai_runtime.js";
import { FakeD1 } from "./fakes.js";

export { FakeD1Object } from "./fakes.js";

async function addThousand(store, key) {
  const tx = await store.begin();
  const v = Number((await tx.get(key)) ?? "0");
  await tx.put(key, String(v + 1000));
  await tx.commit();
}

// All requests share one database (the FakeD1Object named "DB"). The
// binding object is created per request because a Durable Object stub
// belongs to the request that created it; worker.js then sets up its
// D1KvStore (an idempotent schema batch) once per request.
function d1Binding(env) {
  return new FakeD1(env.FAKE_D1.get(env.FAKE_D1.idFromName("DB")));
}

// A D1 binding whose second batch (the first is the adapter's schema
// setup) is preceded by a committed concurrent write to `key`.
function conflictingD1(db, key) {
  let batches = 0;
  return {
    prepare: (sql) => db.prepare(sql),
    exec: (sql) => db.exec(sql),
    async batch(stmts) {
      if (++batches === 2) await addThousand(new D1KvStore(db), key);
      return db.batch(stmts);
    },
  };
}

// The adapter's schema plus rows, written by hand the way a user would
// seed D1. The clock row starts at the highest version present.
function seedSql(rows) {
  const q = (s) => "'" + String(s).replaceAll("'", "''") + "'";
  const values = Object.entries(rows).map(([k, v]) => `(${q(k)}, ${q(v)}, 1)`).join(", ");
  return `CREATE TABLE IF NOT EXISTS kekkai_kv (k TEXT PRIMARY KEY, v TEXT NOT NULL, ver INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS kekkai_kv_guard (conflict INTEGER CHECK (conflict = 0));
CREATE TABLE IF NOT EXISTS kekkai_kv_clock (id INTEGER PRIMARY KEY CHECK (id = 0), n INTEGER NOT NULL);
INSERT INTO kekkai_kv (k, v, ver) VALUES ${values};
INSERT OR IGNORE INTO kekkai_kv_clock (id, n) SELECT 0, COALESCE(MAX(ver), 0) FROM kekkai_kv;`;
}

export default {
  async fetch(request, env, ctx) {
    const useDO = request.headers.get("x-kekkai-backend") === "do";
    const conflict = request.headers.get("x-test-conflict");
    const e = { ...env, KEKKAI_DO: useDO ? env.TEST_DO : undefined, DB: d1Binding(env) };
    if (!useDO && conflict) e.DB = conflictingD1(e.DB, conflict);
    if (!useDO && new URL(request.url).pathname === "/__test/seed" && request.method === "POST") {
      await e.DB.exec(seedSql(await request.json()));
      return new Response("seeded");
    }
    return generated.fetch(request, e, ctx);
  },
};

export class TestKekkaiObject extends KekkaiObject {
  constructor(state, env) {
    super(state, env);
    this.conflictKey = null;
    const real = state.storage;
    const self = this;
    // Storage whose next transaction() is preceded by a concurrent write.
    this.storage = {
      get: (k) => real.get(k),
      put: (k, v) => real.put(k, v),
      delete: (k) => real.delete(k),
      async transaction(fn) {
        const key = self.conflictKey;
        self.conflictKey = null;
        if (key) await addThousand(new DurableObjectStore(real, { prefix: "DB:" }), key);
        return real.transaction(fn);
      },
    };
  }
  store(binding) {
    let s = this.stores.get(binding);
    if (!s) this.stores.set(binding, (s = new DurableObjectStore(this.storage, { prefix: binding + ":" })));
    return s;
  }
  async fetch(request) {
    const url = new URL(request.url);
    if (url.pathname === "/__test/seed" && request.method === "POST") {
      const tx = await this.store("DB").begin();
      for (const [k, v] of Object.entries(await request.json())) await tx.put(k, v);
      await tx.commit();
      return new Response("seeded");
    }
    this.conflictKey = request.headers.get("x-test-conflict");
    return super.fetch(request);
  }
}
