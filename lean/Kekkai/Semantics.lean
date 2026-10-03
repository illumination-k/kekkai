import Kekkai.Syntax
import Kekkai.Basic

/-!
# Operational semantics

An executable, fuel-based big-step interpreter producing a *trace* of effect
events. It is meant to be the reference interpreter for differential testing.

* Runtime capabilities are just identifiers. Provided capabilities (`Log`, `Net`,
  `Db`) are `RCap.res id`; transaction handles created by `transaction` are
  `RCap.tx id` with a fresh `id` drawn from a counter.
* The interpreter distinguishes `timeout` (out of fuel), `stuck` (a dynamic type
  error: the situation that the type system rules out) and `done`.
* `done` carries an `Outcome`: a value, or `err` for an `abort` that propagated.
* The state `St` holds the fresh-id counter and a dynamic flag `live` saying
  whether the innermost transaction is still open. The interpreter is defensive:
  store operations / `commit` / `rollback` on a closed transaction, or a transaction body that
  returns normally without consuming its `tx`, are `stuck`.
* External answers (`fetch` responses, results of `get`, whether a `commit`
  succeeds) come from an arbitrary
  `Oracle`; all theorems are stated for every oracle.
-/

namespace Kekkai

/-- Runtime capability: a provided resource capability or a transaction handle. -/
inductive RCap where
  | res (id : Nat)
  | tx (id : Nat)
  deriving DecidableEq, Repr

/-- Observable effect events. -/
inductive Event where
  | log (cap : Nat) (msg : Val)
  | fetch (cap : Nat) (url : Val)
  | txBegin (db : Nat) (tx : Nat)
  | txOp (tx : Nat) (op : StoreOp) (args : List Val)
  /-- successful commit -/
  | txCommit (tx : Nat)
  /-- explicit rollback, abort-induced rollback, or a commit that failed (the
  transaction ends without its writes taking effect) -/
  | txRollback (tx : Nat)
  deriving DecidableEq, Repr

/-- Interpreter state: next fresh transaction id, and whether the innermost
transaction is still open. -/
structure St where
  next : Nat
  live : Bool
  deriving DecidableEq, Repr

/-- Outcome of a terminating evaluation. -/
inductive Outcome where
  | ok (v : Val)
  | err
  deriving DecidableEq, Repr

/-- Result of running the interpreter. -/
inductive Result where
  /-- out of fuel -/
  | timeout
  /-- dynamic type error -/
  | stuck
  /-- terminated with outcome `o`, final state `σ` and trace `tr` -/
  | done (o : Outcome) (σ : St) (tr : List Event)
  deriving DecidableEq, Repr

/-- Answers of the outside world. -/
structure Oracle where
  fetch : Nat → Val → Int
  /-- answer of `get` on transaction `t` with arguments `args` -/
  get : Nat → List Val → Int
  /-- does committing transaction `t` succeed? -/
  commit : Nat → Bool

/-- Prepend trace events to a result. -/
def Result.prepend (tr : List Event) : Result → Result
  | .done o σ tr' => .done o σ (tr ++ tr')
  | r => r

/-- Sequencing: continue with `k` on a value, propagate everything else
(`timeout`, `stuck`, `err`). -/
def Result.bind (r : Result) (k : Val → St → Result) : Result :=
  match r with
  | .done (.ok v) σ tr => (k v σ).prepend tr
  | r => r

/-- Primitive operators. -/
def evalBin : BinOp → Val → Val → Option Val
  | .add, .int a, .int b => some (.int (a + b))
  | .sub, .int a, .int b => some (.int (a - b))
  | .lt, .int a, .int b => some (.bool (decide (a < b)))
  | .eq, .int a, .int b => some (.bool (decide (a = b)))
  | _, _, _ => none

/-- Result value of a store operation. -/
def storeResult (O : Oracle) : StoreOp → Nat → List Val → Val
  | .get, t, vs => .int (O.get t vs)
  | _, _, _ => .unit

/-- The interpreter. `eval O P n env ρ σ e` evaluates `e` with fuel `n`, value
environment `env` (de Bruijn) and capability environment `ρ` (de Bruijn). -/
def eval (O : Oracle) (P : Prog) : Nat → List Val → List RCap → St → Expr → Result
  | 0, _, _, _, _ => .timeout
  | n+1, env, ρ, σ, e =>
    match e with
    | .val v => .done (.ok v) σ []
    | .var i =>
      match env[i]? with
      | some v => .done (.ok v) σ []
      | none => .stuck
    | .let_ e₁ e₂ =>
      (eval O P n env ρ σ e₁).bind fun v σ₁ => eval O P n (v :: env) ρ σ₁ e₂
    | .ite c t f =>
      (eval O P n env ρ σ c).bind fun v σ₁ =>
        match v with
        | .bool true => eval O P n env ρ σ₁ t
        | .bool false => eval O P n env ρ σ₁ f
        | _ => .stuck
    | .bin op a b =>
      (eval O P n env ρ σ a).bind fun v₁ σ₁ =>
        (eval O P n env ρ σ₁ b).bind fun v₂ σ₂ =>
          match evalBin op v₁ v₂ with
          | some v => .done (.ok v) σ₂ []
          | none => .stuck
    | .call f args cs =>
      match P[f]?, lookupAll env args, lookupAll ρ cs with
      | some fd, some vs, some rs => eval O P n vs rs σ fd.body
      | _, _, _ => .stuck
    | .log c e =>
      (eval O P n env ρ σ e).bind fun v σ₁ =>
        match ρ[c]? with
        | some (.res id) => .done (.ok .unit) σ₁ [.log id v]
        | _ => .stuck
    | .fetch c e =>
      (eval O P n env ρ σ e).bind fun v σ₁ =>
        match ρ[c]? with
        | some (.res id) => .done (.ok (.int (O.fetch id v))) σ₁ [.fetch id v]
        | _ => .stuck
    | .transaction d body =>
      match ρ[d]? with
      | some (.res id) =>
        match eval O P n env (.tx σ.next :: ρ) ⟨σ.next + 1, true⟩ body with
        | .done (.ok v) σ₂ tr =>
          if σ₂.live then .stuck
          else .done (.ok v) ⟨σ₂.next, σ.live⟩ (.txBegin id σ.next :: tr)
        | .done .err σ₂ tr =>
          .done .err ⟨σ₂.next, σ.live⟩
            (.txBegin id σ.next :: (tr ++ if σ₂.live then [.txRollback σ.next] else []))
        | r => r
      | _ => .stuck
    | .store op c args =>
      match ρ[c]?, lookupAll env args with
      | some (.tx t), some vs =>
        if σ.live then .done (.ok (storeResult O op t vs)) σ [.txOp t op vs] else .stuck
      | _, _ => .stuck
    | .commit c =>
      match ρ[c]? with
      | some (.tx t) =>
        if σ.live then
          if O.commit t then .done (.ok (.bool true)) ⟨σ.next, false⟩ [.txCommit t]
          else .done (.ok (.bool false)) ⟨σ.next, false⟩ [.txRollback t]
        else .stuck
      | _ => .stuck
    | .rollback c =>
      match ρ[c]? with
      | some (.tx t) =>
        if σ.live then .done (.ok .unit) ⟨σ.next, false⟩ [.txRollback t] else .stuck
      | _ => .stuck
    | .abort => .done .err σ []

/-- Initial state for running an entry point. -/
def St.init : St := ⟨0, false⟩

/-- Run top-level function `f` on argument values `vs` with provided resource
capabilities `caps` (identifiers). -/
def runFun (O : Oracle) (P : Prog) (n : Nat) (f : Nat) (vs : List Val) (caps : List Nat) :
    Result :=
  match P[f]? with
  | some fd => eval O P n vs (caps.map .res) St.init fd.body
  | none => .stuck

/-! ## Basic facts about `bind` / `prepend` -/

theorem Result.prepend_ne_stuck {tr : List Event} {r : Result} (h : r ≠ .stuck) :
    r.prepend tr ≠ .stuck := by
  cases r <;> simp_all [Result.prepend]

theorem Result.prepend_eq_done {tr : List Event} {r : Result} {o σ tr'} :
    r.prepend tr = .done o σ tr' ↔ ∃ tr₂, r = .done o σ tr₂ ∧ tr' = tr ++ tr₂ := by
  cases r with
  | done o' σ'' tr'' =>
    simp only [Result.prepend, Result.done.injEq]
    constructor
    · rintro ⟨rfl, rfl, rfl⟩; exact ⟨_, ⟨rfl, rfl, rfl⟩, rfl⟩
    · rintro ⟨_, ⟨rfl, rfl, rfl⟩, rfl⟩; exact ⟨rfl, rfl, rfl⟩
  | _ => simp [Result.prepend]

theorem Result.bind_ne_stuck {r : Result} {k : Val → St → Result} (h : r ≠ .stuck)
    (hk : ∀ v σ tr, r = .done (.ok v) σ tr → k v σ ≠ .stuck) : r.bind k ≠ .stuck := by
  unfold Result.bind
  split
  · exact Result.prepend_ne_stuck (hk _ _ _ rfl)
  · exact h

theorem Result.bind_eq_done {r : Result} {k : Val → St → Result} {o σ' tr} :
    r.bind k = .done o σ' tr ↔
      (r = .done .err σ' tr ∧ o = .err) ∨
      ∃ v σ₁ tr₁ tr₂, r = .done (.ok v) σ₁ tr₁ ∧ k v σ₁ = .done o σ' tr₂ ∧ tr = tr₁ ++ tr₂ := by
  unfold Result.bind
  split
  · rename_i v σ₁ tr₁
    rw [Result.prepend_eq_done]
    constructor
    · rintro ⟨tr₂, h1, h2⟩; exact .inr ⟨v, σ₁, tr₁, tr₂, rfl, h1, h2⟩
    · rintro (⟨h, _⟩ | ⟨v', σ₁', tr₁', tr₂, h1, h2, h3⟩)
      · cases h
      · cases h1; exact ⟨tr₂, h2, h3⟩
  · rename_i r hr
    constructor
    · intro h; subst h
      cases o with
      | ok v => exact absurd rfl (hr v σ' tr)
      | err => exact .inl ⟨rfl, rfl⟩
    · rintro (⟨h, rfl⟩ | ⟨v, σ₁, tr₁, tr₂, h1, _, _⟩)
      · exact h
      · exact absurd h1 (hr v σ₁ tr₁)

end Kekkai
