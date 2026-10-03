// Main module of one e2e test service in workerd (generated config:
// tests/suites/e2e.sh). The service bundles:
//
//   scenario.js        the scenario: a *.test.js next to a program, or the
//                      adapter conformance suite (adapters.test.js)
//   program.js         exports { module, meta } of the compiled program
//                      (module.wasm, kekkai_meta.js), or nulls
//   kekkai_runtime.js  the runtime the program was built with
//   worker.js          the generated Workers entry point (programs only;
//                      WITH_WORKER = "1" checks that it loads)
//   fakes.js           storage test doubles
//
// workerd calls test(); it runs the scenario inside the Runner Durable
// Object, so DurableObjectStore uses real Durable Object storage and
// FakeD1 databases live in FakeD1Object instances (SQLite). Output is
// printed as
//
//   KEK <case> | <line>     console output of the scenario, error report
//   KEK <case> ok|fail      the result
//
// which the suite script parses.
//
// A scenario's default export receives { createApp, runtime, meta, module,
// assert, recordingLog, recordingOutbox, call, fakeNet, fakes, allStores }.
import { DurableObject } from "cloudflare:workers";
import assert from "./assert.js";
import * as runtime from "./kekkai_runtime.js";
import * as program from "./program.js";
import scenario from "./scenario.js";
import { makeFakes } from "./fakes.js";

export { FakeD1Object } from "./fakes.js";

function recordingLog() {
  const lines = [];
  return { lines, info: (m) => lines.push(["info", m]), warn: (m) => lines.push(["warn", m]), error: (m) => lines.push(["error", m]) };
}

function recordingOutbox() {
  const sent = [];
  const deliver = async (e) => { sent.push(e); };
  deliver.sent = sent;
  return deliver;
}

// A Net capability that records requests and answers from a table of
// url -> body (or a function url -> body); unknown URLs fail.
function fakeNet(routes = {}) {
  const calls = [];
  const answer = async (method, u, body) => {
    calls.push({ method, url: u, body });
    const r = typeof routes === "function" ? routes(u, body) : routes[u];
    if (r === undefined) throw new Error(`fakeNet: no route for ${u}`);
    return r;
  };
  return { calls, get: (u) => answer("GET", u), post: (u, b) => answer("POST", u, b) };
}

async function call(app, caps, method, pathAndQuery, body) {
  const req = new Request("http://test" + pathAndQuery, { method, body });
  const res = await app.handle(req, caps);
  return { status: res.status, body: await res.text(), headers: res.headers };
}

const show = (x) => (typeof x === "string" ? x : x instanceof Error ? x.stack || String(x) : JSON.stringify(x));

// The generated worker.js must load: it instantiates module.wasm at the top
// level and exports the fetch handler and the Durable Object class.
async function checkWorker() {
  let w;
  try {
    w = await import("./worker.js");
  } catch (e) {
    throw new Error(`generated worker.js does not load: ${show(e)}`);
  }
  assert.equal(typeof w.default.fetch, "function", "worker.js exports a fetch handler");
  assert.equal(typeof w.KekkaiObject, "function", "worker.js exports KekkaiObject");
  return "worker.js loads";
}

export class Runner extends DurableObject {
  async run(withWorker) {
    const out = [];
    const saved = { log: console.log, warn: console.warn, error: console.error };
    const capture = (...a) => { out.push(a.map(show).join(" ")); };
    console.log = console.warn = console.error = capture;
    try {
      if (withWorker) out.push(await checkWorker());
      const fakes = makeFakes(this.env, this.ctx.storage);
      await scenario({
        createApp: () => runtime.createKekkai(program.module, program.meta),
        runtime, meta: program.meta, module: program.module, assert,
        recordingLog, recordingOutbox, call, fakeNet, fakes,
        // One seeded instance of every store adapter: [{ name, store, ... }].
        allStores: (initial) => fakes.allStores(runtime, initial),
      });
      return { ok: true, out };
    } catch (e) {
      out.push(show(e));
      return { ok: false, out };
    } finally {
      Object.assign(console, saved);
    }
  }
}

export default {
  async test(controller, env) {
    let r;
    try {
      r = await env.RUNNER.get(env.RUNNER.idFromName("run")).run(env.WITH_WORKER === "1");
    } catch (e) {
      r = { ok: false, out: [show(e)] };
    }
    for (const entry of r.out) for (const line of entry.split("\n")) console.log(`KEK ${env.CASE} | ${line}`);
    console.log(`KEK ${env.CASE} ${r.ok ? "ok" : "fail"}`);
  },
};
