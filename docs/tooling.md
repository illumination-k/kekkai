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
