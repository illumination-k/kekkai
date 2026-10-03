// Golden tests of the agent tooling commands of the self-hosted compiler,
// run through the ./kek launcher (no Go needed):
//
//   caps [-json], check -json, search [-json] [-limit n], ir -json, and the
//   Workers glue written by build (worker.js, wrangler.toml, -target d1|do).
//
//   node --test tests/agent_cmds.test.mjs
//   UPDATE=1 node --test tests/agent_cmds.test.mjs   # rewrite the golden files
import test from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, readFile, readdir, rm, writeFile, mkdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const goldenDir = path.join(root, "tests", "agent_cmds", "golden");
const update = !!process.env.UPDATE;

function kek(args) {
  return new Promise((resolve, reject) => {
    const p = process.env.KEK
      ? spawn(process.env.KEK, args, { cwd: root })
      : spawn(process.execPath, [path.join(root, "js", "kek.mjs"), ...args], { cwd: root });
    let out = "", err = "";
    p.stdout.setEncoding("utf8").on("data", (d) => (out += d));
    p.stderr.setEncoding("utf8").on("data", (d) => (err += d));
    p.on("error", reject);
    p.on("close", (code) => resolve({ out, err, code }));
  });
}

function render({ out, err, code }) {
  let s = out;
  if (err !== "") s += "--- stderr\n" + err;
  return s + `--- exit ${code}\n`;
}

const spell = { "&": "amp", "<": "lt", ">": "gt", "(": "lp", ")": "rp", ",": "comma", "=": "eq", "/": "_", " ": "_" };

function goldenName(args) {
  return args.join(" ").replace(/[^A-Za-z0-9_.-]/g, (c) => spell[c] ?? "_") + ".txt";
}

async function checkGolden(name, got) {
  const file = path.join(goldenDir, name);
  if (update) {
    await mkdir(goldenDir, { recursive: true });
    await writeFile(file, got);
    return;
  }
  assert.equal(got, await readFile(file, "utf8"), `output differs from ${path.relative(root, file)}`);
}

const bank = "testdata/e2e/bank.kek";
const todo = "examples/todo/todo.kek";
const lint = "tests/agent_cmds/lint.kek";

const cases = [
  ["caps", bank],
  ["caps", todo],
  ["caps", lint],
  ["caps", "testdata/test/counter.kek"],
  ["caps", "testdata/check/err_caps.kek"],
  ["caps", "-json", bank],
  ["caps", "-json", lint],
  ["check", "-json", lint],
  ["check", "-json", bank],
  ["check", "-json", "tests/agent_cmds/parse_err.kek"],
  ["check", "-json", "testdata/check/err_caps.kek"],
  ["check", "-json", "testdata/check/err_types.kek"],
  ["check", "-json", "testdata/check/err_tx.kek"],
  ["check", "-json", "examples/webhooks/net_in_tx.bad.kek"],
  ["check", "-json=false", bank],
  ["ir", "-json", "testdata/run/collections.kek"],
  ["ir", "-json", "testdata/run/strings.kek"],
  ["ir", "-json", bank],
  ["search", "String -> Option<Int>", bank],
  ["search", "-json", "String -> Option<Int>", bank],
  ["search", "-limit", "5", "Int", lint],
  ["search", "-json", "-limit", "0", "a -> Option<a>", lint],
  ["search", "-json", "&Log, String -> ()"],
  ["search", "&Db -> Result<_, TxError>", todo],
  ["search", "(Int, Int) -> Int", lint],
  ["search", "Shape"],
  ["search", "Shape", lint],
  ["search", "-limit=3", "String, Int -> String"],
  ["search", "Int ->"],
  ["search", "&Int"],
  ["search", "Option<Int"],
  ["search", "->"],
  ["search", "-limit", "x", "Int"],
  ["caps", "-x", bank],
];

test("golden names are distinct", () => {
  const names = cases.map(goldenName);
  assert.equal(new Set(names).size, names.length);
});

test("agent commands", { concurrency: 8 }, async (t) => {
  await Promise.all(
    cases.map((args) =>
      t.test(args.join(" "), async () => {
        await checkGolden(goldenName(args), render(await kek(args)));
      }),
    ),
  );
});

// build: the files written next to module.wasm (the runtime is copied by
// the launcher; module.wasm and kekkai_meta.js are covered elsewhere).
const builds = [
  { name: "build_bank_d1", path: bank, flags: [] },
  { name: "build_todo_do", path: todo, flags: ["-target", "do"] },
  { name: "build_webhooks", path: "examples/webhooks/webhooks.kek", flags: ["-target=d1"] },
  { name: "build_payments_keep_toml", path: "examples/payments/payments.kek", flags: ["-target", "do"], toml: "# edited\n" },
  { name: "build_bad_target", path: bank, flags: ["-target", "bogus"] },
  { name: "build_main_program", path: "testdata/run/strings.kek", flags: [] },
];

test("build glue", { concurrency: 4 }, async (t) => {
  await Promise.all(
    builds.map((b) =>
      t.test(b.name, async () => {
        const out = await mkdtemp(path.join(tmpdir(), "kek-build-"));
        try {
          if (b.toml) await writeFile(path.join(out, "wrangler.toml"), b.toml);
          const r = await kek(["build", "-o", out, ...b.flags, b.path]);
          let s = render({ ...r, out: r.out.replaceAll(out, "OUT") });
          const files = (await readdir(out)).sort();
          s += "--- files: " + files.join(" ") + "\n";
          for (const f of ["worker.js", "wrangler.toml"]) {
            if (files.includes(f)) s += `--- ${f}\n` + (await readFile(path.join(out, f), "utf8"));
          }
          await checkGolden(b.name + ".txt", s);
        } finally {
          await rm(out, { recursive: true, force: true });
        }
      }),
    ),
  );
});
