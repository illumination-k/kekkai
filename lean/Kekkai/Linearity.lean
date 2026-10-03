import Kekkai.Typing
import Kekkai.Semantics
import Kekkai.Monitor
import Kekkai.Safety

/-!
# Theorems 3 and 4: linear transactions, no irrevocable effects inside them

We show that the trace of every well-typed program run is accepted by the
transaction monitor (`TxSafe`), and then read off the trace properties proved
in `Kekkai.Monitor`.

The core lemma `eval_txsafe` relates, during evaluation, the static
transaction state `s`, the dynamic flag `σ.live`, the monitor state `M`, and the
id `o` of the transaction owned by the innermost enclosing `transaction` block.
-/

namespace Kekkai

/-- Invariant relating static and dynamic transaction state with the monitor.
`o` is the id of the innermost open transaction (meaningless when none is open). -/
structure Rel (Δ : List (Option CapKind)) (ρ : List RCap) (s : TxSt) (σ : St) (M : Mon)
    (o : Nat) : Prop where
  act : M.active = if σ.live then some o else none
  seen : ∀ x ∈ M.seen, x < σ.next
  /-- inside a transaction no `Net` capability is visible -/
  noNet : σ.live = true → ∀ c : Nat, Δ[c]? ≠ some (some CapKind.net)
  /-- inside a transaction no `Db` capability is visible (no nesting) -/
  noDb : σ.live = true → ∀ c : Nat, Δ[c]? ≠ some (some CapKind.db)
  /-- inside a transaction every visible `Tx` is the open one -/
  txOwn : σ.live = true → ∀ c : Nat, Δ[c]? = some (some CapKind.tx) → ρ[c]? = some (RCap.tx o)
  liveSt : σ.live = true → s ≠ .done
  /-- outside a transaction a visible `Tx` must be a consumed one -/
  deadSt : σ.live = false → ∀ c : Nat, Δ[c]? = some (some CapKind.tx) → s = .done
  stLive : s = .live → σ.live = true

theorem Rel.relive {Δ ρ s σ M o σ' M'} (h : Rel Δ ρ s σ M o) (hl : σ'.live = σ.live)
    (ha : M'.active = if σ'.live then some o else none) (hs : ∀ x ∈ M'.seen, x < σ'.next) :
    Rel Δ ρ s σ' M' o where
  act := ha
  seen := hs
  noNet := hl ▸ h.noNet
  noDb := hl ▸ h.noDb
  txOwn := hl ▸ h.txOwn
  liveSt := hl ▸ h.liveSt
  deadSt := hl ▸ h.deadSt
  stLive := hl ▸ h.stLive

/-- Postcondition of `eval_txsafe`. -/
def TxPost (Δ : List (Option CapKind)) (ρ : List RCap) (s s' : TxSt) (σ : St) (M : Mon)
    (o : Nat) : Result → Prop
  | .done out σ' tr =>
    ∃ M', M.run tr = some M' ∧ M'.active = (if σ'.live then some o else none) ∧
      (∀ x ∈ M'.seen, x < σ'.next) ∧ (s = .none → σ'.live = σ.live) ∧
      ∀ v, out = .ok v → Rel Δ ρ s' σ' M' o
  | _ => True

theorem TxPost.prepend {Δ ρ s₀ s s' σ M M₀ o r tr} (hM : M₀.run tr = some M)
    (h : TxPost Δ ρ s s' σ M o r) (σ₀ : St) (hs : s₀ = .none → s = .none)
    (hl : s₀ = .none → σ.live = σ₀.live) :
    TxPost Δ ρ s₀ s' σ₀ M₀ o (r.prepend tr) := by
  cases r with
  | done out σ' tr' =>
    obtain ⟨M', h1, h2, h3, h4, h5⟩ := h
    exact ⟨M', Mon.run_append.mpr ⟨M, hM, h1⟩, h2, h3,
      fun h0 => (h4 (hs h0)).trans (hl h0), h5⟩
  | _ => trivial

theorem TxPost.bind {Δ ρ s s₁ s₂ σ M o r k} (hr : TxPost Δ ρ s s₁ σ M o r)
    (hs : s = .none → s₁ = .none)
    (hk : ∀ v σ₁ tr M₁, r = .done (.ok v) σ₁ tr → M.run tr = some M₁ → Rel Δ ρ s₁ σ₁ M₁ o →
      (s = .none → σ₁.live = σ.live) → TxPost Δ ρ s₁ s₂ σ₁ M₁ o (k v σ₁)) :
    TxPost Δ ρ s s₂ σ M o (r.bind k) := by
  cases r with
  | done out σ₁ tr =>
    cases out with
    | err =>
      obtain ⟨M', h1, h2, h3, h4, _⟩ := hr
      exact ⟨M', h1, h2, h3, h4, fun _ => nofun⟩
    | ok v =>
      obtain ⟨M₁, h1, _, _, h4, h5⟩ := hr
      simp only [Result.bind]
      exact TxPost.prepend h1 (hk v σ₁ tr M₁ rfl h1 (h5 v rfl) h4) σ hs h4
  | _ => trivial

theorem TxPost.nil {Δ ρ s σ M o v} (h : Rel Δ ρ s σ M o) :
    TxPost Δ ρ s s σ M o (.done (.ok v) σ []) :=
  ⟨M, rfl, h.act, h.seen, fun _ => rfl, fun _ _ => h⟩

theorem TxPost.single {Δ ρ s σ M o v ev} (h : Rel Δ ρ s σ M o) (hst : M.step ev = some M) :
    TxPost Δ ρ s s σ M o (.done (.ok v) σ [ev]) :=
  ⟨M, by simp [Mon.run, hst], h.act, h.seen, fun _ => rfl, fun _ _ => h⟩

/-- Consuming the open transaction (`commit` succeeded or failed, `rollback`). -/
theorem TxPost.consume {Δ ρ σ M o v ev} (h : Rel Δ ρ .live σ M o)
    (hst : M.step ev = some ⟨none, M.seen⟩) :
    TxPost Δ ρ .live .done σ M o (.done (.ok v) ⟨σ.next, false⟩ [ev]) := by
  refine ⟨⟨none, M.seen⟩, by simp [Mon.run, hst], rfl, h.seen, nofun, fun _ _ => ?_⟩
  exact ⟨rfl, h.seen, nofun, nofun, nofun, nofun, fun _ _ _ => rfl, nofun⟩

/-- **Core lemma for Theorems 3 and 4.** Along the evaluation of a well-typed
expression, the produced trace is accepted by the monitor, starting from any
monitor state related to the current static/dynamic transaction state. -/
theorem eval_txsafe (O : Oracle) {P : Prog} (hP : WTProg P) :
    ∀ (n : Nat) {Γ Δ s e τ s'} {env : List Val} {ρ : List RCap} {σ : St} {M : Mon} {o : Nat},
      HasType P Γ Δ s e τ s' → Rel Δ ρ s σ M o →
      TxPost Δ ρ s s' σ M o (eval O P n env ρ σ e) := by
  intro n
  induction n with
  | zero => intros; simp [eval, TxPost]
  | succ n ih =>
    intro Γ Δ s e τ s' env ρ σ M o ht hR
    cases ht with
    | val _ => exact TxPost.nil hR
    | var _ =>
      simp only [eval]
      split
      · exact TxPost.nil hR
      · trivial
    | let_ h₁ h₂ =>
      simp only [eval]
      exact TxPost.bind (ih h₁ hR) h₁.none_pres' fun _ _ _ _ _ _ hR₁ _ => ih h₂ hR₁
    | ite hc ht hf =>
      simp only [eval]
      refine TxPost.bind (ih hc hR) hc.none_pres' fun v _ _ _ _ _ hR₁ _ => ?_
      split
      · exact ih ht hR₁
      · exact ih hf hR₁
      · trivial
    | bin ha hb =>
      simp only [eval]
      refine TxPost.bind (ih ha hR) ha.none_pres' fun _ _ _ _ _ _ hR₁ _ => ?_
      refine TxPost.bind (ih hb hR₁) hb.none_pres' fun _ _ _ _ _ _ hR₂ _ => ?_
      split
      · exact TxPost.nil hR₂
      · trivial
    | @call _ _ _ f args cs fd hf hargs hcs hnd =>
      simp only [eval]
      split
      · rename_i fd' vs rs hf' _ hrs
        rw [hf] at hf'; cases hf'
        have hR' : Rel (fd.caps.map some) rs .none σ M o := by
          refine ⟨hR.act, hR.seen, ?_, ?_, ?_, nofun, ?_, nofun⟩
          · intro hl j hj
            obtain ⟨⟨c, hc⟩, _⟩ := callee_slot hcs hj
            exact hR.noNet hl c hc
          · intro hl j hj
            obtain ⟨⟨c, hc⟩, _⟩ := callee_slot hcs hj
            exact hR.noDb hl c hc
          · intro hl j hj
            obtain ⟨i, hi, hΔi⟩ := lookupAll_get hcs hj
            rw [lookupAll_getElem hrs hi]
            exact hR.txOwn hl i hΔi
          · intro hl j hj
            obtain ⟨⟨c, hc⟩, hmem⟩ := callee_slot hcs hj
            exact absurd (hR.deadSt hl c hc) (hnd hmem)
        have := ih (hP f fd hf) hR' (env := vs)
        revert this
        cases eval O P n vs rs σ fd.body with
        | done out σ' tr =>
          rintro ⟨M', h1, h2, h3, h4, _⟩
          have hl := h4 rfl
          exact ⟨M', h1, h2, h3, fun _ => hl, fun _ _ => hR.relive hl h2 h3⟩
        | _ => intro; trivial
      · trivial
    | log hc he =>
      simp only [eval]
      refine TxPost.bind (ih he hR) he.none_pres' fun _ _ _ _ _ _ hR₁ _ => ?_
      split
      · exact TxPost.single hR₁ (by simp [Mon.step])
      · trivial
    | fetch hc he =>
      simp only [eval]
      refine TxPost.bind (ih he hR) he.none_pres' fun _ σ₁ _ _ _ _ hR₁ _ => ?_
      split
      · have hl : σ₁.live = false := by
          cases h : σ₁.live
          · rfl
          · exact absurd hc (hR₁.noNet h _)
        have ha : _ = none := hR₁.act.trans (by simp [hl])
        exact TxPost.single hR₁ (by simp [Mon.step, ha])
      · trivial
    | @transaction _ _ _ d body _ hd hb =>
      have hl : σ.live = false := by
        cases h : σ.live
        · rfl
        · exact absurd hd (hR.noDb h _)
      have ha : M.active = none := hR.act.trans (by simp [hl])
      have hfresh : σ.next ∉ M.seen := fun h => Nat.lt_irrefl _ (hR.seen _ h)
      simp only [eval]
      split
      · rename_i id _
        let M₁ : Mon := ⟨some σ.next, σ.next :: M.seen⟩
        have hstep : M.step (.txBegin id σ.next) = some M₁ := by
          simp [Mon.step, ha, hfresh, M₁]
        have hR₁ : Rel (some .tx :: mask Δ) (.tx σ.next :: ρ) .live ⟨σ.next + 1, true⟩ M₁
            σ.next := by
          refine ⟨rfl, ?_, ?_, ?_, ?_, fun _ => nofun, nofun, fun _ => rfl⟩
          · intro x hx
            simp only [M₁, List.mem_cons] at hx
            rcases hx with rfl | hx
            · exact Nat.lt_succ_self _
            · exact Nat.lt_succ_of_lt (hR.seen x hx)
          · intro _ c hc
            cases c with
            | zero => simp at hc
            | succ c => simp at hc; exact absurd (lookup_mask hc).1 (by decide)
          · intro _ c hc
            cases c with
            | zero => simp at hc
            | succ c => simp at hc; exact absurd (lookup_mask hc).1 (by decide)
          · intro _ c hc
            cases c with
            | zero => rfl
            | succ c => simp at hc; exact absurd (lookup_mask hc).1 (by decide)
        have := ih hb hR₁ (env := env)
        revert this
        cases eval O P n env (.tx σ.next :: ρ) ⟨σ.next + 1, true⟩ body with
        | done out σ₂ tr =>
          rintro ⟨M₂, h1, h2, h3, _, h5⟩
          have hrun : M.run (.txBegin id σ.next :: tr) = some M₂ :=
            Mon.run_cons.mpr ⟨M₁, hstep, h1⟩
          cases out with
          | ok v =>
            have hl₂ : σ₂.live = false := by
              cases h : σ₂.live
              · rfl
              · exact absurd rfl ((h5 v rfl).liveSt h)
            simp only [hl₂]
            have ha₂ : M₂.active = none := h2.trans (by simp [hl₂])
            have ha' : M₂.active = if (⟨σ₂.next, σ.live⟩ : St).live then some o else none := by
              simp [ha₂, hl]
            exact ⟨M₂, hrun, ha', h3, fun _ => rfl, fun _ _ => hR.relive rfl ha' h3⟩
          | err =>
            cases hl₂ : σ₂.live
            · have ha₂ : M₂.active = none := h2.trans (by simp [hl₂])
              simp only [hl₂, Bool.false_eq_true, ite_false, List.append_nil]
              refine ⟨M₂, hrun, by simp [ha₂, hl], h3, fun _ => rfl, fun _ => nofun⟩
            · have ha₂ : M₂.active = some σ.next := h2.trans (by simp [hl₂])
              refine ⟨⟨none, M₂.seen⟩, ?_, by simp [hl], h3, fun _ => rfl, fun _ => nofun⟩
              simp only [hl₂, ite_true]
              rw [← List.cons_append]
              exact Mon.run_append.mpr ⟨M₂, hrun, by simp [Mon.run, Mon.step, ha₂]⟩
        | _ => intro; trivial
      · trivial
    | store hc _ hs₁ =>
      have hl : σ.live = true := by
        cases h : σ.live
        · exact absurd (hR.deadSt h _ hc) hs₁
        · rfl
      have hown := hR.txOwn hl _ hc
      have ha : M.active = some o := hR.act.trans (by simp [hl])
      simp only [eval, hown]
      split
      · rename_i ht _
        cases ht
        simp only [hl, ite_true]
        exact TxPost.single hR (by simp [Mon.step, ha])
      · trivial
    | commit hc =>
      have hl := hR.stLive rfl
      have ha : M.active = some o := hR.act.trans (by simp [hl])
      simp only [eval, hR.txOwn hl _ hc, hl, ite_true]
      split
      · exact TxPost.consume hR (by simp [Mon.step, ha])
      · exact TxPost.consume hR (by simp [Mon.step, ha])
    | rollback hc =>
      have hl := hR.stLive rfl
      have ha : M.active = some o := hR.act.trans (by simp [hl])
      simp only [eval, hR.txOwn hl _ hc, hl, ite_true]
      exact TxPost.consume hR (by simp [Mon.step, ha])
    | abort _ =>
      simp only [eval]
      exact ⟨M, rfl, hR.act, hR.seen, fun _ => rfl, fun _ => nofun⟩

/-- **Transactional discipline of whole runs.** Running a well-typed entry
function (one that takes no `Tx` parameter) of a well-typed program yields a trace
accepted by the transaction monitor — whatever the fuel, the oracle, and whether
the run ends with a value or an `abort`. -/
theorem run_txsafe (O : Oracle) {P : Prog} (hP : WTProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) (hntx : CapKind.tx ∉ fd.caps)
    {n : Nat} {vs : List Val} {caps : List Nat} {out : Outcome} {σ : St} {tr : List Event}
    (h : runFun O P n f vs caps = .done out σ tr) : TxSafe tr := by
  simp only [runFun, hf] at h
  have hR : Rel (fd.caps.map some) (caps.map .res) .none St.init Mon.init 0 := by
    refine ⟨rfl, by simp [Mon.init], nofun, nofun, nofun, nofun, fun _ c hc => ?_, nofun⟩
    have := List.mem_of_getElem? hc
    simp at this
    exact absurd this hntx
  have := eval_txsafe O hP n (hP f fd hf) hR (env := vs)
  rw [h] at this
  obtain ⟨M', h1, h2, _, h4, _⟩ := this
  refine ⟨M', h1, ?_⟩
  rw [h2, h4 rfl]
  rfl

/-- **Theorem 3 (linearity of `Tx`).** In the trace of a run of a well-typed
program, every `txBegin _ t` is followed by exactly one ending event of `t`
(`txCommit t`, or `txRollback t` — which also covers an explicit rollback, a
failed commit, and the automatic rollback on `abort`). After that event `t` never
occurs again: no store operation on, and no second commit/rollback of, `t`. -/
theorem tx_linearity (O : Oracle) {P : Prog} (hP : WTProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) (hntx : CapKind.tx ∉ fd.caps)
    {n : Nat} {vs : List Val} {caps : List Nat} {out : Outcome} {σ : St} {tr : List Event}
    (h : runFun O P n f vs caps = .done out σ tr)
    {a b : List Event} {d t : Nat} (hsplit : tr = a ++ .txBegin d t :: b) :
    ∃ b₁ e b₂, b = b₁ ++ e :: b₂ ∧ e.Ends t ∧ (∀ x ∈ b₁, ¬ x.Ends t) ∧
      (∀ x ∈ b₂, x.tx? ≠ some t) := by
  obtain ⟨b₁, e, b₂, h1, h2, h3, h4⟩ := (run_txsafe O hP hf hntx h).linear hsplit
  exact ⟨b₁, e, b₂, h1, h2, fun x hx => (h3 x hx).1, h4⟩

/-- **Theorem 4 (no irrevocable effects inside a transaction).** In the trace of a
run of a well-typed program, no `fetch` event occurs between `txBegin _ t` and
the event that ends `t`. -/
theorem no_irrevocable_in_tx (O : Oracle) {P : Prog} (hP : WTProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) (hntx : CapKind.tx ∉ fd.caps)
    {n : Nat} {vs : List Val} {caps : List Nat} {out : Outcome} {σ : St} {tr : List Event}
    (h : runFun O P n f vs caps = .done out σ tr)
    {a b₁ b₂ : List Event} {d t : Nat} {e : Event}
    (hsplit : tr = a ++ .txBegin d t :: (b₁ ++ e :: b₂))
    (he : e.Ends t) (hb₁ : ∀ y ∈ b₁, ¬ y.Ends t) :
    ∀ x ∈ b₁, ¬ x.IsIrrevocable :=
  fun _ hx => (run_txsafe O hP hf hntx h).no_irrevocable_inside hsplit he hb₁ hx

end Kekkai
