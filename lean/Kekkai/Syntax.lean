/-!
# Kekkai core calculus: syntax

A small, first-order core of Kekkai.

* Value variables use de Bruijn indices into a value context `Γ`.
* Capability variables use de Bruijn indices into a *separate* capability
  context `Δ`. Capabilities are **second-class**: there is no expression that
  produces a capability, so they can only be introduced as function parameters
  or by the `transaction` binder. In particular they cannot be returned, stored
  in data, or bound by `let`.
* Calls are in A-normal form: value arguments are value variables and capability
  arguments are capability variables. The surface call `f(a + 1, b; db, log)`
  desugars into `let`s followed by a call.
-/

namespace Kekkai

/-- Value (first-class) types. There is deliberately no capability type here:
capabilities are second-class and never have a value type. -/
inductive Ty where
  | unit
  | bool
  | int
  /-- arrays of integers; the length is fixed once the array is built, and the
  refinement layer reasons about it through the atom `len i` -/
  | arr
  deriving DecidableEq, Repr

/-- Capability kinds.
* `log` — revocable / harmless effect (logging).
* `net` — **irrevocable** effect (network); forbidden inside transactions.
* `db`  — an abstract transactional store (backend chosen by a runtime adapter); can begin a transaction.
* `tx`  — a transaction handle; **linear** in the owning `transaction` block. -/
inductive CapKind where
  | log
  | net
  | db
  | tx
  deriving DecidableEq, Repr

/-- Runtime values. Note that there is no constructor carrying a capability:
values can never contain capabilities (see `Kekkai.NoLeak`). -/
inductive Val where
  | unit
  | bool (b : Bool)
  | int (i : Int)
  /-- an array of integers (immutable, fixed length) -/
  | arr (xs : List Int)
  deriving DecidableEq, Repr

/-- Operations on an open transaction of the abstract transactional store
(the store may be Durable Objects storage, D1, a distributed KV, in-memory, …;
the calculus does not care). None of them consumes the transaction.
* `get(key)` : `Int`
* `put(key, v)`, `delete(key)` : `Unit`
* `outbox(url, body)` : `Unit` — enqueue an irrevocable effect to be performed
  only after a successful commit (the transactional outbox). -/
inductive StoreOp where
  | get
  | put
  | delete
  | outbox
  deriving DecidableEq, Repr

/-- Binary operators on integers. Integers are mathematical integers; `add`,
`sub`, `mul` and `div` fault with `overflow` when the result leaves the 64-bit
range, and `div`/`mod` fault with `divByZero` on a zero divisor (see
`Kekkai.arith`). -/
inductive BinOp where
  | add
  | sub
  | lt
  | eq
  | mul
  /-- truncating division (`Int.tdiv`, like `i64.div_s`) -/
  | div
  /-- remainder with the sign of the dividend (`Int.tmod`, like `i64.rem_s`) -/
  | mod
  | le
  | ne
  deriving DecidableEq, Repr

/-! ## The predicate language of refinements

Quantifier-free integer arithmetic over *atoms*: the integer value of a value
variable and the length of an array variable (both de Bruijn indices into the
value context). Booleans are seen as `0`/`1`. This is the language of the
compiler's `SmtF` (`compiler/smt.kek`); the solver decides its linear fragment
(`mul` with a constant factor). Its meaning is given in `Kekkai.Pred`. -/

/-- Atoms of the predicate language. -/
inductive Atom where
  /-- the integer value of value variable `i` (`0`/`1` for a `Bool`) -/
  | var (i : Nat)
  /-- the length of array variable `i` -/
  | len (i : Nat)
  deriving DecidableEq, Repr

/-- Integer terms. -/
inductive Term where
  | const (k : Int)
  | atom (a : Atom)
  | add (t u : Term)
  | sub (t u : Term)
  | mul (t u : Term)
  deriving DecidableEq, Repr

/-- Predicates. -/
inductive Pred where
  | tt
  | ff
  | le (t u : Term)
  | lt (t u : Term)
  | eq (t u : Term)
  | not (p : Pred)
  | and (p q : Pred)
  | or (p q : Pred)
  deriving DecidableEq, Repr

/-- Expressions of the core calculus. -/
inductive Expr where
  /-- literal -/
  | val (v : Val)
  /-- value variable (de Bruijn) -/
  | var (i : Nat)
  /-- `let x = e₁ in e₂`; `e₂` lives under one more value binder -/
  | let_ (e₁ e₂ : Expr)
  /-- `if c then t else e` -/
  | ite (c t e : Expr)
  /-- integer arithmetic / comparison -/
  | bin (op : BinOp) (a b : Expr)
  /-- `f(args; caps)` — top-level function call. `args` are value variables,
  `caps` are capability variables. -/
  | call (f : Nat) (args : List Nat) (caps : List Nat)
  /-- `log(c, e)` : `Unit`, requires `c : Log` -/
  | log (c : Nat) (e : Expr)
  /-- `fetch(c, e)` : `Int`, requires `c : Net` (irrevocable) -/
  | fetch (c : Nat) (e : Expr)
  /-- `transaction(d) { tx => body }` — requires `d : Db`. Inside `body`
  capability variable `0` is the fresh, linear `tx`, and the outer capability
  context is masked (only `Log` survives; `Net`, `Db`, `Tx` are hidden). -/
  | transaction (d : Nat) (body : Expr)
  /-- `tx.op(args)` — a non-consuming store operation on transaction `c`;
  `args` are value variables (A-normal form). -/
  | store (op : StoreOp) (c : Nat) (args : List Nat)
  /-- `commit(tx)` : `Bool` — consumes the transaction. Committing may fail
  (e.g. an optimistic-concurrency conflict): the result is `true` on success and
  `false` on failure; in **both** cases the transaction is ended. -/
  | commit (c : Nat)
  /-- `rollback(tx)` — consumes the transaction -/
  | rollback (c : Nat)
  /-- early exit (models `?` on an `Err`). Inside a `transaction` block whose
  `tx` is still alive, aborting automatically rolls the transaction back. -/
  | abort
  /-- `a.len()` : `Int` for an array variable `a` -/
  | len (a : Nat)
  /-- `a[i]` : `Int` for an array variable `a` and an integer variable `i`;
  faults with `outOfBounds` unless `0 <= i < a.len()` -/
  | index (a i : Nat)
  deriving Repr

/-- A top-level function: value parameters, capability parameters, return type
and body. A function with `caps = []` is pure. Variable `i` of the body refers to
`params[i]`, capability variable `j` to `caps[j]`.

`pre` (over the parameters: atom index `i` is `params[i]`) and `post` (over
`result :: params`: index `0` is the result, `i + 1` is `params[i]`) are the
refinement contract (`where` clauses); both default to `tt`. They are only used by
the refinement layer (`Kekkai.Refine`), not by `HasType` or `eval`. -/
structure FunDef where
  params : List Ty
  caps : List CapKind
  ret : Ty
  body : Expr
  pre : Pred := .tt
  post : Pred := .tt
  deriving Repr

/-- A program is a list of top-level functions, called by index. -/
abbrev Prog := List FunDef

end Kekkai
