// Main module of the "driver" e2e service in workerd (generated config:
// tests/suites/e2e.sh). Its test() drives testdata/e2e/bank.kek, served by
// the generated worker.js in the "worker" service (worker_wrapper.js), over
// HTTP through a service binding, once on the D1 backend and once on the
// Durable Object backend:
//
//   - reads, transfers, explicit rollbacks
//   - a forced optimistic conflict (a concurrent writer commits between the
//     transaction's reads and its commit): 503, only the other write applied
//   - real concurrent requests racing on the same balances: every committed
//     transfer applied exactly once
//   - the outbox: entries are delivered by fetch (to the "hooks" service)
//     after each commit, never for rolled-back or conflicting transactions
//
// Output: "KEK <case> | <line>" lines and a final "KEK <case> ok|fail".
import assert from "node:assert/strict";

const TRANSFER_HOOK = "https://hooks.example/transfer";

async function scenario(env, backend, log) {
  async function req(method, path, { conflict, body } = {}) {
    const headers = {};
    if (backend === "do") headers["x-kekkai-backend"] = "do";
    if (conflict) headers["x-test-conflict"] = conflict;
    const r = await env.WORKER.fetch("http://bank.test" + path, { method, headers, body });
    return { status: r.status, body: await r.text(), type: r.headers.get("content-type") };
  }
  async function balances() {
    const a = await req("GET", "/balance/alice");
    const b = await req("GET", "/balance/bob");
    return [Number(a.body), Number(b.body)];
  }
  async function hooks() {
    return await (await env.HOOKS.fetch("http://hooks/__entries")).json();
  }
  // Deliveries may still be in flight (waitUntil) when a response arrives.
  async function hooksSettled(want) {
    let got;
    for (let i = 0; i < 100; i++) {
      got = await hooks();
      if (got.length >= want) break;
      await new Promise((r) => setTimeout(r, 20));
    }
    return got;
  }

  const hooksBefore = (await hooks()).length;
  let r = await req("POST", "/__test/seed", { body: JSON.stringify({ "balance:alice": "100", "balance:bob": "5" }) });
  assert.equal(r.status, 200, `${backend}: seed: ${r.body}`);

  r = await req("GET", "/balance/alice");
  assert.deepEqual([r.status, r.body], [200, "100"], `${backend}: seeded balance`);
  r = await req("GET", "/balance/carol");
  assert.equal(r.status, 404);

  r = await req("POST", "/transfer?from=alice&to=bob&amount=30");
  assert.deepEqual([r.status, r.body, r.type], [200, '{"left":70}', "application/json"], `${backend}: transfer`);
  assert.deepEqual(await balances(), [70, 35]);

  r = await req("POST", "/transfer?from=alice&to=bob&amount=1000");
  assert.deepEqual([r.status, r.body], [409, "insufficient funds"]);
  r = await req("POST", "/transfer?from=nobody&to=bob&amount=1");
  assert.deepEqual([r.status, r.body], [404, "no account nobody"]);
  assert.deepEqual(await balances(), [70, 35], `${backend}: rollbacks write nothing`);

  // A concurrent writer commits between our reads and our commit: the
  // optimistic check must reject the transfer as a retryable conflict.
  r = await req("POST", "/transfer?from=alice&to=bob&amount=5", { conflict: "balance:bob" });
  assert.deepEqual([r.status, r.body], [503, "conflict, retry"], `${backend}: forced conflict`);
  assert.deepEqual(await balances(), [70, 1035], `${backend}: only the concurrent write is applied`);

  // Real concurrency: many transfers race on the same two balances.
  const N = 24;
  const rs = await Promise.all(Array.from({ length: N }, () => req("POST", "/transfer?from=alice&to=bob&amount=1")));
  const ok = rs.filter((x) => x.status === 200).length;
  const conflicts = rs.filter((x) => x.status === 503).length;
  assert.equal(ok + conflicts, N, `${backend}: unexpected statuses ${rs.map((x) => x.status)}`);
  assert.ok(ok >= 1);
  assert.deepEqual(await balances(), [70 - ok, 1035 + ok], `${backend}: each committed transfer applied exactly once`);
  log(`${backend}: ${N} concurrent transfers -> ${ok} committed, ${conflicts} conflicts`);

  // One delivery per committed transfer, after the commit; none for the
  // rollbacks or the conflicting transfer.
  const sent = (await hooksSettled(hooksBefore + 1 + ok)).slice(hooksBefore);
  const bodies = sent.map((e) => e.body).sort();
  const want = ["alice->bob:30", ...Array(ok).fill("alice->bob:1")].sort();
  assert.deepEqual(bodies, want, `${backend}: outbox deliveries`);
  assert.ok(sent.every((e) => e.url === TRANSFER_HOOK), `${backend}: outbox URLs ${sent.map((e) => e.url)}`);
  log(`${backend}: ${sent.length} outbox deliveries`);
  return balances;
}

export default {
  async test(controller, env) {
    const out = [];
    const log = (m) => out.push(m);
    let ok = true;
    try {
      const d1Balances = await scenario(env, "d1", log);
      await scenario(env, "do", log);
      // The two backends hold separate data.
      const [a, b] = await d1Balances();
      assert.equal(a + b, 1105, "d1 data untouched by the Durable Object run");
    } catch (e) {
      ok = false;
      out.push(e && e.stack ? e.stack : String(e));
    }
    for (const entry of out) for (const line of entry.split("\n")) console.log(`KEK ${env.CASE} | ${line}`);
    console.log(`KEK ${env.CASE} ${ok ? "ok" : "fail"}`);
  },
};
