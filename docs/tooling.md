# Agent tooling

These `kek` commands give LLM coding agents machine-readable
access to the type checker. Each command reads one `.kek` file. They are
implemented by the self-hosted compiler (`compiler/caps_*`, `diag_*`,
`search_*`, `irjson_*`) and run with the `./kek` launcher.

## `kek check -json <file.kek>`

Prints every diagnostic as JSON. The exit status is 1 when there is at
least one error. Warnings do not change the exit status.

```json
{
  "file": "x.kek",
  "ok": false,
  "diagnostics": [
    {"file": "x.kek", "line": 3, "col": 5, "end_line": 3, "end_col": 9,
     "severity": "error", "phase": "type",
     "message": "mismatched types in function result: expected `Int`, found `String`"}
  ]
}
```

- Lines and columns are 1-based and count bytes. `end_col` is exclusive.
- `phase` is one of:
  - `parse`: syntax errors. When there are any, no type errors are reported.
  - `type`: type, capability and linearity errors.
  - `mut`: mutability errors (docs/mutability.md): a mutation through a
    binding that is not declared mutable or through a shared reference
    (`&T`, `&self`), a borrowed value kept in an owning place or returned
    as an owned type. They are checked only when there are no type errors;
    `kek fix` adds the missing `mut` for many of them.
  - `refine`: refinement errors (docs/refinement.md): an index `v[i]` not
    proved in bounds, a precondition not proved at a call, a postcondition
    or a refined alias (`type Port = Int where ...`) not proved. They are
    checked only when there are no type errors.
  - `lint`: warnings: a capability that a function receives but never uses;
    a division whose divisor may be zero (or `MIN / -1`), and arithmetic
    that may overflow (refinement lints). The refinement lints become
    errors (severity `error`, phase still `lint`) or disappear with the
    `[refine]` section of `./kekkai.toml`:
    `overflow` / `division` = `"auto"` (default: warnings, only in functions
    that use refinements), `"lint"` (warnings everywhere), `"error"` or
    `"off"`.
- Refinement diagnostics (phase `refine`, and the refinement lints) have
  two more fields:
  - `counterexample`: an object from source names to integers, the
    solver's model for the variables of the condition
    (`{"i": 0, "v.len()": 0}`; `{}` when the condition has no variables
    or the solver gave up);
  - `facts`: the facts the solver was given, each with the position it
    comes from: `{"line": 8, "col": 7, "fact": "precondition `0 <= i`"}`.

```json
{"file": "x.kek", "line": 4, "col": 6, "end_line": 4, "end_col": 7,
 "severity": "error", "phase": "refine",
 "message": "cannot prove the index is in bounds: `0 <= i && i < v.len()` (counterexample: i = 0, v.len() = 0)",
 "counterexample": {"i": 0, "v.len()": 0},
 "facts": [{"line": 3, "col": 8, "fact": "`v.len() >= 0`"}]}
```

- When the solver runs out of budget the message starts with
  `unknown (solver budget: <reason>)` instead of `cannot prove`.
- `kek check -v <file|dir>` prints the same diagnostics as text with their
  facts (`    fact: x.kek:3:8: ...`), and the proved conditions as notes
  with the facts they used (the unsat core) on stdout. Plain `kek check`
  prints errors and warnings (`x.kek:5:3: warning: ...`); only errors
  change the exit status.

## `kek caps -json <file.kek>`

Prints one entry per function, in declaration order. The file must type-check.

```json
{"file": "bank.kek", "functions": [
  {"name": "transfer", "signature": "fn transfer(db: &Db, log: &Log, ...) -> Result<Int, TransferError>",
   "line": 25, "col": 4, "handler": false, "pure": false, "async": true,
   "idempotent": false, "readonly": true,
   "caps": [{"name": "db", "type": "&Db", "used": true}, {"name": "log", "type": "&Log", "used": true}],
   "direct_effects": ["db.transaction", "log.info", "tx.commit", "..."],
   "unused_caps": [], "calls": ["balance_key", "read_balance"],
   "declassify": [{"call": "mask", "line": 31, "col": 40}]}
]}
```

Capabilities are second-class and there is no ambient authority, so `caps` lists
every effect that the function and its callees can perform. A function is
`pure` when `caps` is empty. `async` means the function can reach an
asynchronous builtin, so it is compiled to a resumable state machine.
`idempotent` means every capability operation the function can reach
through the call graph is idempotent (true for pure functions; a
`#[handler(idempotent)]` must have it, see [language.md](language.md)).
`declassify` lists the calls of `mask`, `hash` and `expose_unchecked` on
personal data (`Pii`) in the function's body, at the method name.
`readonly` means the function mutates none of its inputs: no parameter is
`mut`, `&mut`, `mut self` or `&mut self`, and the body passes the
mutability rules with its owned parameters taken as shared (it neither
mutates them nor moves them into a mutable place; see
[mutability.md](mutability.md)). `kek assure` records it as the guarantee
`readonly` for the functions that take values with mutable state.

The `signature` shows the parameter and result types as written:
`&T` and `&mut T` (`self: &Point` for `&self`), so does `kek search`.

The text form adds, per function with capabilities, `idempotent: true` or
`idempotent: false (<op> via <callee>)`, a line `declassify: mask (31:40),
...` when the function declassifies, and the tag `#[handler(idempotent)]`.

## `kek hash [-json] <file|dir>`

Prints the definition hash of every user function
(`compiler/defhash.kek`). The hash is alpha-normalized: local variables are
numbered in declaration order and a function's own name is replaced, so
renaming does not change it. `hash_lits` abstracts the values of literals.
`trans` also covers every user definition the function can reach and the
type declarations, so an equal `trans` means equal behaviour. It is the
key of the test, build, coverage and mutation caches, and the identity used
by `similar` and `assure`.

```json
{"path": "x.kek", "hash": "<program>", "types": "<type declarations>",
 "functions": [{"name": "visit", "file": "x.kek", "line": 12, "sig": "(&Db, &Log, String) -> Result<Int, TxError>",
                "caps": ["Db", "Log"], "hash": "...", "hash_lits": "...", "trans": "...", "deps": ["counter_key"]}]}
```

## `kek config`

Prints `./kekkai.toml`, the project configuration read by `similar`,
`cover`, `mutate` and `assure`, as JSON (exit 1 on a syntax error). The
format is a TOML subset: `[section]` / `[section."sub"]` headers and
`key = value` with strings, integers, booleans and arrays.

## `kek fix [-w] <paths>`

Adds the `mut` that the mutability rules (docs/mutability.md) ask for.
Each path is one program, like `kek check` (a file, or a directory of
`.kek` files; `lib/core` and `lib/prelude` are checked as the core library
and the prelude). The program is checked, and for every diagnostic of
phase `mut` that names a binding or parameter the edit is applied:
`let x` → `let mut x`, `x: T` → `mut x: T`, `self` → `mut self`,
`Some(x)` → `Some(mut x)`, `for x` → `for mut x`, `|x|` → `|mut x|`, a
mutated `x: &T` → `x: &mut T` (`&self` → `&mut self`), and an argument
`&v` given to a `&mut T` parameter → `&mut v`. The program is checked
again until nothing changes. Edits are insertions into the text, so
comments and layout are kept (`kek fmt -w` afterwards if a line got too
long).

- Without `-w` the changes are printed as a diff (`--- file`, `+++ file`,
  `@@ -L +L @@` and the old and new line); files are not changed. With
  `-w` the files are rewritten and `kek fix: N change(s)` is printed.
- A trait method's `&self` / `&T` is part of the trait's signature: it is
  reported (`not changed: it is part of a trait's signature`), not
  changed.
- What it cannot fix (a borrowed value stored in an owning place or
  returned as an owned type, which needs `clone()` or a `&T`) and other
  errors (type errors stop the fixing; refinement checks are skipped) are
  printed to stderr; the exit status is then 1.

## `kek search [-json] [-limit n] '<signature>' [file.kek]`

A Hoogle-style search by type, as described under 型検索 in
`docs/design.md`. It searches the functions, enum constructors and builtins
of the file. Without a file, it searches builtins only. Run it before
writing a new function to check whether one already exists. A function's
refinement predicates follow its signature
(`fn get(v: Vec<Int>, i: Int) -> Int where 0 <= i, i < v.len()`).

```
$ kek search 'String -> Option<Int>' bank.kek
parse_amount             fn parse_amount(s: String) -> Option<Int>  [exact] (bank.kek:13:4)
String.parse_int         fn String.parse_int(self) -> Option<Int>  [exact] (builtin)
```

The query syntax:

- Parameters can be written as `A, B -> R`, `(A, B) -> R` or `A -> B -> R`.
- `() -> R` means a function with no parameters.
- A bare type such as `Response` matches on the result type only.
- Lowercase names (`a`, `t`) and `_` are type variables. For example,
  `Option<a> -> a -> a` finds `Option.unwrap_or`.
- The receiver of a builtin method is its first parameter. `String.len`
  has the type `String -> Int`.
- Capability parameters are part of the signature. `Log` and `&Log` mean
  the same thing. `&Log, String -> ()` finds `Log.info`, and a query with
  no capabilities never returns a function that needs one.
- For other types `&T` and `&mut T` mean `T`: a reference only limits
  mutation (docs/mutability.md), so `&Item -> String` and `Item -> String`
  find the same functions. Signatures in the results show `&` and `&mut`
  as written.

The `match` field says how a result matched. Lower `score` values sort
first, and functions from the file rank above builtins.

| `match` | Meaning |
| --- | --- |
| `exact` | The parameters are identical and in the same order. |
| `reordered` | The parameter types match in a different order. |
| `unifies` | The types match after instantiating type variables. |
| `result` | Returned for a bare-type query: the result type matches. |

`-json` prints `{"query": ..., "matches": [{"name", "kind", "signature",
"match", "score", "pure", "caps", "builtin", "line", "col"}]}`.
`kind` is one of `function`, `method`, `static` or `constructor`.

## `kek cover [-json] [-lcov file] [-run re] [-seed n] [-j n] <file|dir>`

Line and branch coverage of the program's `#[test]` functions. The
compiler (`cover-build`, `compiler/cover_walk.kek`) inserts probes
`__cov_hit(k);` into the AST of every user function (not `#[test]`
functions) before type checking: at function entry, in both branches of
every `if` (an implicit empty `else` included), in every `match` arm, in
loop and closure bodies, and after every statement that can leave its block
early (`return`, `?`, `break`, `continue`). Each test runs in its own
process; the probes it hit are written at exit to the file named by
`KEK_COVER_OUT`. Functions marked `#[rare]` are reported separately and are
not counted. The exit status is 1 when a test fails or the line coverage is
below `[cover] min_line` of `kekkai.toml`.

```json
{
  "path": "testdata/cover/shapes.kek",
  "tests": [{"name": "areas", "status": "ok"}],
  "summary": {"lines": {"hit": 16, "total": 21, "percent": 76.2},
              "branches": {"hit": 6, "total": 9, "percent": 66.7},
              "functions": {"hit": 3, "total": 4, "percent": 75.0}},
  "min_line": null, "ok": true,
  "files": [{"file": "...", "lines": {...}, "branches": {...}, "uncovered": [13, 21]}],
  "functions": [{"name": "area", "file": "...", "line": 11, "rare": false, "hits": 1,
                 "lines": {...}, "branches": {...}, "uncovered": [13]}],
  "sites": [{"id": 3, "func": "area", "file": "...", "line": 13, "col": 29, "kind": "arm",
             "branch": 0, "decision_line": 12, "lines": [13], "tests": []}]
}
```

- `status` of a test: `ok`, `failed` or `trapped`. `hits` is the number of
  tests that entered the function.
- `kind` of a site: `fn`, `then`, `else`, `arm`, `loop`, `closure`, `seq`
  (after an early exit). Branch sites (`then`, `else`, `arm`) carry their
  index in the decision and the decision's line.
- `-lcov file` writes an lcov tracefile (`SF`, `FN`/`FNDA`, `BRDA`, `DA`,
  `LF`/`LH`); counts are numbers of tests.
- Results are cached per test in `.kek-cache/cover/`, keyed by the
  compiler, the probe table, the seed and the test's `trans` hash.

## `kek mutate [-json] [-run re] [-base <file|dir> | -diff <rev>] [-shard i/n] [-results f] [-merge f,...] [-j n] <file|dir>`

Mutation testing on the typed AST (`compiler/mutate_gen.kek`). Mutants:
arithmetic swaps on `Int`, comparison boundaries and negations, `&&`/`||`,
negated `if`/`while` conditions, dropped `!`/`-`, integer literals (n+1, 0),
flipped booleans, strings to `""`, deleted call/assignment statements, and
function results replaced by `0`, `""`, `None`, `Vec::new()` (negated for
`Bool`). `#[test]` and `#[rare]` functions are not mutated. Statement
deletions and result replacements are type-checked by group testing (all
at once, bisecting only a group that fails); the rejected ones are
**killed by types** (e.g. deleting `tx.commit()?;` breaks `Tx` linearity).
In functions that use refinement types, every mutant is checked this way:
one that breaks a proof (`i < n` → `i <= n` before `v[i]`) is killed by
types too.
The others are compiled into one mutant schema. A baseline run of every
test (one process) records which mutant sites it reaches and its probe
hits (`ticks`); each mutant then runs only the passing tests that reach
it, cheapest first, until one fails. One wasmtime process runs many
(mutant, test) pairs (`module --batch`), and only a trap or a timeout
starts a new one. A run's time limit is a probe budget, 10x the test's
ticks + 1000: deterministic, so timeouts are cached like other results
(`-timeout`, default 60s, is only a wall-clock backstop per process).

Statuses: `killed` (a test failed or trapped; `killed_by` names it),
`survived`, `timeout` (counted as detected), `no_coverage`,
`killed_by_types`, and `skipped` for the mutants of other shards (counted
in no score). The score is
(killed + timeout) / (killed + timeout + survived + no_coverage);
`covered_score` leaves out `no_coverage`. The exit status is 1 when the
score is below `[mutate] min_score` of `kekkai.toml`.

```json
{
  "path": "testdata/mutate/calc.kek", "base": null,
  "functions": ["clamp", "triangle", "is_adult", "deposit"],
  "tests": [{"name": "clamp_inside", "status": "ok", "ticks": 7}],
  "summary": {"generated": 30, "killed_by_types": 1, "killed": 18, "survived": 8,
              "timeout": 3, "no_coverage": 0, "skipped": 0, "score": 72.4, "covered_score": 72.4,
              "min_score": null, "ok": true},
  "mutants": [{"id": 1, "file": "...", "line": 8, "col": 10, "func": "clamp", "kind": "boundary",
               "description": "`<` → `<=`", "original": "if x < lo {", "mutated": "if x <= lo {",
               "status": "survived"}]
}
```

- `-base` / `-diff rev` mutate only the definitions whose `hash` differs
  from the base program (`-diff` extracts it with `git show` / `git
  archive`) or that are new; the others are listed as `unchanged`.
- The build is cached by the sources, and each (mutant, test) result by the
  mutant's identity (the function's `trans` hash, the mutant's position in
  the function and the mutation) and the test's `trans` hash, under
  `.kek-cache/mutate/`: an unchanged second run starts no test process.
- `-shard i/n` (or Bazel's `TEST_SHARD_INDEX` / `TEST_TOTAL_SHARDS`, which
  also touches `TEST_SHARD_STATUS_FILE`) runs the mutants whose index is
  i modulo n. `-results f` writes the run's raw baseline and results;
  `kek mutate -merge f0,f1,... <path>` reports the shards together, the
  same report as one unsharded run.

## `kek affected [-json] (-base <file|dir> | -diff <rev>) <file|dir>`

What a change affects, from the definition hashes: `changed` (own hash
differs), `added`, `removed`, `affected` (the `trans` hash differs: the
changed definitions and everything that reaches them), `tests` (the
affected `#[test]` functions) and `build` (whether the program hash, and so
`kek build`'s output, changed). `-diff rev` extracts the base program from
git. `kek test -affected <rev>` runs only those tests.

```json
{"path": "counter.kek", "base": "...", "build": true, "types": false,
 "changed": ["counter_key"], "added": [], "removed": [],
 "affected": ["counter_key", "visit", "handle", "key_format", "visits_are_counted"],
 "tests": ["key_format", "visits_are_counted"]}
```

## Action cache and remote cache

`kek build` and `kek test` key their outputs by the digest of their
inputs (the compiler stage, the arguments and the program's files) and
skip the compiler when it is unchanged; test results are keyed by each
test's `trans` hash. `KEK_REMOTE_CACHE=<url>` shares the entries over the
HTTP protocol of Bazel's remote cache (`GET`/`PUT <url>/ac/<sha256>`;
bazel-remote with `--disable_http_ac_validation`, any server accepting
PUT, or a `file://` directory). See [parallel-build.md](parallel-build.md).
## `kek assure plan|apply|check [-json] <file|dir>`

The guarantee ledger (design and policy reference: [assure.md](assure.md)).
For every function it records the guarantees the compiler establishes
(`effects`, `net.hosts`, `tx.linear`, `idempotent`, `tested`, and the
refinement proofs `refine.index_safe`, `refine.no_div_zero`,
`refine.no_overflow` with evidence `smt`) and the
assumptions written in the code (`#[allow(similar, ...)]`, `#[rare]`, and
one `pii.declassify` per call of `mask`/`hash`/`expose_unchecked`, with its
`call` and the reason/owner/expiry of the function's `#[declassify(...)]`)
in `kekkai.assure.lock`.
`plan` diffs the lock against the program and applies the policy in
`kekkai.toml`; `apply` rewrites the lock (`-yes` approves changes that need
review, and weakenings also need `-reason`, `-owner`, `-expires`); `check`
exits 1 when the lock is missing or stale, on policy violations and on
expired escape hatches. The launcher passes today's date as `-today`
(override it with `KEK_TODAY`).

`-json` (plan and check) prints:

```json
{
  "version": 1, "path": "app", "lock": "kekkai.assure.lock", "lock_found": true,
  "config": {"lock": "<policy hash in the lock>", "current": "<current policy hash>"},
  "today": "2026-10-04",
  "ok": false,
  "summary": {"definitions": 8, "changes": 1, "needs_review": 1, "auto_approved": 0,
              "violations": 0, "expired": 0, "config_errors": 0},
  "changes": [
    {"definition": "rate_key", "file": "app/rates.kek", "line": 19,
     "class": "weaken", "rule": "weaken", "guarantee": "effects", "evidence": "type",
     "from": "pure", "to": "Log", "added": ["Log"], "removed": [],
     "message": "weaken effects: +Log", "review": true, "escalate": "owner"}
  ],
  "violations": [
    {"definition": "fetch_rate", "file": "app/rates.kek", "line": 3, "rule": "allowed_hosts",
     "guarantee": "net.hosts", "items": ["rates.evil.example"],
     "message": "host not in allowed_hosts: rates.evil.example"}
  ],
  "expired": [
    {"definition": "rate_key", "file": "app/rates.kek", "line": 18, "source": "code",
     "kind": "#[allow(similar)]", "expires": "2027-03-31", "reason": "...", "owner": "shogo"}
  ],
  "config_errors": []
}
```

- `class` is one of `strengthen`, `change`, `weaken`, `assumption`,
  `added`, `removed`. `rule` is the policy key that decided the change:
  `strengthen`, `new_contract`, `change`, `allowed_host`, `added`,
  `removed`, `weaken`, `new_assumption`, `config`.
- `guarantee` names what changed: a guarantee, or `hash` (body only),
  `file`, `entry`, `async`, `name` (a rename: same definition hash),
  `assumption`, `definition` (added/removed) or `config` (the policy).
- `from`/`to` are display strings; `added`/`removed` are the items of a
  set guarantee (or the assumption: its kind, `pii.declassify(mask)` for a
  declassification). The message of a declassification gives its position
  (`new assumption pii.declassify mask() at 24:46`).
- `review` is true when the policy does not auto-approve the change;
  `escalate` is the role it is escalated to (approval then needs the
  metadata of `[assure] require`).
- `violations[].rule` is `forbid`, `allowed_hosts`, `expires`,
  `declassify_requires` (a declassifying function whose `#[declassify]`
  lacks a field of `[pii] declassify_requires`; `items` are the missing
  fields) or `max_declassify_per_module` (a file with more
  declassifications than `[pii] max_declassify_per_module`, reported at
  the first one over the limit); violations cannot be approved. `expired[].source` is `code` (an
  attribute) or `lock` (a waiver recorded by `apply`).
- `ok` is what `check` requires: a lock exists, no changes, no violations,
  nothing expired, no configuration errors.
## `kek similar [-json] [-threshold pct] [-all] [-tests] [-base path | -diff rev] <file|dir>`

Reports duplicate and similar definitions, as described under 類似コードの検出
in `docs/design.md`. Run it after writing a function to check that it does
not repeat an existing one, and in CI to enforce it. The program must
type-check. The exit status is 1 when there is at least one finding, 0
when there is none, and 2 for usage errors and programs that do not
check.

```
$ kek similar src
literals (86% similar):
  src/fees.kek:37:1  shipping_fee (Int) -> Int
  src/fees.kek:44:1  express_fee (Int) -> Int
    literal: 1000 at 38:17, 2000 at 45:17
    literal: 500 at 39:35, 800 at 46:35
  hint: differ only in constants: parameterize the literals 1000 vs 2000, 500 vs 800

1 finding (1 literals)
```

There are three kinds of findings. Each is computed on the definition
hashes of `compiler/defhash.kek` (`kek hash`):

| `kind` | Meaning | How |
| --- | --- | --- |
| `duplicate` | The same code up to the names of the function and its locals. | Equal `hash` (alpha-normalized). One finding per group. |
| `literals` | Only constants differ. | Equal `hash_lits` (literal values abstracted). `literals` lists each differing literal with its value and position in every member. |
| `semantic` | Different code, same behaviour (only with `-semantic`). | Pure, non-generic functions of the same signature whose parameter and result types are built from `Int`, `Bool`, `String`, `()`, `Vec`, `Option` and tuples are paired. `./kek` writes one property test per pair (`a(x) == b(x)`, 200 generated inputs) and runs it with `kek test` (timeout `KEK_TEST_TIMEOUT`, default 10s). Pairs that agree on every input are reported with `similarity` 100. This is evidence, not a proof. |
| `structural` | Near-misses. | MinHash (30 bands of 2 rows) over 3-label shingles of the preorder labels (literal values and local numbers abstracted) proposes candidate pairs; they are confirmed by the Zhang–Shasha tree edit distance. `similarity` = 100 × (1 − distance / larger node count). Pairs at or above the threshold are reported. |

`similarity` is an integer percent. For `literals` it counts each
differing literal as one relabel. Proving equivalence with an SMT solver
(for small linear-arithmetic functions, as in the design) is future work.

What is compared:

- By default, only definitions with the same signature, which includes
  the capabilities. `-all` compares across signatures.
- `#[test]` functions are skipped unless `-tests` is given. Implementations
  generated by `#[derive]`, the core library and the prelude are always
  skipped. So are definitions with fewer than `min_nodes` (default 16)
  syntax tree labels.
- `structural` compares one representative of each `hash_lits` class.
  The other members are already reported as `duplicate` or `literals`.

Options:

- `-threshold pct` sets the minimum similarity of `structural` findings
  (1–100, default 80).
- `-min_nodes n` overrides `min_nodes`.
- `-exhaustive` compares every pair instead of the MinHash candidates.
  It is slower, and useful for checking recall.
- `-base <file|dir>` loads a base program. A definition whose `hash`
  appears in the base is old. Only findings with at least one new member
  are reported. Old members are marked `[base]` in the text output and
  `"new": false` in JSON.
- `-diff <rev>` is handled by `./kek`. It extracts the same path at the
  git revision (`git archive`) into a temporary directory and passes it as
  `-base`. If the path did not exist at `rev`, every definition is new.
  The JSON `base` is then `git:<rev>`.

`./kekkai.toml` can set defaults in a `[similar]` section: `threshold`,
`min_nodes`, and `max_nodes` (default 400). Trees larger than
`max_nodes` are compared by the edit distance of their label sequences.
That distance is a lower bound of the tree edit distance, and such
findings have `"approx": true`. Flags override the file.

A definition with `#[allow(similar, reason = "...", owner = "...",
expires = "YYYY-MM-DD")]` suppresses the findings that involve it. They
are listed under `allowed` with `allowed_by`. In a group, the other
members are still reported when two or more of them remain.

`-json` prints:

```json
{"path": "src", "base": "git:origin/main", "threshold": 80, "min_nodes": 16, "all": false,
 "findings": [
   {"kind": "structural", "similarity": 87,
    "members": [{"name": "describe", "file": "src/a.kek", "line": 52, "col": 1,
                 "sig": "(Point) -> String", "hash": "f99d...", "nodes": 36, "new": true},
                {"name": "describe_point", "...": "..."}],
    "distance": 5, "approx": false,
    "hint": "similar structure: extract the common part of `describe` and `describe_point` into one parameterized definition"},
   {"kind": "literals", "similarity": 86, "members": ["..."],
    "literals": [{"values": ["1000", "2000"],
                  "positions": [{"file": "src/a.kek", "line": 38, "col": 17}, {"...": "..."}]}],
    "hint": "..."}
 ],
 "allowed": [
   {"kind": "duplicate", "...": "...",
    "allowed_by": [{"name": "count_even", "reason": "kept apart on purpose",
                    "owner": "billing", "expires": "2027-01-01"}]}
 ]}
```

- `base` is present only with `-base` or `-diff`.
- `distance` and `approx` appear only on `structural` findings.
- `literals` appears only on `literals` findings, with values in member
  order. String values are quoted.
- Findings are sorted by kind (`duplicate`, `literals`, `structural`), then
  by descending similarity, then by member position. Members are sorted by
  file, line and column.
## `kek test -json [flags] <file|dir>`

Runs the `#[test]` functions like `kek test` and prints the results as one
line of JSON instead of the report. The exit status is unchanged: 0 when
every test passes, 1 when one fails, 2 for a usage error; diagnostics of
the program still go to stderr.

```json
{"file": "props.kek", "passed": 1, "failed": 1, "cached": 1, "tests": [
  {"name": "reverse_twice_is_identity", "status": "ok", "cached": true,
   "pure": true, "caps": [], "hash": "8f2e…", "ms": 1.3, "output": [],
   "counterexample": null},
  {"name": "small_numbers", "status": "failed", "cached": false,
   "pure": true, "caps": [], "hash": "41c0…", "ms": 0.7,
   "output": ["Err: too big: 101", "counterexample: small_numbers(n = 101)",
              "found: case 33 of 100, shrunk in 60 steps from small_numbers(n = 9223372036854775807)",
              "reproduce: kek test -seed 0 -run '^small_numbers$'"],
   "counterexample": "small_numbers(n = 101)"}
]}
```

- Tests are listed in declaration order (after `-run` selection).
- `status` is `ok`, `failed` (returned `false` or `Err`) or `trapped` (the
  process trapped, e.g. on call stack exhaustion).
- `cached` is true when the result was replayed from the test result cache
  (see below); `ms` is then the time of the run that was cached. `ms` is
  `null` for a trapped test.
- `pure` means the test takes no capabilities; `caps` lists the mock
  capabilities it receives, in parameter order.
- `hash` is the test's `trans` definition hash (`kek hash`): it changes
  when the test or anything it can reach (functions, methods, type
  declarations) changes, and not for comments or formatting.
- `output` holds the report lines below the test's result line (failure
  reason, counterexample, mock log lines and outbox), without indentation.
- `counterexample` is the shrunk input of a failed property test, rendered
  like Rust's `Debug` (`name(param = value, ...)`), else `null`.

Other flags are those of `kek test`: `-run re`, `-seed n`, `-cases n`,
`-j n`, `-no-cache`, `-clock ms`, `-net f.json`, `-db f.json`.

### Property tests

A test that takes parameters other than capabilities is a property:
`kek test` generates the arguments. Generatable types are `Int`, `Bool`,
`String`, `()`, `Vec<T>`, `Option<T>`, `Result<T, E>`, tuples, and the
program's own structs and enums (generic and recursive ones included)
whose fields are generatable; functions, `&Tx`, opaque host types and
library types such as `HashMap` are rejected by the type checker.

- A property runs on 100 cases (`-cases n`; `#[test(cases = N)]` on the
  test overrides it). Inputs grow with the case number and are biased to
  edge cases (0, ±1, the extremes of `Int`, empty strings and vectors,
  non-ASCII text).
- Inputs come from a PRNG seeded with `-seed` (`KEK_TEST_SEED`) and the
  test name, so a run is reproducible.
- A failure is shrunk greedily (integers toward 0, strings and vectors by
  removing chunks and shrinking elements, `Some` toward `None`, structs and
  enum fields one at a time, recursive enums toward their sub-values),
  within a budget of 2000 runs.
- Every run gets fresh mocks (an empty or `-db`-seeded `&Db`, no log lines,
  the same `&Random` sequence), so a counterexample reproduces on its own
  and the reported log lines are those of the counterexample's run.
- A trap ends the test's process: the report shows the trap and the last
  input that was about to run (`last input: case 3: deep(n = 1)`); traps
  are not shrunk.

- A parameter whose type is a refined alias (`p: Port` with `type Port =
  Int where 1024 <= self && self < 65536`) gets only values that satisfy
  the predicate: a value is generated again until it does, up to 1000
  times (an `Int` alias also draws from the bounds `self op k` its
  predicate states); a case where none does is skipped. Shrink candidates
  that break the predicate are skipped too.

### Test result cache

Every mock is deterministic, so a test's result is a function of the
compiler (the launcher's stage hash), the test's `trans` hash, `-seed`,
`-clock`, the contents of the `-net`/`-db` files and, for a property, its
number of cases. Results (status and report lines) are stored under
`.kek-cache/test/<options hash>/<trans>-<cases>-<name>` (written to a
temporary file and renamed into place). `kek test` lists the tests with
their hashes (`test-build -list`, which writes `tests.json` and
`tests.tsv`), and builds and runs only the uncached ones: when nothing
changed, nothing is compiled, and after an edit exactly the tests that can
reach the edited definitions run again. A cached result is reported with
`(cached)` (failures are replayed too). `-no-cache` or `KEK_TEST_CACHE=0`
runs every test.

Tests run in separate processes, `-j n` at a time (default: the number of
CPUs); the report keeps declaration order.

## `kek smt [-repeat n] <file>` (hidden)

Runs the refinement-type solver (`compiler/smt.kek`) on problems written
in a small text syntax (docs/refinement.md, "ソルバの実装") and prints one
line per problem: `valid [h1, h2]` (the unsat core), `invalid x=1 y=2` (a
counterexample) or `unknown: reason`. `-random n [-seed s]` checks n
random formulas against brute force and `-bench n` solves n random
problems (for timing); both are used by `tests/run.sh smt`. It is not in
the usage text: it is a test hook for the solver, not a user command.
