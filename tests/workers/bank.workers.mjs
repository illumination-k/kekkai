// HTTP scenario for testdata/e2e/bank.kek running on workerd:
//   node bank.workers.mjs <base-url>
// D1 must already hold balance:alice=100 and balance:bob=5.
import assert from "node:assert/strict";

const base = process.argv[2];

async function req(method, path, { backend, conflict, body } = {}) {
  const headers = {};
  if (backend === "do") headers["x-kekkai-backend"] = "do";
  if (conflict) headers["x-test-conflict"] = conflict;
  const r = await fetch(base + path, { method, headers, body });
  return { status: r.status, body: await r.text(), type: r.headers.get("content-type") };
}

async function balances(backend) {
  const a = await req("GET", "/balance/alice", { backend });
  const b = await req("GET", "/balance/bob", { backend });
  return [Number(a.body), Number(b.body)];
}

async function scenario(backend) {
  const o = { backend };
  let r = await req("GET", "/balance/alice", o);
  assert.deepEqual([r.status, r.body], [200, "100"], `${backend}: seeded balance`);
  r = await req("GET", "/balance/carol", o);
  assert.equal(r.status, 404);

  r = await req("POST", "/transfer?from=alice&to=bob&amount=30", o);
  assert.deepEqual([r.status, r.body, r.type], [200, '{"left":70}', "application/json"], `${backend}: transfer`);
  assert.deepEqual(await balances(backend), [70, 35]);

  r = await req("POST", "/transfer?from=alice&to=bob&amount=1000", o);
  assert.deepEqual([r.status, r.body], [409, "insufficient funds"]);
  r = await req("POST", "/transfer?from=nobody&to=bob&amount=1", o);
  assert.deepEqual([r.status, r.body], [404, "no account nobody"]);
  assert.deepEqual(await balances(backend), [70, 35], `${backend}: rollbacks write nothing`);

  // A concurrent writer commits between our reads and our commit: the
  // optimistic check must reject the transfer as a retryable conflict.
  r = await req("POST", "/transfer?from=alice&to=bob&amount=5", { ...o, conflict: "balance:bob" });
  assert.deepEqual([r.status, r.body], [503, "conflict, retry"], `${backend}: forced conflict`);
  assert.deepEqual(await balances(backend), [70, 1035], `${backend}: only the concurrent write is applied`);

  // Real concurrency: many transfers race on the same two balances.
  const N = 24;
  const rs = await Promise.all(Array.from({ length: N }, () => req("POST", "/transfer?from=alice&to=bob&amount=1", o)));
  const ok = rs.filter((x) => x.status === 200).length;
  const conflicts = rs.filter((x) => x.status === 503).length;
  assert.equal(ok + conflicts, N, `${backend}: unexpected statuses ${rs.map((x) => x.status)}`);
  assert.ok(ok >= 1);
  assert.deepEqual(await balances(backend), [70 - ok, 1035 + ok], `${backend}: each committed transfer applied exactly once`);
  console.log(`${backend}: ${N} concurrent transfers -> ${ok} committed, ${conflicts} conflicts`);
}

await scenario("d1");

let r = await req("POST", "/__test/seed", { backend: "do", body: JSON.stringify({ "balance:alice": "100", "balance:bob": "5" }) });
assert.equal(r.status, 200, r.body);
await scenario("do");

// The two backends hold separate data.
assert.equal((await balances("d1"))[0] + (await balances("d1"))[1], 1105);
console.log("ok");
