import Kekkai.Typing
import Kekkai.Semantics

/-!
# Theorem 5: capability non-leakage

Capabilities are second-class. This is enforced *by construction* at three
levels; we make each explicit.

1. **Types.** Surface types (`SType`) include capability types, but the typing
   judgment assigns expressions only value types `Ty`, and function return types
   are `Ty`s. So no expression and no function can have a capability type: a
   capability can never be returned, stored, or `let`-bound
   (`no_expr_has_cap_type`, `no_fun_returns_cap`).
2. **Values.** `Val` has no constructor carrying a capability, so runtime values
   never contain capabilities (`Val.capIds_nil` — trivial by construction).
3. **Semantics (the non-trivial part).** Results do not even *depend* on which
   capabilities were passed in: renaming the provided capability ids renames the
   trace accordingly and leaves the outcome (in particular the returned value)
   unchanged (`eval_rename`, `result_independent_of_caps`). Hence no information
   about capabilities can leak into values.
-/

namespace Kekkai

/-- Surface types: value types and (second-class) capability types. Capability
types may only appear as parameter types (`FunDef.caps`). -/
inductive SType where
  | val (τ : Ty)
  | cap (k : CapKind)

/-- The type an expression is given by the judgment, seen as a surface type. -/
theorem no_expr_has_cap_type {P Γ Δ s e τ s'} (_ : HasType P Γ Δ s e τ s') :
    ∀ k, SType.val τ ≠ SType.cap k := fun _ => nofun

/-- No function returns a capability. -/
theorem no_fun_returns_cap (fd : FunDef) : ∀ k, SType.val fd.ret ≠ SType.cap k :=
  fun _ => nofun

/-- Capability identifiers contained in a value: always none. -/
def Val.capIds : Val → List RCap
  | .unit => []
  | .bool _ => []
  | .int _ => []
  | .arr _ => []

/-- Values never contain capabilities (trivial by construction of `Val`). -/
theorem Val.capIds_nil (v : Val) : v.capIds = [] := by cases v <;> rfl

/-! ## Renaming of provided capabilities -/

/-- Rename provided resource capabilities (transaction handles are internal and
unaffected). -/
def RCap.rename (π : Nat → Nat) : RCap → RCap
  | .res id => .res (π id)
  | .tx t => .tx t

/-- Rename the resource capabilities mentioned in an event. -/
def Event.rename (π : Nat → Nat) : Event → Event
  | .log c v => .log (π c) v
  | .fetch c v => .fetch (π c) v
  | .txBegin d t => .txBegin (π d) t
  | ev => ev

/-- Rename the trace of a result. -/
def Result.rename (π : Nat → Nat) : Result → Result
  | .done o σ tr => .done o σ (tr.map (Event.rename π))
  | r => r

theorem lookupAll_map {α β : Type} (f : α → β) (l : List α) :
    ∀ is : List Nat, lookupAll (l.map f) is = (lookupAll l is).map (List.map f)
  | [] => rfl
  | i :: is => by
      simp only [lookupAll, List.getElem?_map, lookupAll_map f l is]
      cases l[i]? <;> cases lookupAll l is <;> rfl

theorem Result.rename_prepend (π : Nat → Nat) (tr : List Event) (r : Result) :
    (r.prepend tr).rename π = (r.rename π).prepend (tr.map (Event.rename π)) := by
  cases r <;> simp [Result.prepend, Result.rename]

theorem Result.bind_rename {π : Nat → Nat} {r : Result} {k k' : Val → St → Result}
    (hk : ∀ v σ, k' v σ = (k v σ).rename π) :
    (r.rename π).bind k' = (r.bind k).rename π := by
  cases r with
  | done o σ tr =>
    cases o with
    | ok v =>
      simp only [Result.bind, Result.rename, hk]
      exact (Result.rename_prepend π tr (k v σ)).symm
    | err => rfl
  | _ => rfl

theorem binResult_rename {π : Nat → Nat} {op : BinOp} {v₁ v₂ : Val} {σ : St} :
    (binResult op v₁ v₂ σ).rename π = binResult op v₁ v₂ σ := by
  unfold binResult
  split
  · split <;> rfl
  · rfl

theorem indexResult_rename {π : Nat → Nat} {xs : List Int} {k : Int} {σ : St} :
    (indexResult xs k σ).rename π = indexResult xs k σ := by
  unfold indexResult
  split <;> rfl

set_option linter.unusedSimpArgs false in
/-- **Equivariance of evaluation.** Renaming the provided capabilities by `π`
(with an oracle that answers renamed `fetch`es the same way) renames the trace by
`π` and changes nothing else. -/
theorem eval_rename (O O' : Oracle) (P : Prog) (π : Nat → Nat)
    (hfetch : ∀ id v, O'.fetch (π id) v = O.fetch id v) (hget : O'.get = O.get)
    (hcommit : O'.commit = O.commit) :
    ∀ (n : Nat) (env : List Val) (ρ : List RCap) (σ : St) (e : Expr),
      eval O' P n env (ρ.map (RCap.rename π)) σ e = (eval O P n env ρ σ e).rename π := by
  have hstore : ∀ op t vs, storeResult O' op t vs = storeResult O op t vs := by
    intro op t vs; cases op <;> simp [storeResult, hget]
  intro n
  induction n with
  | zero => intros; rfl
  | succ n ih =>
    intro env ρ σ e
    cases e with
    | val v => rfl
    | var i =>
      simp only [eval]; cases env[i]? <;> rfl
    | let_ e₁ e₂ =>
      simp only [eval]
      rw [ih]
      exact Result.bind_rename fun v σ₁ => ih _ _ _ _
    | ite c t f =>
      simp only [eval]
      rw [ih]
      refine Result.bind_rename fun v σ₁ => ?_
      split <;> simp [ih, Result.rename]
    | bin op a b =>
      simp only [eval]
      rw [ih]
      refine Result.bind_rename fun v σ₁ => ?_
      rw [ih]
      refine Result.bind_rename fun v₂ σ₂ => ?_
      exact binResult_rename.symm
    | call f args cs =>
      simp only [eval, lookupAll_map]
      cases P[f]? <;> cases lookupAll env args <;> cases lookupAll ρ cs <;>
        simp [Result.rename, ih]
    | log c e =>
      simp only [eval]
      rw [ih]
      refine Result.bind_rename fun v σ₁ => ?_
      simp only [List.getElem?_map]
      cases ρ[c]? with
      | none => rfl
      | some r => cases r <;> simp [RCap.rename, Result.rename, Event.rename]
    | fetch c e =>
      simp only [eval]
      rw [ih]
      refine Result.bind_rename fun v σ₁ => ?_
      simp only [List.getElem?_map]
      cases ρ[c]? with
      | none => rfl
      | some r => cases r <;> simp [RCap.rename, Result.rename, Event.rename, hfetch]
    | transaction d body =>
      simp only [eval, List.getElem?_map]
      cases hd : ρ[d]? with
      | none => rfl
      | some r =>
        cases r with
        | tx t => rfl
        | res id =>
          simp only [Option.map_some, RCap.rename]
          have := ih env (.tx σ.next :: ρ) ⟨σ.next + 1, true⟩ body
          simp only [List.map_cons, RCap.rename] at this
          rw [this]
          cases eval O P n env (.tx σ.next :: ρ) ⟨σ.next + 1, true⟩ body with
          | done o σ₂ tr =>
            cases o with
            | ok v => cases h : σ₂.live <;> simp [h, Result.rename, Event.rename]
            | err => cases h : σ₂.live <;> simp [h, Result.rename, Event.rename]
          | _ => rfl
    | store op c args =>
      simp only [eval, List.getElem?_map]
      cases ρ[c]? with
      | none => rfl
      | some r =>
        cases r with
        | res id => cases lookupAll env args <;> rfl
        | tx t =>
          cases lookupAll env args with
          | none => rfl
          | some vs =>
            cases hl : σ.live <;> simp [hl, RCap.rename, Result.rename, Event.rename, hstore]
    | commit c =>
      simp only [eval, List.getElem?_map]
      cases ρ[c]? with
      | none => rfl
      | some r =>
        cases r with
        | res id => rfl
        | tx t =>
          cases hl : σ.live <;> cases hc : O.commit t <;>
            simp [hl, hc, hcommit, RCap.rename, Result.rename, Event.rename]
    | rollback c =>
      simp only [eval, List.getElem?_map]
      cases ρ[c]? with
      | none => rfl
      | some r =>
        cases r with
        | res id => rfl
        | tx t => cases hl : σ.live <;> simp [hl, RCap.rename, Result.rename, Event.rename]
    | abort => rfl
    | len a => simp only [eval]; split <;> rfl
    | index a i => simp only [eval]; split <;> first | rfl | exact indexResult_rename.symm

/-- **Theorem 5 (capability non-leakage, semantic form).** Running an entry
point with provided capabilities `caps` or with renamed capabilities
`caps.map π` gives the same outcome (in particular the same returned value) and
the same final state; only the capability ids in the trace are renamed. So the
result cannot depend on — let alone contain — the capabilities. (Here the
oracle's answers to `fetch` must not depend on the identity of the `Net`
capability, e.g. `O.fetch` constant in its first argument.) -/
theorem result_independent_of_caps (O : Oracle) (P : Prog)
    (hO : ∀ id₁ id₂ v, O.fetch id₁ v = O.fetch id₂ v)
    (π : Nat → Nat) (n f : Nat) (vs : List Val) (caps : List Nat) :
    runFun O P n f vs (caps.map π) = (runFun O P n f vs caps).rename π := by
  simp only [runFun]
  cases P[f]? with
  | none => rfl
  | some fd =>
    have := eval_rename O O P π (fun id v => hO _ _ v) rfl rfl n vs (caps.map .res) St.init fd.body
    simp only [List.map_map] at this
    simpa [Function.comp_def, RCap.rename] using this

end Kekkai
