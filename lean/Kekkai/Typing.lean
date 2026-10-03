import Kekkai.Syntax
import Kekkai.Basic

/-!
# Typing

The judgment is

```
HasType P Γ Δ s e τ s'
```

* `Γ : List Ty` — unrestricted value context.
* `Δ : List (Option CapKind)` — capability context. A `none` slot is a *masked*
  capability: it exists (so de Bruijn indices stay stable) but cannot be used.
* `s, s' : TxSt` — the state of the linear transaction owned by the innermost
  enclosing `transaction` block, threaded through evaluation order
  (`Δ ⊢ e ⊣ Δ'` style). `none` means "this code does not own a transaction"
  (every function body is checked in state `none`), `live` means the owned `tx`
  must still be consumed, `done` means it was consumed.

Linearity of `Tx`:
* `commit`/`rollback` require `live` and produce `done` — so they can happen at
  most once on every path;
* a `transaction` body must go from `live` to `done` — so on every successful path
  they happen at least once;
* store operations and passing a `tx` to a function (a *borrow*: callees can
  perform store operations but not
  consume it, since their body is checked in state `none`) require that the
  transaction is not `done`.

No irrevocable effects inside transactions: the body of a `transaction` is
checked in the masked context `some tx :: mask Δ`, where `mask` hides every
capability except `Log`. Hence `Net` (and `Db`, so no nested transactions) are
unusable inside, including indirectly through calls, since calls can only pass
capabilities that are visible in `Δ`.
-/

namespace Kekkai

/-- State of the linear transaction owned by the current code. -/
inductive TxSt where
  | none
  | live
  | done
  deriving DecidableEq, Repr

/-- Typing of literals. -/
inductive ValTy : Val → Ty → Prop where
  | unit : ValTy .unit .unit
  | bool (b : Bool) : ValTy (.bool b) .bool
  | int (i : Int) : ValTy (.int i) .int

/-- Result type of a binary operator (both operands are `Int`). -/
def BinOp.resTy : BinOp → Ty
  | .add => .int
  | .sub => .int
  | .lt => .bool
  | .eq => .bool

/-- Result type of a store operation. -/
def StoreOp.resTy : StoreOp → Ty
  | .get => .int
  | .put => .unit
  | .delete => .unit
  | .outbox => .unit

/-- Hide every capability except `Log`. Used for the outer context of a
transaction body. -/
def maskSlot : Option CapKind → Option CapKind
  | some .log => some .log
  | _ => none

/-- Mask a capability context (see `maskSlot`). -/
def mask (Δ : List (Option CapKind)) : List (Option CapKind) := Δ.map maskSlot

/-- Allowed transitions of the owned-transaction state for `abort`: aborting may
pretend that the transaction was consumed (it is rolled back automatically). -/
def AbortStep (s s' : TxSt) : Prop := s' = s ∨ (s = .live ∧ s' = .done)

/-- The typing judgment `HasType P Γ Δ s e τ s'`. -/
inductive HasType (P : Prog) :
    List Ty → List (Option CapKind) → TxSt → Expr → Ty → TxSt → Prop where
  | val {Γ Δ s v τ} : ValTy v τ → HasType P Γ Δ s (.val v) τ s
  | var {Γ Δ s i τ} : Γ[i]? = some τ → HasType P Γ Δ s (.var i) τ s
  | let_ {Γ Δ s s₁ s₂ e₁ e₂ τ₁ τ₂} :
      HasType P Γ Δ s e₁ τ₁ s₁ → HasType P (τ₁ :: Γ) Δ s₁ e₂ τ₂ s₂ →
      HasType P Γ Δ s (.let_ e₁ e₂) τ₂ s₂
  | ite {Γ Δ s s₁ s₂ c t e τ} :
      HasType P Γ Δ s c .bool s₁ → HasType P Γ Δ s₁ t τ s₂ → HasType P Γ Δ s₁ e τ s₂ →
      HasType P Γ Δ s (.ite c t e) τ s₂
  | bin {Γ Δ s s₁ s₂ op a b} :
      HasType P Γ Δ s a .int s₁ → HasType P Γ Δ s₁ b .int s₂ →
      HasType P Γ Δ s (.bin op a b) op.resTy s₂
  | call {Γ Δ s f args cs fd} :
      P[f]? = some fd →
      lookupAll Γ args = some fd.params →
      lookupAll Δ cs = some (fd.caps.map some) →
      (CapKind.tx ∈ fd.caps → s ≠ .done) →
      HasType P Γ Δ s (.call f args cs) fd.ret s
  | log {Γ Δ s s₁ c e τ} :
      Δ[c]? = some (some .log) → HasType P Γ Δ s e τ s₁ →
      HasType P Γ Δ s (.log c e) .unit s₁
  | fetch {Γ Δ s s₁ c e τ} :
      Δ[c]? = some (some .net) → HasType P Γ Δ s e τ s₁ →
      HasType P Γ Δ s (.fetch c e) .int s₁
  | transaction {Γ Δ s d body τ} :
      Δ[d]? = some (some .db) →
      HasType P Γ (some .tx :: mask Δ) .live body τ .done →
      HasType P Γ Δ s (.transaction d body) τ s
  | store {Γ Δ s op c args tys} :
      Δ[c]? = some (some .tx) → lookupAll Γ args = some tys → s ≠ .done →
      HasType P Γ Δ s (.store op c args) op.resTy s
  | commit {Γ Δ c} :
      Δ[c]? = some (some .tx) → HasType P Γ Δ .live (.commit c) .bool .done
  | rollback {Γ Δ c} :
      Δ[c]? = some (some .tx) → HasType P Γ Δ .live (.rollback c) .unit .done
  | abort {Γ Δ s s' τ} : AbortStep s s' → HasType P Γ Δ s .abort τ s'

/-- A function is well typed when its body has its return type, in the context of
its parameters, *without owning a transaction* (state `none` → `none`). -/
def WTFun (P : Prog) (fd : FunDef) : Prop :=
  HasType P fd.params (fd.caps.map some) .none fd.body fd.ret .none

/-- Every function of the program is well typed. -/
def WTProg (P : Prog) : Prop := ∀ (f : Nat) (fd : FunDef), P[f]? = some fd → WTFun P fd

/-- Code that does not own a transaction cannot acquire one: state `none` is
preserved. -/
theorem HasType.none_pres' {P Γ Δ s e τ s'} (h : HasType P Γ Δ s e τ s') :
    s = .none → s' = .none := by
  induction h with
  | val | var | call | transaction | store => exact id
  | let_ _ _ ih₁ ih₂ => exact fun hs => ih₂ (ih₁ hs)
  | ite _ _ _ ih₁ ih₂ _ => exact fun hs => ih₂ (ih₁ hs)
  | bin _ _ ih₁ ih₂ => exact fun hs => ih₂ (ih₁ hs)
  | log _ _ ih | fetch _ _ ih => exact ih
  | commit | rollback => intro hs; cases hs
  | abort h =>
    intro hs
    rcases h with h | ⟨h, _⟩
    · rw [h]; exact hs
    · subst hs; cases h

/-- Code that does not own a transaction cannot acquire one: state `none` is
preserved. -/
theorem HasType.none_pres {P Γ Δ e τ s'} (h : HasType P Γ Δ .none e τ s') : s' = .none :=
  h.none_pres' rfl

theorem lookup_mask {Δ : List (Option CapKind)} {c : Nat} {k : CapKind}
    (h : (mask Δ)[c]? = some (some k)) : k = .log ∧ Δ[c]? = some (some .log) := by
  simp only [mask, List.getElem?_map] at h
  cases hc : Δ[c]? with
  | none => simp [hc] at h
  | some o =>
    simp [hc] at h
    cases o with
    | none => simp [maskSlot] at h
    | some k' => cases k' <;> simp [maskSlot] at h <;> simp [h]

end Kekkai
