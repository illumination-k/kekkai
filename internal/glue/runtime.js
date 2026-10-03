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

export class IoError extends Error {
  constructor(message) {
    super(message);
    this.name = "IoError";
  }
}

function toIoError(e) {
  if (e instanceof IoError) return e;
  return new IoError(String(e && e.message ? e.message : e));
}

/** File system capability for command-line programs (Node only). */
export async function nodeFs() {
  const fsp = await import("node:fs/promises");
  return {
    read: (p) => fsp.readFile(p, "utf8"),
    write: (p, c) => fsp.writeFile(p, c),
    writeBytes: (p, b) => fsp.writeFile(p, b),
  };
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
 */
export class D1KvStore {
  constructor(db, table = "kekkai_kv") {
    this.db = db;
    this.table = table;
    this.ready = null;
  }
  init() {
    if (!this.ready) {
      const t = this.table;
      this.ready = this.db.batch([
        this.db.prepare(`CREATE TABLE IF NOT EXISTS ${t} (k TEXT PRIMARY KEY, v TEXT NOT NULL, ver INTEGER NOT NULL)`),
        this.db.prepare(`CREATE TABLE IF NOT EXISTS ${t}_guard (conflict INTEGER CHECK (conflict = 0))`),
      ]);
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
    const reads = new Map();
    const writes = new Map();
    return {
      async get(key) {
        if (writes.has(key)) return writes.get(key);
        const row = await db.prepare(`SELECT v, ver FROM ${table} WHERE k = ?`).bind(key).first();
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
        for (const [k, v] of writes) {
          if (v === null) stmts.push(db.prepare(`DELETE FROM ${table} WHERE k = ?`).bind(k));
          else stmts.push(db.prepare(
            `INSERT INTO ${table} (k, v, ver) VALUES (?, ?, 1) ON CONFLICT(k) DO UPDATE SET v = excluded.v, ver = ver + 1`,
          ).bind(k, v));
        }
        if (stmts.length === 0) return;
        try {
          await db.batch(stmts);
        } catch (e) {
          if (/CHECK constraint failed/i.test(String(e && e.message))) throw new TxConflict();
          throw e;
        }
      },
      async rollback() { writes.clear(); },
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
    await fetchFn(url, { method: "POST", body });
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

    // --- collections (Vec <-> JS array conversion, Map as a JS Map) ---
    arr_new: () => [],
    arr_len: (a) => a.length,
    arr_get: (a, i) => a[i],
    arr_push: (a, x) => { a.push(x); },
    box_i64: (x) => x,
    box_i32: (x) => x,
    map_new: () => new Map(),
    map_len: (m) => BigInt(m.size),
    map_keys: (m) => [...m.keys()],
    map_get: (m, k) => (m.has(k) ? m.get(k) : null),
    map_insert: (m, k, v) => { m.set(k, v); },
    map_contains: (m, k) => (m.has(k) ? 1 : 0),
    map_remove: (m, k) => { m.delete(k); },

    // --- pure data ---
    "int.min": (a, b) => (a < b ? a : b),
    "int.max": (a, b) => (a > b ? a : b),
    "string.char_at": (s, i) => (i >= 0n && i < BigInt(s.length) ? BigInt(s.charCodeAt(Number(i))) : null),
    "string.slice": (s, a, b) => {
      const n = BigInt(s.length);
      const clamp = (x) => (x < 0n ? 0n : x > n ? n : x);
      const lo = clamp(a), hi = clamp(b);
      return hi <= lo ? "" : s.slice(Number(lo), Number(hi));
    },
    "string.index_of": (s, t) => { const i = s.indexOf(t); return i < 0 ? null : BigInt(i); },
    "string.replace": (s, a, b) => (a === "" ? s : s.split(a).join(b)),
    "string.split": (s, sep) => (sep === "" ? [...s] : s.split(sep)),
    "string.to_bytes": (s) => [...new TextEncoder().encode(s)].map(BigInt),
    "string.from_char": (c) => String.fromCharCode(Number(BigInt.asUintN(16, c))),
    "string.from_bytes": (a) => new TextDecoder().decode(new Uint8Array(a.map((x) => Number(BigInt.asUintN(8, x))))),
    "ioError.message": (e) => e.message,
    "fs.read": (fs, p) => start(asResult(fs.read(p), toIoError)),
    "fs.write": (fs, p, c) => start(asResult(fs.write(p, c), toIoError)),
    "fs.write_bytes": (fs, p, a) => start(asResult(fs.writeBytes(p, new Uint8Array(a.map((x) => Number(BigInt.asUintN(8, x))))), toIoError)),

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
     * Run a #[main] program: `args` is an array of strings, `caps` maps
     * parameter names to capabilities. Returns the exit code.
     */
    async main(argv, caps, ctx = {}) {
      const args = [];
      for (const p of meta.handlerParams) {
        if (p.kind === "args") args.push(argv);
        else {
          const c = caps[p.name];
          if (!c) throw new Error(`kekkai: missing capability ${p.name}: ${p.kind}`);
          args.push(c);
        }
      }
      return Number(await run(args, ctx));
    },
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
