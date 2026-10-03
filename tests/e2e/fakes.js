// Test doubles for the storage backends the runtime adapters target. The
// module runs inside workerd (tests/suites/e2e.sh embeds it in every e2e
// service):
//
//   FakeD1Object   a Durable Object class holding one SQLite database
//                  (ctx.storage.sql); the backing store of a FakeD1
//   FakeD1         a D1Database (prepare/bind/first/run/all/raw/batch/exec)
//                  talking to one FakeD1Object over RPC. A batch runs in
//                  one transactionSync(), so it is atomic and CHECK
//                  constraints abort it exactly as in D1's SQLite
//   startRemoteKv  in-process reference gateway for RemoteKvStore (an MVCC
//                  store with snapshot reads and validated commits), handed
//                  to the adapter as its `fetch` function
//   makeFakes      per-run factory: fresh FakeD1 databases, and allStores()
//                  with one seeded instance of every adapter, where
//                  DurableObjectStore runs on the real storage of the Durable
//                  Object executing the test
//
// Every asynchronous fake operation yields to the event loop first, so
// concurrently running transactions really interleave.
import { DurableObject } from "cloudflare:workers";

const yieldNow = () => new Promise((r) => setTimeout(r, 0));

// ---------------------------------------------------------------------------
// D1

export class FakeD1Object extends DurableObject {
  run1({ sql, params }) {
    const db = this.ctx.storage.sql;
    const cur = db.exec(sql, ...params);
    const results = cur.toArray();
    const meta = { changes: cur.rowsWritten, rows_read: cur.rowsRead };
    meta.last_row_id = db.exec("SELECT last_insert_rowid() AS id").one().id;
    return { results, success: true, meta };
  }
  query(statement) {
    return this.run1(statement);
  }
  /** All statements in one SQLite transaction; any error rolls back all. */
  batch(statements) {
    return this.ctx.storage.transactionSync(() => statements.map((s) => this.run1(s)));
  }
  script(sql) {
    this.ctx.storage.sql.exec(sql);
    return { count: sql.split(";").filter((s) => s.trim()).length, duration: 0 };
  }
}

function d1Error(e) {
  const msg = String(e && e.message ? e.message : e);
  const err = new Error(msg.startsWith("D1_") ? msg : `D1_ERROR: ${msg}`);
  err.cause = e;
  return err;
}

class FakeStatement {
  constructor(d1, sql, params = []) {
    this.d1 = d1;
    this.sql = sql;
    this.params = params;
  }
  bind(...params) {
    for (const p of params) {
      if (p === undefined) throw new Error("D1_TYPE_ERROR: Type 'undefined' not supported for value 'undefined'");
    }
    return new FakeStatement(this.d1, this.sql, params);
  }
  wire() {
    return { sql: this.sql, params: this.params };
  }
  async run() {
    await this.d1.enter("run", [this]);
    try {
      return await this.d1.stub.query(this.wire());
    } catch (e) {
      throw d1Error(e);
    }
  }
  async all() {
    return this.run();
  }
  async first(column) {
    const { results } = await this.run();
    const row = results.length ? { ...results[0] } : null;
    if (column === undefined) return row;
    return row ? row[column] ?? null : null;
  }
  async raw() {
    const { results } = await this.run();
    return results.map((r) => Object.values(r));
  }
}

export class FakeD1 {
  /** stub: a FakeD1Object stub; each object is a separate database. */
  constructor(stub) {
    this.stub = stub;
    this.calls = { run: 0, batch: 0 };
    // Optional async hook run before each operation (after yielding):
    // (kind, statements) => Promise<void>. Lets a test inject a concurrent
    // writer at an exact point.
    this.before = null;
  }
  async enter(kind, stmts) {
    this.calls[kind]++;
    await yieldNow();
    if (this.before) await this.before(kind, stmts);
  }
  prepare(sql) {
    return new FakeStatement(this, sql);
  }
  /** Atomic like D1: all statements in one SQLite transaction. */
  async batch(stmts) {
    await this.enter("batch", stmts);
    try {
      return await this.stub.batch(stmts.map((s) => s.wire()));
    } catch (e) {
      throw d1Error(e);
    }
  }
  async exec(sql) {
    await yieldNow();
    try {
      return await this.stub.script(sql);
    } catch (e) {
      throw d1Error(e);
    }
  }
  /** Rows of a query, for assertions (not counted as a call). */
  async rows(sql, ...params) {
    return (await this.stub.query({ sql, params })).results;
  }
}

// ---------------------------------------------------------------------------
// Remote optimistic KV gateway (reference implementation of the protocol
// documented on RemoteKvStore), served in process through a fetch function.

export function startRemoteKv() {
  let clock = 0;
  const history = new Map(); // key -> [{ ver, value|null }] ascending
  const latest = (k) => { const h = history.get(k); return h ? h[h.length - 1] : { ver: 0, value: null }; };
  const at = (k, rv) => {
    const h = history.get(k) || [];
    for (let i = h.length - 1; i >= 0; i--) if (h[i].ver <= rv) return h[i];
    return { ver: 0, value: null };
  };
  const state = { failNext: null, commits: 0, conflicts: 0 };
  const handlers = {
    "/begin": () => [200, { readVersion: clock }],
    "/get": ({ key, readVersion }) => {
      const e = at(key, readVersion);
      return [200, { value: e.value, version: e.ver }];
    },
    "/commit": ({ reads, writes }) => {
      for (const { key, version } of reads) {
        if (latest(key).ver !== version) { state.conflicts++; return [409, { error: "conflict", key }]; }
      }
      if (writes.length) {
        const ver = ++clock;
        for (const { key, value } of writes) {
          if (!history.has(key)) history.set(key, []);
          history.get(key).push({ ver, value });
        }
      }
      state.commits++;
      return [200, { ok: true }];
    },
  };
  const base = "http://remote-kv.test";
  async function fetchFn(input, init = {}) {
    const req = new Request(input, init);
    const body = await req.text();
    await yieldNow();
    const { pathname } = new URL(req.url);
    let status, out;
    if (state.failNext) {
      status = state.failNext; out = { error: "injected" }; state.failNext = null;
    } else if (req.method !== "POST" || !handlers[pathname]) {
      status = 404; out = { error: "not found" };
    } else {
      [status, out] = handlers[pathname](JSON.parse(body || "{}"));
    }
    return new Response(JSON.stringify(out), { status, headers: { "content-type": "application/json" } });
  }
  return {
    url: base,
    fetch: fetchFn,
    state,
    snapshot: () => Object.fromEntries([...history.keys()].map((k) => [k, latest(k).value]).filter(([, v]) => v !== null)),
  };
}

// ---------------------------------------------------------------------------
// Per-run factory.

/**
 * env.FAKE_D1: the FakeD1Object namespace; storage: the Durable Object
 * storage DurableObjectStore instances run on (each allStores() call gets
 * its own key prefix, so every call starts empty).
 */
export function makeFakes(env, storage) {
  let stores = 0;
  const newD1 = () => new FakeD1(env.FAKE_D1.get(env.FAKE_D1.newUniqueId()));

  async function allStores(runtime, initial = {}) {
    const { MemoryStore, D1KvStore, DurableObjectStore, RemoteKvStore } = runtime;
    const remote = startRemoteKv();
    const d1 = newD1();
    const list = [
      { name: "memory", store: new MemoryStore() },
      { name: "d1", store: new D1KvStore(d1), d1 },
      { name: "durable-object", store: new DurableObjectStore(storage, { prefix: `DB${++stores}:` }), storage },
      { name: "remote", store: new RemoteKvStore(remote.url, { fetch: remote.fetch }), remote },
    ];
    for (const s of list) {
      if (Object.keys(initial).length === 0) continue;
      const tx = await s.store.begin();
      for (const [k, v] of Object.entries(initial)) await tx.put(k, v);
      await tx.commit();
    }
    return { list, close: async () => {} };
  }

  return { newD1, startRemoteKv, allStores, snapshotOf };
}

/** Read keys through the store's non-transactional get. */
export async function snapshotOf(store, keys) {
  const out = {};
  for (const k of keys) {
    const v = await store.get(k);
    if (v !== null) out[k] = v;
  }
  return out;
}
