import Kekkai.Typing
import Kekkai.Semantics

/-!
# Theorem 1: type safety

For the fuel-based big-step interpreter, type safety reads: a well-typed
expression evaluated in a well-typed runtime environment never gets `stuck`; when
it terminates with a value, the value has the expected type.
-/

namespace Kekkai

/-- A runtime capability fits a capability kind. -/
def CapFits : RCap → CapKind → Prop
  | .tx _, .tx => True
  | .res _, .log => True
  | .res _, .net => True
  | .res _, .db => True
  | _, _ => False

/-- A runtime capability fits a (possibly masked) slot. -/
def CapOk (r : RCap) (s : Option CapKind) : Prop := ∀ k, s = some k → CapFits r k

/-- Runtime value environment matches the value context. -/
abbrev EnvOk (env : List Val) (Γ : List Ty) : Prop := Forall2 ValTy env Γ

/-- Runtime capability environment matches the capability context. -/
abbrev CapsOk (ρ : List RCap) (Δ : List (Option CapKind)) : Prop := Forall2 CapOk ρ Δ

/-- Agreement between the static transaction state and the dynamic `live` flag. -/
structure TxInv (Δ : List (Option CapKind)) (s : TxSt) (live : Bool) : Prop where
  live_of_live : s = .live → live = true
  dead_of_done : s = .done → live = false
  /-- code that does not own a transaction but has a `tx` in scope (it was
  borrowed from a caller) runs while that transaction is open. -/
  borrowed : s = .none → ∀ c : Nat, Δ[c]? = some (some CapKind.tx) → live = true

/-- Postcondition of the safety lemma. -/
def SafePost (Δ : List (Option CapKind)) (τ : Ty) (s s' : TxSt) (σ : St) : Result → Prop
  | .timeout => True
  | .stuck => False
  | .done o σ' _ =>
    (s = .none → σ'.live = σ.live) ∧ ∀ v, o = .ok v → ValTy v τ ∧ TxInv Δ s' σ'.live

theorem SafePost.ne_stuck {Δ τ s s' σ r} (h : SafePost Δ τ s s' σ r) : r ≠ .stuck := by
  rintro rfl; exact h

theorem SafePost.prepend {Δ τ s s' σ r tr} (h : SafePost Δ τ s s' σ r) :
    SafePost Δ τ s s' σ (r.prepend tr) := by
  cases r <;> simp only [Result.prepend, SafePost] at h ⊢ <;> exact h

theorem SafePost.bind {Δ τ₁ τ₂ s s₁ s₂ σ r k}
    (hr : SafePost Δ τ₁ s s₁ σ r) (hs : s = .none → s₁ = .none)
    (hk : ∀ v σ₁ tr, r = .done (.ok v) σ₁ tr → ValTy v τ₁ → TxInv Δ s₁ σ₁.live →
      (s = .none → σ₁.live = σ.live) → SafePost Δ τ₂ s₁ s₂ σ₁ (k v σ₁)) :
    SafePost Δ τ₂ s s₂ σ (r.bind k) := by
  cases r with
  | timeout => trivial
  | stuck => exact hr
  | done o σ₁ tr =>
    cases o with
    | err => simpa [Result.bind, SafePost] using hr.1
    | ok v =>
      obtain ⟨h1, h2⟩ := hr
      obtain ⟨hv, hinv⟩ := h2 v rfl
      have := (hk v σ₁ tr rfl hv hinv h1).prepend (tr := tr)
      simp only [Result.bind]
      revert this
      cases (k v σ₁).prepend tr with
      | timeout => intro; trivial
      | stuck => intro h; exact h
      | done o' σ' tr' =>
        rintro ⟨h3, h4⟩
        exact ⟨fun h => (h3 (hs h)).trans (h1 h), h4⟩

theorem SafePost.done_ok {Δ τ s s' σ v tr} (hv : ValTy v τ) (hi : TxInv Δ s' σ.live) :
    SafePost Δ τ s s' σ (.done (.ok v) σ tr) := by
  refine ⟨fun _ => rfl, ?_⟩
  rintro _ ⟨⟩; exact ⟨hv, hi⟩

theorem capsOk_mask {ρ Δ} (h : CapsOk ρ Δ) : CapsOk ρ (mask Δ) := by
  unfold mask
  refine Forall2.map_right (fun r o hro => ?_) h
  intro k hk
  cases o with
  | none => simp [maskSlot] at hk
  | some k' => cases k' <;> simp [maskSlot] at hk <;> subst hk <;> exact hro _ rfl

theorem capOk_res {r : RCap} {k : CapKind} (h : CapOk r (some k)) (hk : k ≠ .tx) :
    ∃ id, r = .res id := by
  have := h k rfl
  cases r <;> cases k <;> simp_all [CapFits]

theorem capOk_tx {r : RCap} (h : CapOk r (some .tx)) : ∃ t, r = .tx t := by
  have := h _ rfl
  cases r <;> simp_all [CapFits]

theorem lookup_res {ρ : List RCap} {Δ : List (Option CapKind)} {c : Nat} {k : CapKind} (hρ : CapsOk ρ Δ) (hc : Δ[c]? = some (some k)) (hk : k ≠ .tx) :
    ∃ id, ρ[c]? = some (RCap.res id) := by
  obtain ⟨r, hr, hok⟩ := hρ.get hc
  obtain ⟨id, rfl⟩ := capOk_res hok hk
  exact ⟨id, hr⟩

theorem lookup_tx {ρ : List RCap} {Δ : List (Option CapKind)} {c : Nat} (hρ : CapsOk ρ Δ) (hc : Δ[c]? = some (some .tx)) :
    ∃ t, ρ[c]? = some (RCap.tx t) := by
  obtain ⟨r, hr, hok⟩ := hρ.get hc
  obtain ⟨t, rfl⟩ := capOk_tx hok
  exact ⟨t, hr⟩

theorem evalBin_ty (op : BinOp) (a b : Int) :
    ∃ v, evalBin op (.int a) (.int b) = some v ∧ ValTy v op.resTy := by
  cases op <;> exact ⟨_, rfl, by constructor⟩

theorem storeResult_ty (O : Oracle) (op : StoreOp) (t : Nat) (vs : List Val) :
    ValTy (storeResult O op t vs) op.resTy := by
  cases op <;> constructor

/-- A capability parameter of a callee comes from a capability of the caller. -/
theorem callee_slot {Δ : List (Option CapKind)} {cs : List Nat} {ks : List CapKind} {j : Nat}
    {k : CapKind} (h : lookupAll Δ cs = some (ks.map some)) (hj : (ks.map some)[j]? = some (some k)) :
    (∃ c : Nat, Δ[c]? = some (some k)) ∧ k ∈ ks := by
  obtain ⟨c, _, hc⟩ := lookupAll_get h hj
  refine ⟨⟨c, hc⟩, ?_⟩
  have := List.mem_of_getElem? hj
  simpa using this

/-- **Type safety (core lemma).** For any fuel, a well-typed expression run in a
well-typed environment never gets stuck, and a returned value has the expected
type. -/
theorem eval_safe (O : Oracle) {P : Prog} (hP : WTProg P) :
    ∀ (n : Nat) {Γ Δ s e τ s'} {env : List Val} {ρ : List RCap} {σ : St},
      HasType P Γ Δ s e τ s' → EnvOk env Γ → CapsOk ρ Δ → TxInv Δ s σ.live →
      SafePost Δ τ s s' σ (eval O P n env ρ σ e) := by
  intro n
  induction n with
  | zero => intros; simp [eval, SafePost]
  | succ n ih =>
    intro Γ Δ s e τ s' env ρ σ ht henv hρ hinv
    cases ht with
    | val hv => exact SafePost.done_ok hv hinv
    | var hi =>
      obtain ⟨v, hv, hvt⟩ := henv.get hi
      simp only [eval, hv]
      exact SafePost.done_ok hvt hinv
    | let_ h₁ h₂ =>
      simp only [eval]
      refine SafePost.bind (ih h₁ henv hρ hinv) h₁.none_pres' ?_
      intro v σ₁ _ _ hv hinv₁ _
      exact ih h₂ (.cons hv henv) hρ hinv₁
    | ite hc ht hf =>
      simp only [eval]
      refine SafePost.bind (ih hc henv hρ hinv) hc.none_pres' ?_
      intro v σ₁ _ _ hv hinv₁ _
      cases hv with
      | bool b =>
        cases b
        · exact ih hf henv hρ hinv₁
        · exact ih ht henv hρ hinv₁
    | @bin _ _ _ _ _ op _ _ ha hb =>
      simp only [eval]
      refine SafePost.bind (ih ha henv hρ hinv) ha.none_pres' ?_
      intro v₁ σ₁ _ _ hv₁ hinv₁ _
      refine SafePost.bind (ih hb henv hρ hinv₁) hb.none_pres' ?_
      intro v₂ σ₂ _ _ hv₂ hinv₂ _
      cases hv₁ with
      | int a =>
        cases hv₂ with
        | int b =>
          obtain ⟨v, hv, hvt⟩ := evalBin_ty op a b
          simp only [hv]
          exact SafePost.done_ok hvt hinv₂
    | @call _ _ _ f args cs fd hf hargs hcs hnd =>
      obtain ⟨vs, hvs, hvst⟩ := lookupAll_forall2 henv hargs
      obtain ⟨rs, hrs, hrst⟩ := lookupAll_forall2 hρ hcs
      simp only [eval, hf, hvs, hrs]
      have hinv' : TxInv (fd.caps.map some) .none σ.live := by
        refine ⟨nofun, nofun, fun _ c hc => ?_⟩
        obtain ⟨⟨c', hc'⟩, hmem⟩ := callee_slot hcs hc
        have hs := hnd hmem
        cases s with
        | none => exact hinv.borrowed rfl c' hc'
        | live => exact hinv.live_of_live rfl
        | done => exact absurd rfl hs
      have := ih (hP f fd hf) hvst hrst hinv'
      revert this
      cases eval O P n vs rs σ fd.body with
      | timeout => intro; trivial
      | stuck => intro h; exact h
      | done o σ' tr =>
        rintro ⟨h1, h2⟩
        have hl := h1 rfl
        refine ⟨fun _ => hl, fun v hv => ⟨(h2 v hv).1, ?_⟩⟩
        rw [hl]; exact hinv
    | log hc he =>
      simp only [eval]
      refine SafePost.bind (ih he henv hρ hinv) he.none_pres' ?_
      intro v σ₁ _ _ _ hinv₁ _
      obtain ⟨id, hid⟩ := lookup_res hρ hc (by decide)
      simp only [hid]
      exact SafePost.done_ok .unit hinv₁
    | fetch hc he =>
      simp only [eval]
      refine SafePost.bind (ih he henv hρ hinv) he.none_pres' ?_
      intro v σ₁ _ _ _ hinv₁ _
      obtain ⟨id, hid⟩ := lookup_res hρ hc (by decide)
      simp only [hid]
      exact SafePost.done_ok (.int _) hinv₁
    | @transaction _ _ _ d body _ hd hb =>
      obtain ⟨id, hid⟩ := lookup_res hρ hd (by decide)
      simp only [eval, hid]
      have hρ' : CapsOk (.tx σ.next :: ρ) (some .tx :: mask Δ) :=
        .cons (fun k hk => by cases hk; trivial) (capsOk_mask hρ)
      have hinv' : TxInv (some .tx :: mask Δ) .live (⟨σ.next + 1, true⟩ : St).live :=
        ⟨fun _ => rfl, nofun, nofun⟩
      have := ih hb henv hρ' hinv'
      revert this
      cases eval O P n env (.tx σ.next :: ρ) ⟨σ.next + 1, true⟩ body with
      | timeout => intro; trivial
      | stuck => intro h; exact h
      | done o σ₂ tr =>
        rintro ⟨_, h2⟩
        cases o with
        | ok v =>
          obtain ⟨hv, hinv₂⟩ := h2 v rfl
          have hl : σ₂.live = false := hinv₂.dead_of_done rfl
          simp only [hl]
          exact ⟨fun _ => rfl, fun v' hv' => by cases hv'; exact ⟨hv, hinv⟩⟩
        | err => exact ⟨fun _ => rfl, fun _ => nofun⟩
    | @store _ _ _ op _ _ _ hc hargs hs₁ =>
      obtain ⟨t, ht⟩ := lookup_tx hρ hc
      obtain ⟨vs, hvs, _⟩ := lookupAll_forall2 henv hargs
      have hl : σ.live = true := by
        cases s with
        | none => exact hinv.borrowed rfl _ hc
        | live => exact hinv.live_of_live rfl
        | done => exact absurd rfl hs₁
      simp only [eval, ht, hvs, hl]
      exact SafePost.done_ok (storeResult_ty O op t vs) hinv
    | commit hc =>
      obtain ⟨t, ht⟩ := lookup_tx hρ hc
      simp only [eval, ht, hinv.live_of_live rfl]
      cases O.commit t
      all_goals
        simp only [Bool.false_eq_true, ite_true, ite_false]
        refine ⟨nofun, fun v hv => ?_⟩
        cases hv
        exact ⟨.bool _, nofun, fun _ => rfl, nofun⟩
    | rollback hc =>
      obtain ⟨t, ht⟩ := lookup_tx hρ hc
      simp only [eval, ht, hinv.live_of_live rfl]
      refine ⟨nofun, fun v hv => ?_⟩
      cases hv
      exact ⟨.unit, nofun, fun _ => rfl, nofun⟩
    | abort _ =>
      simp only [eval]
      exact ⟨fun _ => rfl, fun _ => nofun⟩

/-- **Theorem 1 (type safety).** Running a well-typed entry function of a
well-typed program, with well-typed arguments and capabilities of the declared
kinds (`Log`/`Net`/`Db`; entry points take no `Tx`), never gets stuck — for any
amount of fuel — and a returned value has the declared return type. -/
theorem type_safety (O : Oracle) {P : Prog} (hP : WTProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) (hntx : CapKind.tx ∉ fd.caps)
    {vs : List Val} {caps : List Nat} (hvs : EnvOk vs fd.params)
    (hcaps : CapsOk (caps.map .res) (fd.caps.map some)) (n : Nat) :
    runFun O P n f vs caps ≠ .stuck ∧
      ∀ v σ tr, runFun O P n f vs caps = .done (.ok v) σ tr → ValTy v fd.ret := by
  have hinv : TxInv (fd.caps.map some) .none St.init.live := by
    refine ⟨nofun, nofun, fun _ c hc => ?_⟩
    have := List.mem_of_getElem? hc
    simp at this
    exact absurd this hntx
  have h := eval_safe O hP n (hP f fd hf) hvs hcaps hinv
  simp only [runFun, hf]
  refine ⟨h.ne_stuck, fun v σ tr he => ?_⟩
  rw [he] at h
  exact (h.2 v rfl).1

end Kekkai
