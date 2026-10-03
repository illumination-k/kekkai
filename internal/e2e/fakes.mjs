// Test doubles for the storage backends the runtime adapters target:
//
//   FakeD1               a D1Database on node:sqlite (prepare/bind/first/run/
//                        all/raw/batch/exec), with real atomic batches
//   FakeDurableStorage   Durable Object storage (get/put/delete/transaction)
//   startRemoteKv()      reference HTTP gateway for RemoteKvStore: an MVCC
//                        store with snapshot reads and validated commits
//
// Every asynchronous operation yields to the event loop first, so
// concurrently running transactions really interleave.
import { DatabaseSync } from "node:sqlite";
import http from "node:http";

const yieldNow = () => new Promise((r) => setImmediate(r));

// ---------------------------------------------------------------------------
// D1

function d1Error(e) {
  const err = new Error(`D1_ERROR: ${e.message}: ${e.errcode === 275 || /constraint/i.test(e.message) ? "SQLITE_CONSTRAINT" : "SQLITE_ERROR"}`);
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
  returnsRows() {
    return /^\s*(SELECT|WITH|PRAGMA)\b/i.test(this.sql) || /\bRETURNING\b/i.test(this.sql);
  }
  // Synchronous execution; used directly inside batches.
  exec() {
    const st = this.d1.db.prepare(this.sql);
    if (this.returnsRows()) {
      const results = st.all(...this.params);
      return { results, success: true, meta: { changes: 0, rows_read: results.length } };
    }
    const r = st.run(...this.params);
    return { results: [], success: true, meta: { changes: Number(r.changes), last_row_id: Number(r.lastInsertRowid) } };
  }
  async run() {
    await this.d1.enter("run", [this]);
    try { return this.exec(); } catch (e) { throw d1Error(e); }
  }
  async all() { return this.run(); }
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
  constructor(path = ":memory:") {
    this.db = new DatabaseSync(path);
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
    this.db.exec("BEGIN IMMEDIATE");
    try {
      const out = stmts.map((s) => s.exec());
      this.db.exec("COMMIT");
      return out;
    } catch (e) {
      this.db.exec("ROLLBACK");
      throw d1Error(e);
    }
  }
  async exec(sql) {
    await yieldNow();
    this.db.exec(sql);
    return { count: sql.split(";").filter((s) => s.trim()).length, duration: 0 };
  }
  /** Synchronous helpers for assertions. */
  rows(sql, ...params) {
    return this.db.prepare(sql).all(...params);
  }
}

// ---------------------------------------------------------------------------
// Durable Object storage

export class FakeDurableStorage {
  constructor() {
    this.data = new Map();
    this.queue = Promise.resolve();
    this.transactions = 0;
  }
  async get(key) {
    await yieldNow();
    return this.data.get(key);
  }
  async put(key, value) {
    await yieldNow();
    this.data.set(key, value);
  }
  async delete(key) {
    await yieldNow();
    return this.data.delete(key);
  }
  /** Closure transactions run one at a time and apply atomically. */
  transaction(fn) {
    const run = this.queue.then(async () => {
      this.transactions++;
      const writes = new Map(); // key -> value | undefined (delete)
      const txn = {
        get: async (k) => (writes.has(k) ? writes.get(k) : this.data.get(k)),
        put: async (k, v) => { writes.set(k, v); },
        delete: async (k) => { const had = writes.has(k) ? writes.get(k) !== undefined : this.data.has(k); writes.set(k, undefined); return had; },
        rollback: () => { throw new Error("rolled back"); },
      };
      const result = await fn(txn);
      for (const [k, v] of writes) {
        if (v === undefined) this.data.delete(k);
        else this.data.set(k, v);
      }
      return result;
    });
    this.queue = run.catch(() => {});
    return run;
  }
}

// ---------------------------------------------------------------------------
// Remote optimistic KV gateway (reference implementation of the protocol
// documented on RemoteKvStore).

export async function startRemoteKv() {
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
  const server = http.createServer((req, res) => {
    let body = "";
    req.on("data", (c) => (body += c));
    req.on("end", () => {
      let status, out;
      if (state.failNext) {
        status = state.failNext; out = { error: "injected" }; state.failNext = null;
      } else if (req.method !== "POST" || !handlers[req.url]) {
        status = 404; out = { error: "not found" };
      } else {
        [status, out] = handlers[req.url](JSON.parse(body || "{}"));
      }
      res.writeHead(status, { "content-type": "application/json" });
      res.end(JSON.stringify(out));
    });
  });
  await new Promise((r) => server.listen(0, "127.0.0.1", r));
  const { port } = server.address();
  return {
    url: `http://127.0.0.1:${port}`,
    state,
    snapshot: () => Object.fromEntries([...history.keys()].map((k) => [k, latest(k).value]).filter(([, v]) => v !== null)),
    close: () => new Promise((r) => { server.closeAllConnections?.(); server.close(r); }),
  };
}

// ---------------------------------------------------------------------------
// One instance of every adapter, seeded with the same data.

export async function allStores(runtime, initial = {}) {
  const { MemoryStore, D1KvStore, DurableObjectStore, RemoteKvStore } = runtime;
  const remote = await startRemoteKv();
  const d1 = new FakeD1();
  const doStorage = new FakeDurableStorage();
  const list = [
    { name: "memory", store: new MemoryStore() },
    { name: "d1", store: new D1KvStore(d1), d1 },
    { name: "durable-object", store: new DurableObjectStore(doStorage, { prefix: "DB:" }), storage: doStorage },
    { name: "remote", store: new RemoteKvStore(remote.url), remote },
  ];
  for (const s of list) {
    if (Object.keys(initial).length === 0) continue;
    const tx = await s.store.begin();
    for (const [k, v] of Object.entries(initial)) await tx.put(k, v);
    await tx.commit();
  }
  return { list, close: () => remote.close() };
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
