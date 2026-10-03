// Kekkai runtime for Cloudflare Workers (and Node for tests).
//
// The compiled WasmGC module imports host operations from the "kek"
// module. Capabilities are plain JS objects passed into the handler; the
// wasm code can only reach the outside world through them.
//
// Functions that perform I/O are compiled into resumable state machines:
// handler_step() returns 1 when the computation is suspended on a pending
// host promise. The runtime awaits it, stores the result, and steps again.

// ---------------------------------------------------------------------------
// Errors

export class TxError extends Error {
  constructor(message, { retryable = false } = {}) {
    super(message);
    this.name = "TxError";
    this.retryable = retryable;
  }
}

/** An optimistic-concurrency conflict: re-running the transaction may succeed. */
export class TxConflict extends TxError {
  constructor(message = "transaction conflict") {
    super(message, { retryable: true });
    this.name = "TxConflict";
  }
}

export class NetError extends Error {
  constructor(message) {
    super(message);
    this.name = "NetError";
  }
}

function toTxError(e) {
  if (e instanceof TxError) return e;
  return new TxError(String(e && e.message ? e.message : e));
}

function toNetError(e) {
  if (e instanceof NetError) return e;
  return new NetError(String(e && e.message ? e.message : e));
}

// ---------------------------------------------------------------------------
// Transactional store adapters
//
// The language fixes only the transaction *protocol* (a linear Tx that is
// committed or rolled back exactly once, no irrevocable effects inside, an
// outbox flushed after commit, automatic rollback on `?`). Any backend that
// implements this interface can be plugged in:
//
//   interface Store {
//     begin(): Promise<StoreTx>
//     get(key: string): Promise<string | null>     // non-transactional read
//   }
//   interface StoreTx {
//     get(key: string): Promise<string | null>
//     put(key: string, value: string): Promise<void>
//     delete(key: string): Promise<void>
//     commit(): Promise<void>     // throw TxConflict on a serialization conflict
//     rollback(): Promise<void>
//   }

/** In-memory store with optimistic concurrency control (tests, local dev). */
export class MemoryStore {
  constructor(initial = {}) {
    this.data = new Map(); // key -> { value, version }
    this.clock = 0;
    for (const [k, v] of Object.entries(initial)) this.data.set(k, { value: v, version: ++this.clock });
  }
  async get(key) {
    const e = this.data.get(key);
    return e ? e.value : null;
  }
  snapshot() {
    return Object.fromEntries([...this.data].map(([k, e]) => [k, e.value]));
  }
  async begin() {
    const store = this;
    const reads = new Map(); // key -> version observed (0 = absent)
    const writes = new Map(); // key -> value | null (delete)
    return {
      async get(key) {
        if (writes.has(key)) return writes.get(key);
        const e = store.data.get(key);
        if (!reads.has(key)) reads.set(key, e ? e.version : 0);
        return e ? e.value : null;
      },
      async put(key, value) { writes.set(key, value); },
      async delete(key) { writes.set(key, null); },
      async commit() {
        for (const [k, ver] of reads) {
          const e = store.data.get(k);
          if ((e ? e.version : 0) !== ver) throw new TxConflict(`conflict on key ${JSON.stringify(k)}`);
        }
        for (const [k, v] of writes) {
          if (v === null) store.data.delete(k);
          else store.data.set(k, { value: v, version: ++store.clock });
        }
      },
      async rollback() { writes.clear(); },
    };
  }
}

/**
 * Optimistic transactions over a D1 database used as a versioned key-value
 * table. Reads record versions; commit runs one atomic D1 batch that first
 * re-validates every version read (a guard row violating a CHECK constraint
 * aborts the batch on conflict) and then applies the buffered writes. The
 * same scheme maps onto distributed KV stores with conditional writes.
 *
 * Versions come from a single monotonically increasing clock row, so a
 * version is never reused for a key, even across delete and re-create
 * (per-key counters would let `delete; put` reset a key to version 1 and an
 * old reader would validate against a different value: an ABA lost update).
 * Absent keys have version 0.
 *
 * Schema (created on first use; `D1KvStore.schema()` returns it so it can
 * be applied ahead of time, e.g. with `wrangler d1 execute`):
 *   <t>(k TEXT PRIMARY KEY, v TEXT NOT NULL, ver INTEGER NOT NULL)
 *   <t>_guard(conflict INTEGER CHECK (conflict = 0))   -- always empty
 *   <t>_clock(id INTEGER PRIMARY KEY CHECK (id = 0), n INTEGER NOT NULL)
 */
export class D1KvStore {
  constructor(db, table = "kekkai_kv") {
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(table)) throw new Error(`D1KvStore: invalid table name ${table}`);
    this.db = db;
    this.table = table;
    this.ready = null;
  }
  static schema(table = "kekkai_kv") {
    const t = table;
    return [
      `CREATE TABLE IF NOT EXISTS ${t} (k TEXT PRIMARY KEY, v TEXT NOT NULL, ver INTEGER NOT NULL)`,
      `CREATE TABLE IF NOT EXISTS ${t}_guard (conflict INTEGER CHECK (conflict = 0))`,
      `CREATE TABLE IF NOT EXISTS ${t}_clock (id INTEGER PRIMARY KEY CHECK (id = 0), n INTEGER NOT NULL)`,
      // Start the clock above every existing version (tables written by
      // older runtimes or seeded by hand).
      `INSERT OR IGNORE INTO ${t}_clock (id, n) SELECT 0, COALESCE(MAX(ver), 0) FROM ${t}`,
    ];
  }
  init() {
    if (!this.ready) {
      this.ready = this.db.batch(D1KvStore.schema(this.table).map((s) => this.db.prepare(s))).catch((e) => {
        this.ready = null; // retry on the next request
        throw e;
      });
    }
    return this.ready;
  }
  async get(key) {
    await this.init();
    const row = await this.db.prepare(`SELECT v FROM ${this.table} WHERE k = ?`).bind(key).first();
    return row ? row.v : null;
  }
  async begin() {
    await this.init();
    const { db, table } = this;
    const reads = new Map(); // key -> version observed (0 = absent)
    const writes = new Map(); // key -> value | null (delete)
    return {
      async get(key) {
        if (writes.has(key)) return writes.get(key);
        const row = await db.prepare(`SELECT v, ver FROM ${table} WHERE k = ?`).bind(key).first();
        // Keep the first version observed: a later read of the same key
        // must not paper over a change that happened in between.
        if (!reads.has(key)) reads.set(key, row ? row.ver : 0);
        return row ? row.v : null;
      },
      async put(key, value) { writes.set(key, value); },
      async delete(key) { writes.set(key, null); },
      async commit() {
        const stmts = [];
        for (const [k, ver] of reads) {
          stmts.push(db.prepare(
            `INSERT INTO ${table}_guard (conflict) SELECT 1 WHERE COALESCE((SELECT ver FROM ${table} WHERE k = ?), 0) <> ?`,
          ).bind(k, ver));
        }
        if (writes.size > 0) {
          stmts.push(db.prepare(`UPDATE ${table}_clock SET n = n + 1 WHERE id = 0`));
          for (const [k, v] of writes) {
            if (v === null) stmts.push(db.prepare(`DELETE FROM ${table} WHERE k = ?`).bind(k));
            else stmts.push(db.prepare(
              `INSERT INTO ${table} (k, v, ver) VALUES (?, ?, (SELECT n FROM ${table}_clock WHERE id = 0)) ` +
              `ON CONFLICT(k) DO UPDATE SET v = excluded.v, ver = excluded.ver`,
            ).bind(k, v));
          }
        }
        reads.clear();
        writes.clear();
        if (stmts.length === 0) return;
        try {
          await db.batch(stmts);
        } catch (e) {
          throw d1Error(e);
        }
      },
      async rollback() { reads.clear(); writes.clear(); },
    };
  }
}

function d1Error(e) {
  const msg = String(e && e.message ? e.message : e);
  if (/CHECK constraint failed/i.test(msg)) return new TxConflict();
  // Contention inside D1 itself (busy / locked database, overloaded
  // object) is transient: report it as retryable as well.
  if (/SQLITE_BUSY|database is locked|overloaded|reset because its code was updated/i.test(msg)) {
    return new TxError(msg, { retryable: true });
  }
  return new TxError(msg);
}

/**
 * Transactions inside a Durable Object, over its transactional storage
 * (`ctx.storage`, KV or SQLite backed). A Durable Object is a single
 * thread of execution with one strongly consistent storage, so this is the
 * natural home for interactive transactions on Workers.
 *
 * Isolation is serializable via optimistic validation: reads record the
 * value observed, writes are buffered, and commit re-reads every key in
 * one `storage.transaction()` and applies the writes only if nothing
 * changed. (Requests interleave inside an object whenever the program
 * awaits, so validation is still needed; it can only fail when two
 * requests to the same object race on the same keys.) Comparing values
 * rather than versions is sound because validation and the writes are
 * atomic: every value the transaction read is current at the commit
 * point, so the transaction is equivalent to running entirely there.
 *
 * Keys are namespaced with `prefix` so several Db capabilities can share
 * one object.
 */
export class DurableObjectStore {
  constructor(storage, { prefix = "" } = {}) {
    this.storage = storage;
    this.prefix = prefix;
  }
  async get(key) {
    const v = await this.storage.get(this.prefix + key);
    return v === undefined ? null : v;
  }
  async begin() {
    const { storage, prefix } = this;
    const reads = new Map(); // key -> value observed (null = absent)
    const writes = new Map(); // key -> value | null
    return {
      async get(key) {
        if (writes.has(key)) return writes.get(key);
        let v = await storage.get(prefix + key);
        if (v === undefined) v = null;
        if (!reads.has(key)) reads.set(key, v);
        return v;
      },
      async put(key, value) { writes.set(key, value); },
      async delete(key) { writes.set(key, null); },
      async commit() {
        const r = [...reads];
        const w = [...writes];
        reads.clear();
        writes.clear();
        if (w.length === 0 && r.length === 0) return;
        try {
          await storage.transaction(async (txn) => {
            for (const [k, seen] of r) {
              let cur = await txn.get(prefix + k);
              if (cur === undefined) cur = null;
              if (cur !== seen) throw new TxConflict(`conflict on key ${JSON.stringify(k)}`);
            }
            for (const [k, v] of w) {
              if (v === null) await txn.delete(prefix + k);
              else await txn.put(prefix + k, v);
            }
          });
        } catch (e) {
          // The error may cross the storage layer as a plain Error.
          if (e instanceof TxError) throw e;
          if (e && (e.name === "TxConflict" || /^conflict on key/.test(String(e.message)))) throw new TxConflict(e.message);
          throw new TxError(String(e && e.message ? e.message : e));
        }
      },
      async rollback() { reads.clear(); writes.clear(); },
    };
  }
}

/**
 * Skeleton adapter for a remote optimistic KV service over HTTP (a thin
 * gateway in front of FoundationDB, TiKV, Spanner, DynamoDB, ...). See
 * docs/runtime.md for the mapping of each backend onto this protocol.
 *
 *   POST {base}/begin                        -> { "readVersion": any }
 *   POST {base}/get    { key, readVersion }   -> { "value": string|null, "version": any }
 *   POST {base}/commit { readVersion, reads: [{ key, version }],
 *                        writes: [{ key, value|null }] }
 *        -> 200 { "ok": true }               committed atomically
 *        -> 409                              conflict (retryable)
 *        -> 503                              transient failure (retryable)
 *
 * The gateway must make validation of `reads` and application of `writes`
 * atomic; the read version lets MVCC stores serve all reads of one
 * transaction from a single snapshot. Versions are opaque to the adapter.
 */
export class RemoteKvStore {
  constructor(baseUrl, { fetch: fetchFn = globalThis.fetch, headers = {} } = {}) {
    this.base = baseUrl.replace(/\/$/, "");
    this.fetch = fetchFn;
    this.headers = headers;
  }
  async call(path, body) {
    let r;
    try {
      r = await this.fetch(this.base + path, {
        method: "POST",
        headers: { "content-type": "application/json", ...this.headers },
        body: JSON.stringify(body),
      });
    } catch (e) {
      throw new TxError(`remote store unreachable: ${e && e.message ? e.message : e}`, { retryable: true });
    }
    if (r.status === 409) throw new TxConflict();
    if (r.status === 503 || r.status === 429) throw new TxError(`remote store busy (${r.status})`, { retryable: true });
    if (!r.ok) throw new TxError(`remote store error ${r.status}: ${await r.text()}`);
    return r.json();
  }
  async get(key) {
    const { readVersion } = await this.call("/begin", {});
    return (await this.call("/get", { key, readVersion })).value;
  }
  async begin() {
    const store = this;
    const { readVersion } = await this.call("/begin", {});
    const reads = new Map();
    const writes = new Map();
    return {
      async get(key) {
        if (writes.has(key)) return writes.get(key);
        const { value, version } = await store.call("/get", { key, readVersion });
        if (!reads.has(key)) reads.set(key, version);
        return value;
      },
      async put(key, value) { writes.set(key, value); },
      async delete(key) { writes.set(key, null); },
      async commit() {
        const body = {
          readVersion,
          reads: [...reads].map(([key, version]) => ({ key, version })),
          writes: [...writes].map(([key, value]) => ({ key, value })),
        };
        reads.clear();
        writes.clear();
        if (body.writes.length === 0 && body.reads.length === 0) return;
        await store.call("/commit", body);
      },
      async rollback() { reads.clear(); writes.clear(); },
    };
  }
}

// ---------------------------------------------------------------------------
// Default capabilities

export function consoleLog() {
  return {
    info: (m) => console.log(m),
    warn: (m) => console.warn(m),
    error: (m) => console.error(m),
  };
}

export function fetchNet(fetchFn = globalThis.fetch) {
  const call = async (url, init) => {
    const r = await fetchFn(url, init);
    return await r.text();
  };
  return {
    get: (url) => call(url),
    post: (url, body) => call(url, { method: "POST", body }),
  };
}

export const systemClock = () => ({ now: () => Date.now() });

export const cryptoRandom = () => ({
  int(lo, hi) {
    if (hi <= lo) return lo;
    const span = Number(hi - lo);
    const buf = new Uint32Array(2);
    crypto.getRandomValues(buf);
    const r = (buf[0] * 2 ** 32 + buf[1]) % span;
    return lo + BigInt(r);
  },
});

/** Outbox delivery after commit: POST the body to the URL. */
export function fetchOutbox(fetchFn = globalThis.fetch) {
  return async ({ url, body }) => {
    const r = await fetchFn(url, { method: "POST", body });
    if (!r.ok) throw new Error(`outbox POST ${url} failed with status ${r.status}`);
  };
}

/** Outbox that only logs the entries (local development, tests). */
export function logOutbox(log = console) {
  return async ({ url, body }) => {
    log.log(`kekkai outbox: POST ${url} ${JSON.stringify(body)}`);
  };
}

/**
 * Outbox backed by a Cloudflare Queue producer binding: entries are
 * enqueued after commit and delivered (with retries) by a consumer.
 * Delivery is then at-least-once, so receivers should be idempotent.
 */
export function queueOutbox(queue) {
  return async (entry) => {
    await queue.send({ url: entry.url, body: entry.body });
  };
}

// ---------------------------------------------------------------------------
// Request / Response

async function readRequest(request) {
  const url = new URL(request.url);
  const hasBody = request.method !== "GET" && request.method !== "HEAD";
  return {
    method: request.method,
    path: url.pathname,
    url,
    headers: request.headers,
    body: hasBody ? await request.text() : "",
  };
}

function resp(status, body, contentType) {
  const headers = {};
  if (contentType) headers["content-type"] = contentType;
  return { status: Number(status), body, headers };
}

function toResponse(r) {
  const body = r.body === null || r.status === 204 || r.status === 304 ? null : r.body;
  return new Response(body, { status: r.status, headers: r.headers });
}

// ---------------------------------------------------------------------------
// Instance

const OPTION = (v) => (v === undefined ? null : v);

function i64(n) {
  return BigInt.asIntN(64, BigInt(n));
}

/**
 * Create a Kekkai program instance.
 *   module:  WebAssembly.Module compiled from module.wasm
 *   meta:    { strings, handlerParams } (generated kekkai_meta.js)
 */
export function createKekkai(module, meta) {
  let current = null; // the request whose computation is being stepped

  // Asynchronous host operations: they start a promise that settles into
  // the value returned by kek.take() when the computation resumes.
  const start = (promise) => {
    current.pending = promise;
  };
  const asResult = (promise, toErr) =>
    Promise.resolve(promise).then(
      (value) => ({ ok: true, value }),
      (error) => ({ ok: false, error: toErr(error) }),
    );

  const host = {
    // --- runtime helpers ---
    lit: (i) => meta.strings[i],
    take: () => current.result,
    is_null: (x) => (x === null || x === undefined ? 1 : 0),
    to_i64: (x) => i64(x),
    to_i32: (x) => (x ? 1 : 0),
    res_tag: (x) => (x.ok ? 0 : 1),
    res_val: (x) => x.value,
    res_err: (x) => x.error,
    str_eq: (a, b) => (a === b ? 1 : 0),
    str_concat: (a, b) => a + b,

    // --- pure data ---
    "int.to_string": (x) => x.toString(),
    "int.abs": (x) => i64(x < 0n ? -x : x),
    "bool.to_string": (b) => (b ? "true" : "false"),
    "string.len": (s) => BigInt(s.length),
    "string.parse_int": (s) => {
      if (!/^[+-]?\d+$/.test(s)) return null;
      const n = BigInt(s);
      return n === BigInt.asIntN(64, n) ? n : null;
    },
    "string.contains": (s, t) => (s.includes(t) ? 1 : 0),
    "string.starts_with": (s, t) => (s.startsWith(t) ? 1 : 0),
    "string.ends_with": (s, t) => (s.endsWith(t) ? 1 : 0),
    "string.trim": (s) => s.trim(),
    "string.to_upper": (s) => s.toUpperCase(),
    "string.to_lower": (s) => s.toLowerCase(),

    "request.method": (r) => r.method,
    "request.path": (r) => r.path,
    "request.segment": (r, i) => {
      const segs = r.path.split("/").filter((s) => s !== "");
      return OPTION(i >= 0n && i < BigInt(segs.length) ? decodeURIComponent(segs[Number(i)]) : null);
    },
    "request.query": (r, k) => OPTION(r.url.searchParams.get(k)),
    "request.header": (r, k) => OPTION(r.headers.get(k)),
    "request.body": (r) => r.body,

    "response.text": (s, b) => resp(s, b, "text/plain; charset=utf-8"),
    "response.json": (s, b) => resp(s, b, "application/json"),
    "response.empty": (s) => resp(s, null),
    "response.no_content": () => resp(204, null),
    "response.not_found": () => resp(404, "not found", "text/plain; charset=utf-8"),
    "response.bad_request": (m) => resp(400, m, "text/plain; charset=utf-8"),
    "response.with_header": (r, k, v) => ({ ...r, headers: { ...r.headers, [k]: v } }),

    "txError.message": (e) => e.message,
    "txError.retryable": (e) => (e.retryable ? 1 : 0),
    "netError.message": (e) => e.message,

    // --- capabilities (sync) ---
    "log.info": (log, m) => log.info(m),
    "log.warn": (log, m) => log.warn(m),
    "log.error": (log, m) => log.error(m),
    "clock.now_ms": (c) => i64(c.now()),
    "random.int": (r, lo, hi) => i64(r.int(lo, hi)),

    // --- capabilities (async) ---
    "net.get": (net, url) => start(asResult(net.get(url), toNetError)),
    "net.post": (net, url, body) => start(asResult(net.post(url, body), toNetError)),

    "db.get": (db, key) => start(asResult(db.store.get(key).then(OPTION), toTxError)),
    "db.begin": (db) => start(db.store.begin().then((inner) => new Transaction(inner, db.outbox, current))),
    "tx.get": (tx, key) => start(asResult(tx.inner.get(key).then(OPTION), toTxError)),
    "tx.put": (tx, key, value) => start(asResult(tx.inner.put(key, value), toTxError)),
    "tx.delete": (tx, key) => start(asResult(tx.inner.delete(key), toTxError)),
    "tx.outbox": (tx, url, body) => {
      tx.outbox.push({ url, body });
    },
    "tx.commit": (tx) => start(asResult(tx.commit(), toTxError)),
    "tx.rollback": (tx) => {
      tx.rollbackPromise = tx.rollback();
    },
    "tx.finish": (tx) => start(tx.finish()),
  };

  const imports = { kek: {} };
  for (const imp of WebAssembly.Module.imports(module)) {
    const fn = host[imp.name];
    if (!fn) throw new Error(`kekkai runtime: missing host operation ${imp.name}`);
    imports.kek[imp.name] = fn;
  }
  const instance = new WebAssembly.Instance(module, imports);
  const ex = instance.exports;

  async function run(args, ctx) {
    const req = { pending: null, result: undefined, ctx };
    current = req;
    const frame = ex.handler_new(...args);
    for (;;) {
      current = req;
      const suspended = ex.handler_step(frame);
      current = null;
      if (!suspended) break;
      const p = req.pending;
      req.pending = null;
      req.result = await p;
    }
    return ex.handler_result(frame);
  }

  return {
    exports: ex,
    /**
     * Run the handler. `caps` maps handler parameter names to capability
     * objects (Db capabilities are { store, outbox }).
     */
    async handle(request, caps, ctx = {}) {
      const args = [];
      for (const p of meta.handlerParams) {
        if (p.kind === "request") args.push(await readRequest(request));
        else {
          const c = caps[p.name];
          if (!c) throw new Error(`kekkai: missing capability ${p.name}: ${p.kind}`);
          args.push(c);
        }
      }
      try {
        return toResponse(await run(args, ctx));
      } catch (e) {
        console.error("kekkai: handler failed:", e);
        return new Response("internal error", { status: 500 });
      }
    },
  };
}

class Transaction {
  constructor(inner, outbox, req) {
    this.inner = inner;
    this.open = true;
    this.outbox = [];
    this.deliver = outbox;
    this.ctx = req && req.ctx;
    this.rollbackPromise = null;
  }
  async commit() {
    this.open = false;
    try {
      await this.inner.commit();
    } catch (e) {
      // a failed commit ends the transaction: make sure nothing is left applied
      try { await this.inner.rollback(); } catch {}
      throw e;
    }
    // Irrevocable effects run only after a successful commit.
    const entries = this.outbox;
    this.outbox = [];
    const work = Promise.all(entries.map((e) => Promise.resolve(this.deliver(e)).catch((err) => console.error("kekkai: outbox delivery failed:", err))));
    if (this.ctx && this.ctx.waitUntil) this.ctx.waitUntil(work);
    else await work;
  }
  rollback() {
    this.open = false;
    this.outbox = [];
    return this.inner.rollback();
  }
  async finish() {
    if (this.rollbackPromise) await this.rollbackPromise;
    if (this.open) await this.rollback();
  }
}

/** Build a Db capability from a store adapter. */
export function dbCap(store, outbox = fetchOutbox()) {
  return { store, outbox };
}
