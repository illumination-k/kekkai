import Kekkai.Typing
import Kekkai.Semantics

/-!
# Theorem 2: effect soundness

Every event in a trace is authorized by a capability that was *passed in* (it is
in the runtime capability environment `ρ` of the evaluation), or — for transaction
operations — by a transaction handle that was *created during the evaluation*
(a fresh id in the range `[σ.next, σ'.next)`).

Since the interpreter can only reach capabilities through `ρ`, and `ρ` for a
callee is built from the capability arguments of the call, a function can only
perform effects through capabilities it receives as arguments. Corollary: a
function without capability parameters is pure — its trace is empty.
-/

namespace Kekkai

/-- An event is authorized by `ρ` and the range `[lo, hi)` of fresh transaction
ids. -/
def Event.Authorized (ρ : List RCap) (lo hi : Nat) : Event → Prop
  | .log c _ => RCap.res c ∈ ρ
  | .fetch c _ => RCap.res c ∈ ρ
  | .txBegin d t => RCap.res d ∈ ρ ∧ lo ≤ t ∧ t < hi
  | .txOp t _ _ => RCap.tx t ∈ ρ ∨ (lo ≤ t ∧ t < hi)
  | .txCommit t => RCap.tx t ∈ ρ ∨ (lo ≤ t ∧ t < hi)
  | .txRollback t => RCap.tx t ∈ ρ ∨ (lo ≤ t ∧ t < hi)

theorem Event.Authorized.mono {ρ ρ' : List RCap} {lo hi lo' hi' : Nat} {ev : Event}
    (hρ : ∀ r ∈ ρ, r ∈ ρ') (hlo : lo' ≤ lo) (hhi : hi ≤ hi') :
    ev.Authorized ρ lo hi → ev.Authorized ρ' lo' hi' := by
  cases ev <;> simp only [Event.Authorized] <;> intro h
  · exact hρ _ h
  · exact hρ _ h
  · exact ⟨hρ _ h.1, by omega, by omega⟩
  all_goals
    rcases h with h | h
    · exact .inl (hρ _ h)
    · exact .inr ⟨by omega, by omega⟩

/-- Postcondition of the effect-soundness lemma. -/
def EffPost (ρ : List RCap) (σ : St) (r : Result) : Prop :=
  ∀ o σ' tr, r = .done o σ' tr →
    σ.next ≤ σ'.next ∧ ∀ ev ∈ tr, ev.Authorized ρ σ.next σ'.next

theorem EffPost.bind {ρ σ r k} (hr : EffPost ρ σ r)
    (hk : ∀ v σ₁ tr, r = .done (.ok v) σ₁ tr → EffPost ρ σ₁ (k v σ₁)) :
    EffPost ρ σ (r.bind k) := by
  intro o σ' tr h
  rcases Result.bind_eq_done.mp h with ⟨h1, _⟩ | ⟨v, σ₁, tr₁, tr₂, h1, h2, rfl⟩
  · exact hr _ _ _ h1
  · obtain ⟨m1, a1⟩ := hr _ _ _ h1
    obtain ⟨m2, a2⟩ := hk v σ₁ tr₁ h1 _ _ _ h2
    refine ⟨Nat.le_trans m1 m2, fun ev hev => ?_⟩
    rcases List.mem_append.mp hev with hev | hev
    · exact Event.Authorized.mono (fun _ h => h) (Nat.le_refl _) m2 (a1 ev hev)
    · exact Event.Authorized.mono (fun _ h => h) m1 (Nat.le_refl _) (a2 ev hev)

theorem EffPost.single {ρ σ ev v} (h : ev.Authorized ρ σ.next σ.next) :
    EffPost ρ σ (.done (.ok v) σ [ev]) := by
  intro o σ' tr he
  cases he
  exact ⟨Nat.le_refl _, fun e he => by simp at he; subst he; exact h⟩

/-- **Effect soundness (core lemma).** No typing assumption is needed: this is a
property of the semantics — capabilities can only be reached through `ρ`. -/
theorem eval_effects (O : Oracle) (P : Prog) :
    ∀ (n : Nat) (env : List Val) (ρ : List RCap) (σ : St) (e : Expr),
      EffPost ρ σ (eval O P n env ρ σ e) := by
  intro n
  induction n with
  | zero => intro env ρ σ e o σ' tr h; simp [eval] at h
  | succ n ih =>
    intro env ρ σ e
    cases e with
    | val v =>
      intro o σ' tr h; simp only [eval] at h; cases h; simp
    | var i =>
      intro o σ' tr h; simp only [eval] at h; split at h <;> cases h; simp
    | let_ e₁ e₂ =>
      simp only [eval]; exact (ih _ _ _ _).bind fun v σ₁ _ _ => ih _ _ _ _
    | ite c t f =>
      simp only [eval]
      refine (ih _ _ _ _).bind fun v σ₁ _ _ => ?_
      split
      · exact ih _ _ _ _
      · exact ih _ _ _ _
      · intro o σ' tr h; cases h
    | bin op a b =>
      simp only [eval]
      refine (ih _ _ _ _).bind fun v₁ σ₁ _ _ => (ih _ _ _ _).bind fun v₂ σ₂ _ _ => ?_
      intro o σ' tr h
      obtain ⟨rfl, rfl, _⟩ := binResult_done h
      simp
    | call f args cs =>
      intro o σ' tr h
      simp only [eval] at h
      split at h
      · rename_i fd vs rs _ _ hrs
        obtain ⟨m, a⟩ := ih _ _ _ _ _ _ _ h
        exact ⟨m, fun ev hev =>
          Event.Authorized.mono (fun r hr => lookupAll_mem hrs hr) (Nat.le_refl _)
            (Nat.le_refl _) (a ev hev)⟩
      · cases h
    | log c e =>
      simp only [eval]
      refine (ih _ _ _ _).bind fun v σ₁ _ _ => ?_
      split
      · rename_i id hid
        exact EffPost.single (List.mem_of_getElem? hid)
      · intro o σ' tr h; cases h
    | fetch c e =>
      simp only [eval]
      refine (ih _ _ _ _).bind fun v σ₁ _ _ => ?_
      split
      · rename_i id hid
        exact EffPost.single (List.mem_of_getElem? hid)
      · intro o σ' tr h; cases h
    | transaction d body =>
      intro o σ' tr h
      simp only [eval] at h
      split at h
      · rename_i id hid
        have hih := ih env (.tx σ.next :: ρ) ⟨σ.next + 1, true⟩ body
        have hd : RCap.res id ∈ ρ := List.mem_of_getElem? hid
        -- events of the body, re-authorized w.r.t. the outer environment
        have lift : ∀ (σ₂ : St) (tr₂ : List Event),
            (σ.next + 1 ≤ σ₂.next ∧
              ∀ ev ∈ tr₂, ev.Authorized (.tx σ.next :: ρ) (σ.next + 1) σ₂.next) →
            ∀ ev ∈ tr₂, ev.Authorized ρ σ.next σ₂.next := by
          intro σ₂ tr₂ ⟨_, a⟩ ev hev
          have := a ev hev
          cases ev <;> simp only [Event.Authorized, List.mem_cons] at this ⊢
          · rcases this with h | h
            · cases h
            · exact h
          · rcases this with h | h
            · cases h
            · exact h
          · rcases this with ⟨h | h, h2⟩
            · cases h
            · exact ⟨h, by omega⟩
          all_goals
            rcases this with (h | h) | h
            · cases h; exact .inr ⟨Nat.le_refl _, by omega⟩
            · exact .inl h
            · exact .inr ⟨by omega, h.2⟩
        split at h
        · rename_i v σ₂ tr₂ he
          split at h
          · cases h
          · cases h
            obtain ⟨m, a⟩ := hih _ _ _ he
            have m' : σ.next + 1 ≤ σ₂.next := m
            refine ⟨show σ.next ≤ σ₂.next by omega, fun ev hev => ?_⟩
            simp only [List.mem_cons] at hev
            rcases hev with rfl | hev
            · exact ⟨hd, Nat.le_refl _, show σ.next < σ₂.next by omega⟩
            · exact lift σ₂ tr₂ ⟨m, a⟩ ev hev
        · rename_i σ₂ tr₂ he
          cases h
          obtain ⟨m, a⟩ := hih _ _ _ he
          have m' : σ.next + 1 ≤ σ₂.next := m
          refine ⟨show σ.next ≤ σ₂.next by omega, fun ev hev => ?_⟩
          simp only [List.mem_cons, List.mem_append] at hev
          rcases hev with rfl | hev | hev
          · exact ⟨hd, Nat.le_refl _, show σ.next < σ₂.next by omega⟩
          · exact lift σ₂ tr₂ ⟨m, a⟩ ev hev
          · split at hev
            · simp at hev; subst hev
              exact .inr ⟨Nat.le_refl _, show σ.next < σ₂.next by omega⟩
            · simp at hev
        · rename_i hne1 hne2
          cases o <;> simp_all
      · cases h
    | store op c args =>
      intro o σ' tr h
      simp only [eval] at h
      split at h
      · rename_i t _ ht _
        split at h
        · cases h
          exact ⟨Nat.le_refl _, fun ev hev => by
            simp at hev; subst hev; exact .inl (List.mem_of_getElem? ht)⟩
        · cases h
      · cases h
    | commit c =>
      intro o σ' tr h
      simp only [eval] at h
      split at h
      · rename_i t ht
        split at h
        · split at h <;> cases h <;>
            exact ⟨Nat.le_refl _, fun ev hev => by
              simp at hev; subst hev; exact .inl (List.mem_of_getElem? ht)⟩
        · cases h
      · cases h
    | rollback c =>
      intro o σ' tr h
      simp only [eval] at h
      split at h
      · rename_i t ht
        split at h
        · cases h
          exact ⟨Nat.le_refl _, fun ev hev => by
            simp at hev; subst hev; exact .inl (List.mem_of_getElem? ht)⟩
        · cases h
      · cases h
    | abort =>
      intro o σ' tr h; simp only [eval] at h; cases h; simp
    | len a =>
      intro o σ' tr h; simp only [eval] at h; split at h <;> cases h; simp
    | index a i =>
      intro o σ' tr h; simp only [eval] at h; split at h
      · obtain ⟨rfl, rfl, _⟩ := indexResult_done h; simp
      · cases h
    | wrap e =>
      simp only [eval]
      refine (ih _ _ _ _).bind fun v σ₁ _ _ => ?_
      intro o σ' tr h; cases h; simp
    | lbind e body =>
      simp only [eval]
      refine (ih _ _ _ _).bind fun v σ₁ _ _ => ?_
      split
      · intro o σ' tr h
        obtain ⟨o', h'⟩ := lbindResult_done h
        obtain ⟨m, a⟩ := ih _ [] σ₁ body _ _ _ h'
        exact ⟨m, fun ev hev =>
          Event.Authorized.mono (fun r hr => by simp at hr) (Nat.le_refl _) (Nat.le_refl _)
            (a ev hev)⟩
      · intro o σ' tr h; cases h; simp
      · intro o σ' tr h; cases h

/-- The provided resource capability an event uses, if any. -/
def Event.resource : Event → Option Nat
  | .log c _ => some c
  | .fetch c _ => some c
  | .txBegin d _ => some d
  | _ => none

/-- The transaction an event belongs to, if any. -/
def Event.txId : Event → Option Nat
  | .txBegin _ t => some t
  | .txOp t _ _ => some t
  | .txCommit t => some t
  | .txRollback t => some t
  | _ => none

/-- **Theorem 2 (effect soundness).** When an entry function is run with the
resource capabilities `caps`, every event of the trace uses only a capability from
`caps`, and every transaction event refers to a transaction created during the
run (an id below the final counter). No typing assumption is needed. -/
theorem effect_soundness (O : Oracle) (P : Prog) {n f : Nat} {vs : List Val}
    {caps : List Nat} {o : Outcome} {σ : St} {tr : List Event}
    (h : runFun O P n f vs caps = .done o σ tr) :
    ∀ ev ∈ tr, (∀ c, ev.resource = some c → c ∈ caps) ∧
      (∀ t, ev.txId = some t → t < σ.next) := by
  simp only [runFun] at h
  split at h
  · rename_i fd _
    obtain ⟨_, a⟩ := eval_effects O P n vs (caps.map .res) St.init fd.body _ _ _ h
    intro ev hev
    have := a ev hev
    cases ev <;> simp_all [Event.Authorized, Event.resource, Event.txId, St.init]
  · cases h

/-- With no capabilities at all, evaluation produces no events (and does not
touch the state). -/
theorem eval_no_caps (O : Oracle) (P : Prog) :
    ∀ (n : Nat) (env : List Val) (σ : St) (e : Expr) {o σ' tr},
      eval O P n env [] σ e = .done o σ' tr → tr = [] ∧ σ' = σ := by
  intro n
  induction n with
  | zero => intro env σ e o σ' tr h; simp [eval] at h
  | succ n ih =>
    intro env σ e o σ' tr h
    -- a helper for `bind`
    have hb : ∀ {r : Result} {k : Val → St → Result} {σ₀ : St},
        (∀ {o σ' tr}, r = .done o σ' tr → tr = [] ∧ σ' = σ₀) →
        (∀ {v σ₁ tr₁ o σ' tr}, r = .done (.ok v) σ₁ tr₁ → k v σ₁ = .done o σ' tr →
          tr = [] ∧ σ' = σ₁) →
        ∀ {o σ' tr}, r.bind k = .done o σ' tr → tr = [] ∧ σ' = σ₀ := by
      intro r k σ₀ hr hk o σ' tr h
      rcases Result.bind_eq_done.mp h with ⟨h1, _⟩ | ⟨v, σ₁, tr₁, tr₂, h1, h2, rfl⟩
      · exact hr h1
      · obtain ⟨rfl, rfl⟩ := hr h1
        obtain ⟨rfl, rfl⟩ := hk h1 h2
        exact ⟨rfl, rfl⟩
    cases e with
    | val v => simp only [eval] at h; cases h; exact ⟨rfl, rfl⟩
    | var i => simp only [eval] at h; split at h <;> cases h; exact ⟨rfl, rfl⟩
    | let_ e₁ e₂ =>
      simp only [eval] at h
      exact hb (fun h => ih _ _ _ h) (fun _ h => ih _ _ _ h) h
    | ite c t f =>
      simp only [eval] at h
      refine hb (fun h => ih _ _ _ h) (fun _ h => ?_) h
      split at h
      · exact ih _ _ _ h
      · exact ih _ _ _ h
      · cases h
    | bin op a b =>
      simp only [eval] at h
      refine hb (fun h => ih _ _ _ h) (fun _ h => ?_) h
      refine hb (fun h => ih _ _ _ h) (fun _ h => ?_) h
      obtain ⟨rfl, rfl, _⟩ := binResult_done h; exact ⟨rfl, rfl⟩
    | call f args cs =>
      simp only [eval] at h
      split at h
      · rename_i hrs
        rw [lookupAll_nil hrs] at h
        exact ih _ _ _ h
      · cases h
    | log c e | fetch c e =>
      simp only [eval] at h
      refine hb (fun h => ih _ _ _ h) (fun _ h => ?_) h
      split at h
      · rename_i hc; simp at hc
      · cases h
    | store op c args =>
      simp only [eval] at h
      split at h
      · rename_i hc _; simp at hc
      · cases h
    | transaction d body | commit d | rollback d =>
      simp only [eval] at h
      split at h
      · rename_i hc; simp at hc
      · cases h
    | abort => simp only [eval] at h; cases h; exact ⟨rfl, rfl⟩
    | len a => simp only [eval] at h; split at h <;> cases h; exact ⟨rfl, rfl⟩
    | index a i =>
      simp only [eval] at h; split at h
      · obtain ⟨rfl, rfl, _⟩ := indexResult_done h; exact ⟨rfl, rfl⟩
      · cases h
    | wrap e =>
      simp only [eval] at h
      exact hb (fun h => ih _ _ _ h) (fun _ h => by cases h; exact ⟨rfl, rfl⟩) h
    | lbind e body =>
      simp only [eval] at h
      refine hb (fun h => ih _ _ _ h) (fun _ h => ?_) h
      split at h
      · obtain ⟨_, h'⟩ := lbindResult_done h; exact ih _ _ _ h'
      · cases h; exact ⟨rfl, rfl⟩
      · cases h

/-- **Corollary (purity).** A well-typed call of a function that takes no
capability parameters produces an empty trace. -/
theorem pure_call_no_effects (O : Oracle) {P : Prog} {Γ Δ s f args cs τ s'} {fd : FunDef}
    (ht : HasType P Γ Δ s (.call f args cs) τ s') (hf : P[f]? = some fd) (hpure : fd.caps = [])
    {n : Nat} {env : List Val} {ρ : List RCap} {σ : St} {o σ' tr}
    (h : eval O P n env ρ σ (.call f args cs) = .done o σ' tr) : tr = [] := by
  cases ht with
  | call hf' _ hcs _ =>
    rw [hf] at hf'; cases hf'
    rw [hpure] at hcs
    have hcs' : cs = [] := by
      cases cs with
      | nil => rfl
      | cons c cs =>
        obtain ⟨_, _, _, _, h⟩ := lookupAll_cons.mp hcs
        cases h
    subst hcs'
    cases n with
    | zero => simp [eval] at h
    | succ n =>
      simp only [eval] at h
      split at h
      · rename_i hrs
        simp [lookupAll] at hrs
        subst hrs
        exact (eval_no_caps O P _ _ _ _ h).1
      · cases h

end Kekkai
