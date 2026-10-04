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
  - `lint`: currently a single warning, for a capability that a function
    receives but never uses.

## `kek caps -json <file.kek>`

Prints one entry per function, in declaration order. The file must type-check.

```json
{"file": "bank.kek", "functions": [
  {"name": "transfer", "signature": "fn transfer(db: &Db, log: &Log, ...) -> Result<Int, TransferError>",
   "line": 25, "col": 4, "handler": false, "pure": false, "async": true,
   "caps": [{"name": "db", "type": "&Db", "used": true}, {"name": "log", "type": "&Log", "used": true}],
   "direct_effects": ["db.transaction", "log.info", "tx.commit", "..."],
   "unused_caps": [], "calls": ["balance_key", "read_balance"]}
]}
```

Capabilities are second-class and there is no ambient authority, so `caps` lists
every effect that the function and its callees can perform. A function is
`pure` when `caps` is empty. `async` means the function can reach an
asynchronous builtin, so it is compiled to a resumable state machine.

## `kek search [-json] [-limit n] '<signature>' [file.kek]`

A Hoogle-style search by type, as described under 型検索 in
`docs/design.md`. It searches the functions, enum constructors and builtins
of the file. Without a file, it searches builtins only. Run it before
writing a new function to check whether one already exists.

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
