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

## `kek mutate [-json] [-run re] [-base <file|dir> | -diff <rev>] [-timeout 2s] [-j n] <file|dir>`

Mutation testing on the typed AST (`compiler/mutate_gen.kek`). Mutants:
arithmetic swaps on `Int`, comparison boundaries and negations, `&&`/`||`,
negated `if`/`while` conditions, dropped `!`/`-`, integer literals (n+1, 0),
flipped booleans, strings to `""`, deleted call/assignment statements, and
function results replaced by `0`, `""`, `None`, `Vec::new()` (negated for
`Bool`). `#[test]` and `#[rare]` functions are not mutated. Statement
deletions and result replacements are type-checked one by one; the
rejected ones are **killed by types** (e.g. deleting `tx.commit()?;` breaks
`Tx` linearity). The others are compiled into one mutant schema, where the
active mutant is chosen by `KEK_MUTANT`. A baseline run of every test
records which mutant sites it reaches; each mutant then runs only the
passing tests that reach it, until one fails.

Statuses: `killed` (a test failed or trapped; `killed_by` names it),
`survived`, `timeout` (counted as detected; default limit 1s + 10x the
slowest test), `no_coverage`, `killed_by_types`. The score is
(killed + timeout) / (killed + timeout + survived + no_coverage);
`covered_score` leaves out `no_coverage`. The exit status is 1 when the
score is below `[mutate] min_score` of `kekkai.toml`.

```json
{
  "path": "testdata/mutate/calc.kek", "base": null,
  "functions": ["clamp", "triangle", "is_adult", "deposit"],
  "tests": [{"name": "clamp_inside", "status": "ok", "ms": 0}],
  "summary": {"generated": 30, "killed_by_types": 1, "killed": 18, "survived": 8,
              "timeout": 3, "no_coverage": 0, "score": 72.4, "covered_score": 72.4,
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
