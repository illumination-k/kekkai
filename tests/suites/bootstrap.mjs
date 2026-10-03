// bootstrap: the self-hosting fixed point. `./kek bootstrap-check` builds
// compiler/ with the committed bootstrap compiler (stage1), rebuilds it
// with stage1 (stage2) and requires stage1 == stage2 byte for byte. It also
// leaves stage1 in .kek-cache/, so it runs first and the other suites reuse
// the cached compiler.
import { fail, runKek } from "../lib.mjs";

export async function bootstrapCases() {
  return [{
    name: "fixed-point",
    run: async () => {
      const r = await runKek(["bootstrap-check"]);
      if (r.code !== 0) fail(`exit ${r.code}\n${r.out}`);
      return r.out.trim();
    },
  }];
}
