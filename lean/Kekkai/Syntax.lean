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

/-- Binary operators on integers. -/
inductive BinOp where
  | add
  | sub
  | lt
  | eq
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
  deriving Repr

/-- A top-level function: value parameters, capability parameters, return type
and body. A function with `caps = []` is pure. Variable `i` of the body refers to
`params[i]`, capability variable `j` to `caps[j]`. -/
structure FunDef where
  params : List Ty
  caps : List CapKind
  ret : Ty
  body : Expr
  deriving Repr

/-- A program is a list of top-level functions, called by index. -/
abbrev Prog := List FunDef

end Kekkai
