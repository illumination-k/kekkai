// Test entry point for running a compiled program on workerd (wrangler dev
// --local). It wraps the generated worker.js without changing it:
//
//   x-kekkai-backend: do      route the request into the Durable Object
//                             (binds TEST_DO as KEKKAI_DO); default is D1
//   x-test-conflict: <key>    simulate a concurrent writer: just before the
//                             transaction's commit reaches storage, another
//                             transaction adds 1000 to <key> and commits
//   POST /__test/seed         body {key: value}: write through a transaction
//                             (Durable Object backend only; D1 is seeded with
//                             `wrangler d1 execute`)
import generated, { KekkaiObject } from "./worker.js";
import { D1KvStore, DurableObjectStore } from "./kekkai_runtime.js";

async function addThousand(store, key) {
  const tx = await store.begin();
  const v = Number((await tx.get(key)) ?? "0");
  await tx.put(key, String(v + 1000));
  await tx.commit();
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

export default {
  async fetch(request, env, ctx) {
    const useDO = request.headers.get("x-kekkai-backend") === "do";
    const conflict = request.headers.get("x-test-conflict");
    const e = { ...env, KEKKAI_DO: useDO ? env.TEST_DO : undefined };
    if (!useDO && conflict) e.DB = conflictingD1(env.DB, conflict);
    if (!useDO && new URL(request.url).pathname === "/__test/seed") return new Response("seed D1 with wrangler d1 execute", { status: 400 });
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
