// Main module of the "hooks" e2e service in workerd (generated config:
// tests/suites/e2e.sh). It is the global outbound of the "worker" service,
// so every fetch the program makes (outbox deliveries with
// KEKKAI_OUTBOX = "fetch") arrives here and is recorded:
//
//   any POST                  records { url, body } and answers 200
//   GET http://hooks/__entries  the recorded entries, in order (JSON)
//
// Entries are kept in the storage of one Durable Object, so they survive
// across requests and isolates.
import { DurableObject } from "cloudflare:workers";

export class HookLog extends DurableObject {
  async add(entry) {
    const n = (await this.ctx.storage.get("n")) ?? 0;
    await this.ctx.storage.put({ n: n + 1, ["e" + String(n).padStart(8, "0")]: entry });
  }
  async entries() {
    return [...(await this.ctx.storage.list({ prefix: "e" })).values()];
  }
}

export default {
  async fetch(request, env) {
    const log = env.LOG.get(env.LOG.idFromName("log"));
    if (request.method === "GET" && new URL(request.url).pathname === "/__entries") {
      return Response.json(await log.entries());
    }
    if (request.method !== "POST") return new Response("only POST is recorded", { status: 405 });
    await log.add({ url: request.url, body: await request.text() });
    return new Response("recorded");
  },
};
