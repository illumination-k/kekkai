import Kekkai.Typing
import Kekkai.Semantics
import Kekkai.Safety
import Kekkai.Effects

/-!
# Theorem 9: information flow — non-interference for labeled values (P2)

`Labeled<L, T>` is the value type `Ty.lab τ` (all labels are collapsed into a
single secret level). Its contents can only be used inside the body of
`lbind e body` (`Labeled::map` / `zip` / `and_then`), and that body is checked
with an *empty* capability context and without a transaction: it is pure. So the
"pc" of a labeled computation is the label itself, and nothing it does is
observable except through its (labeled) result. Outside `lbind`, a labeled value
can only be moved around (`let`, arguments, results): `ite`, `bin`, `len`,
`index` need public types, and `log`, `fetch` and store operations only accept
public values.

**Low equivalence.** Two values are low-equivalent (`LowEq`) when they are equal
or both labeled: an observer sees every value except the contents of labeled
ones.

**Theorem (termination-insensitive non-interference, `noninterference`).** Run an
entry function of a well-typed program twice, with the same capabilities and the
same oracle, on well-typed arguments that agree on every non-labeled parameter
(`PubAgree`). If **both** runs terminate (`done`, with any amounts of fuel), then
they produce the same trace (the same events: logs, fetches, transactions and
their store operations), the same final state, and low-equivalent outcomes: both
return or both `abort`, and returned values are low-equivalent — equal when the
return type is public (`noninterference_public`).

Runs that do not terminate are excluded (*termination-insensitive*): a labeled
computation may loop (`timeout`) or fault (division by zero, …) depending on the
secret, which is an observable termination channel. Faults are ruled out anyway
for programs that pass the refinement layer (`refinement_safety`). An `abort`
inside a labeled computation does not leak: it cannot escape the `lbind` (the
result is the poisoned labeled value `labErr`).

Declassification (`mask`, `hash`, `expose_unchecked` in the surface language) is
deliberately not part of the calculus: programs using it are outside this
guarantee.
-/

namespace Kekkai

/-! ## Low equivalence -/

/-- Low equivalence of values: equal, or both labeled. -/
def LowEq (v w : Val) : Prop := v = w ∨ (v.isLab = true ∧ w.isLab = true)

theorem LowEq.refl (v : Val) : LowEq v v := .inl rfl

/-- Low equivalence of value environments. -/
abbrev EnvLow (env₁ env₂ : List Val) : Prop := Forall2 LowEq env₁ env₂

/-- Two argument lists agree on every parameter of a public (non-labeled) type. -/
def PubAgree (Γ : List Ty) (vs₁ vs₂ : List Val) : Prop :=
  ∀ (i : Nat) (τ : Ty), Γ[i]? = some τ → τ.pub = true → vs₁[i]? = vs₂[i]?

/-- Low equivalence of outcomes at type `τ`: both return low-equivalent values of
type `τ`, or both abort. -/
def OutLow (τ : Ty) : Outcome → Outcome → Prop
  | .ok v, .ok w => ValTy v τ ∧ ValTy w τ ∧ LowEq v w
  | .err, .err => True
  | _, _ => False

theorem ValTy.isLab_of_pub {v : Val} {τ : Ty} (h : ValTy v τ) (hp : τ.pub = true) :
    v.isLab = false := by
  cases h <;> first | rfl | cases hp

theorem ValTy.isLab_of_lab {v : Val} {τ : Ty} (h : ValTy v (.lab τ)) : v.isLab = true := by
  cases h <;> rfl

/-- At a public type, low equivalence is equality. -/
theorem LowEq.eq_of_pub {v w : Val} {τ : Ty} (h : LowEq v w) (hv : ValTy v τ)
    (hp : τ.pub = true) : v = w := by
  rcases h with h | ⟨h, _⟩
  · exact h
  · rw [hv.isLab_of_pub hp] at h; cases h

theorem OutLow.eq_of_pub {τ : Ty} {o₁ o₂ : Outcome} (h : OutLow τ o₁ o₂) (hp : τ.pub = true) :
    o₁ = o₂ := by
  cases o₁ <;> cases o₂ <;> simp only [OutLow] at h
  · obtain ⟨hv, _, hl⟩ := h; rw [hl.eq_of_pub hv hp]
  · rfl

theorem Forall2.get₂ {α β : Type} {R : α → β → Prop} :
    ∀ {l₁ : List α} {l₂ : List β} {i : Nat} {a : α} {b : β},
      Forall2 R l₁ l₂ → l₁[i]? = some a → l₂[i]? = some b → R a b
  | _, _, _, _, _, .nil, h, _ => by simp at h
  | _, _, 0, _, _, .cons hab _, h₁, h₂ => by
      simp at h₁ h₂; subst h₁; subst h₂; exact hab
  | _, _, _ + 1, _, _, .cons _ hl, h₁, h₂ => by
      simp at h₁ h₂; exact Forall2.get₂ hl h₁ h₂

theorem lookupAll_forall2₂ {α β : Type} {R : α → β → Prop} {l₁ : List α} {l₂ : List β}
    (hl : Forall2 R l₁ l₂) :
    ∀ {is : List Nat} {as : List α} {bs : List β},
      lookupAll l₁ is = some as → lookupAll l₂ is = some bs → Forall2 R as bs
  | [], as, bs, h₁, h₂ => by
      simp [lookupAll] at h₁ h₂; subst h₁; subst h₂; exact .nil
  | _ :: _, _, _, h₁, h₂ => by
      obtain ⟨a, as', ha, has, rfl⟩ := lookupAll_cons.mp h₁
      obtain ⟨b, bs', hb, hbs, rfl⟩ := lookupAll_cons.mp h₂
      exact .cons (hl.get₂ ha hb) (lookupAll_forall2₂ hl has hbs)

/-- Low-equivalent values of public types are equal. -/
theorem eq_of_pub_all {vs ws : List Val} (hl : EnvLow vs ws) :
    ∀ {tys : List Ty}, Forall2 ValTy vs tys → (∀ t ∈ tys, t.pub = true) → vs = ws := by
  induction hl with
  | nil => intros; rfl
  | cons h _ ih =>
    intro tys hv hp
    cases hv with
    | cons hv hvs =>
      rw [h.eq_of_pub hv (hp _ (List.mem_cons_self ..)),
        ih hvs (fun t ht => hp t (List.mem_cons_of_mem _ ht))]

/-- Agreeing on the public parameters is low equivalence (for well-typed
arguments). -/
theorem envLow_of_pubAgree {Γ : List Ty} {vs₁ vs₂ : List Val} (h₁ : EnvOk vs₁ Γ) :
    EnvOk vs₂ Γ → PubAgree Γ vs₁ vs₂ → EnvLow vs₁ vs₂ := by
  induction h₁ generalizing vs₂ with
  | nil => intro h₂ _; cases h₂; exact .nil
  | @cons v τ _ _ hv _ ih =>
    intro h₂ hp
    cases h₂ with
    | @cons w _ _ _ hw hws =>
      refine .cons ?_ (ih hws fun i τ' hi hpub => hp (i + 1) τ' hi hpub)
      cases hpub : τ.pub
      · cases τ <;>
          first
          | exact .inr ⟨hv.isLab_of_lab, hw.isLab_of_lab⟩
          | simp [Ty.pub] at hpub
      · have := hp 0 τ rfl hpub
        simp only [List.getElem?_cons_zero, Option.some.injEq] at this
        exact .inl this

/-! ## The relation on pairs of results -/

/-- Postcondition of the non-interference lemma: if both results are `done`, they
have the same trace and state, and low-equivalent outcomes. -/
def NIPost (τ : Ty) (r₁ r₂ : Result) : Prop :=
  ∀ o₁ σ₁ tr₁ o₂ σ₂ tr₂, r₁ = .done o₁ σ₁ tr₁ → r₂ = .done o₂ σ₂ tr₂ →
    tr₁ = tr₂ ∧ σ₁ = σ₂ ∧ OutLow τ o₁ o₂

theorem NIPost.bind {τ₁ τ₂ : Ty} {r₁ r₂ : Result} {k₁ k₂ : Val → St → Result}
    (hr : NIPost τ₁ r₁ r₂)
    (hk : ∀ v₁ v₂ σ tr₁ tr₂, r₁ = .done (.ok v₁) σ tr₁ → r₂ = .done (.ok v₂) σ tr₂ →
      ValTy v₁ τ₁ → ValTy v₂ τ₁ → LowEq v₁ v₂ → NIPost τ₂ (k₁ v₁ σ) (k₂ v₂ σ)) :
    NIPost τ₂ (r₁.bind k₁) (r₂.bind k₂) := by
  intro o₁ σ₁ tr₁ o₂ σ₂ tr₂ h₁ h₂
  rcases Result.bind_eq_done.mp h₁ with ⟨g₁, rfl⟩ | ⟨v₁, σ₁', tr₁', tr₁'', g₁, g₁', rfl⟩ <;>
    rcases Result.bind_eq_done.mp h₂ with ⟨g₂, rfl⟩ | ⟨v₂, σ₂', tr₂', tr₂'', g₂, g₂', rfl⟩
  · obtain ⟨rfl, rfl, _⟩ := hr _ _ _ _ _ _ g₁ g₂; exact ⟨rfl, rfl, trivial⟩
  · exact (hr _ _ _ _ _ _ g₁ g₂).2.2.elim
  · exact (hr _ _ _ _ _ _ g₁ g₂).2.2.elim
  · obtain ⟨rfl, rfl, hv₁, hv₂, hlow⟩ := hr _ _ _ _ _ _ g₁ g₂
    obtain ⟨rfl, rfl, ho⟩ := hk _ _ _ _ _ g₁ g₂ hv₁ hv₂ hlow _ _ _ _ _ _ g₁' g₂'
    exact ⟨rfl, rfl, ho⟩

/-- Identical results whose returned values have type `τ`. -/
theorem NIPost.same {τ : Ty} {r : Result}
    (h : ∀ v σ tr, r = .done (.ok v) σ tr → ValTy v τ) : NIPost τ r r := by
  intro o₁ σ₁ tr₁ o₂ σ₂ tr₂ h₁ h₂
  rw [h₁] at h₂; cases h₂
  cases o₁ with
  | ok v => exact ⟨rfl, rfl, h v σ₁ tr₁ h₁, h v σ₁ tr₁ h₁, .refl v⟩
  | err => exact ⟨rfl, rfl, trivial⟩

/-- A result that, if `done`, has no events, leaves the state `σ` unchanged and
returns a labeled value. -/
def LabRes (τ : Ty) (σ : St) (r : Result) : Prop :=
  ∀ o σ' tr, r = .done o σ' tr → tr = [] ∧ σ' = σ ∧ ∃ w, o = .ok w ∧ ValTy w (.lab τ)

theorem NIPost.of_labRes {τ : Ty} {σ : St} {r₁ r₂ : Result} (h₁ : LabRes τ σ r₁)
    (h₂ : LabRes τ σ r₂) : NIPost (.lab τ) r₁ r₂ := by
  intro o₁ σ₁ tr₁ o₂ σ₂ tr₂ e₁ e₂
  obtain ⟨rfl, rfl, w₁, rfl, hw₁⟩ := h₁ _ _ _ e₁
  obtain ⟨rfl, rfl, w₂, rfl, hw₂⟩ := h₂ _ _ _ e₂
  exact ⟨rfl, rfl, hw₁, hw₂, .inr ⟨hw₁.isLab_of_lab, hw₂.isLab_of_lab⟩⟩

theorem labRes_labErr {τ : Ty} {σ : St} : LabRes τ σ (.done (.ok .labErr) σ []) := by
  intro o σ' tr h; cases h; exact ⟨rfl, rfl, _, rfl, .labErr _⟩

/-- **The body of a labeled computation is unobservable.** It is typed with no
capability, so it emits no event and does not change the state
(`eval_no_caps`); whatever it returns (or if it aborts) the result is labeled. -/
theorem labRes_body (O : Oracle) {P : Prog} (hP : WTProg P) {Γ : List Ty} {τ τ' : Ty}
    {body : Expr} (hb : HasType P (τ :: Γ) [] .none body (.lab τ') .none) {k : Nat}
    {env : List Val} {a : Val} (henv : EnvOk env Γ) (ha : ValTy a τ) (σ : St) :
    LabRes τ' σ (lbindResult (eval O P k (a :: env) [] σ body)) := by
  have hs := eval_safe O hP k hb (.cons ha henv) .nil
    ⟨nofun, nofun, fun _ c hc => by simp at hc⟩ (σ := σ)
  intro o σ' tr h
  obtain ⟨o', h'⟩ := lbindResult_done h
  obtain ⟨rfl, rfl⟩ := eval_no_caps O P _ _ _ _ h'
  rw [h'] at hs h
  refine ⟨rfl, rfl, ?_⟩
  cases o' with
  | ok w => cases h; exact ⟨w, rfl, (hs.2 w rfl).1⟩
  | err => cases h; exact ⟨_, rfl, .labErr _⟩

theorem binResult_ty {op : BinOp} {a b : Int} {σ σ' : St} {v : Val} {tr : List Event}
    (h : binResult op (.int a) (.int b) σ = .done (.ok v) σ' tr) : ValTy v op.resTy := by
  simp only [binResult] at h
  split at h
  · rename_i w hw; cases h; exact arith_ty hw
  · cases h

/-! ## The core lemma -/

/-- **Non-interference (core lemma).** Evaluating a well-typed expression in two
low-equivalent, well-typed value environments — with the same capabilities `ρ`
and state `σ`, and any amounts of fuel — gives, when both evaluations terminate,
the same trace, the same final state and low-equivalent outcomes. -/
theorem eval_ni (O : Oracle) {P : Prog} (hP : WTProg P) :
    ∀ (n₁ : Nat) {Γ Δ s e τ s'} {n₂ : Nat} {env₁ env₂ : List Val} {ρ : List RCap} {σ : St},
      HasType P Γ Δ s e τ s' → EnvOk env₁ Γ → EnvOk env₂ Γ → EnvLow env₁ env₂ →
      NIPost τ (eval O P n₁ env₁ ρ σ e) (eval O P n₂ env₂ ρ σ e) := by
  intro n₁
  induction n₁ with
  | zero => intros; intro _ _ _ _ _ _ h; simp [eval] at h
  | succ n ih =>
    intro Γ Δ s e τ s' n₂ env₁ env₂ ρ σ ht h₁ h₂ hl
    cases n₂ with
    | zero => intro _ _ _ _ _ _ _ h; simp [eval] at h
    | succ m =>
    cases ht with
    | val hv =>
      simp only [eval]
      exact NIPost.same fun _ _ _ h => by cases h; exact hv
    | var hi =>
      obtain ⟨v₁, hv₁, hvt₁⟩ := h₁.get hi
      obtain ⟨v₂, hv₂, hvt₂⟩ := h₂.get hi
      simp only [eval, hv₁, hv₂]
      intro _ _ _ _ _ _ e₁ e₂; cases e₁; cases e₂
      exact ⟨rfl, rfl, hvt₁, hvt₂, hl.get₂ hv₁ hv₂⟩
    | let_ ha hb =>
      simp only [eval]
      exact NIPost.bind (ih ha h₁ h₂ hl) fun _ _ _ _ _ _ _ hv₁ hv₂ hlow =>
        ih hb (.cons hv₁ h₁) (.cons hv₂ h₂) (.cons hlow hl)
    | ite hc ht hf =>
      simp only [eval]
      refine NIPost.bind (ih hc h₁ h₂ hl) fun v₁ v₂ σ₁ _ _ _ _ hv₁ _ hlow => ?_
      obtain rfl := hlow.eq_of_pub hv₁ rfl
      cases hv₁ with
      | bool b =>
        cases b
        · exact ih hf h₁ h₂ hl
        · exact ih ht h₁ h₂ hl
    | bin ha hb =>
      simp only [eval]
      refine NIPost.bind (ih ha h₁ h₂ hl) fun v₁ v₂ σ₁ _ _ _ _ hv₁ _ hlow => ?_
      obtain rfl := hlow.eq_of_pub hv₁ rfl
      refine NIPost.bind (ih hb h₁ h₂ hl) fun w₁ w₂ σ₂ _ _ _ _ hw₁ _ hlow' => ?_
      obtain rfl := hlow'.eq_of_pub hw₁ rfl
      cases hv₁; cases hw₁
      exact NIPost.same fun _ _ _ h => binResult_ty h
    | @call _ _ _ f args cs fd hf hargs _ _ =>
      obtain ⟨vs₁, hvs₁, hvt₁⟩ := lookupAll_forall2 h₁ hargs
      obtain ⟨vs₂, hvs₂, hvt₂⟩ := lookupAll_forall2 h₂ hargs
      cases hrs : lookupAll ρ cs with
      | none =>
        simp only [eval, hf, hvs₁, hvs₂, hrs]
        intro _ _ _ _ _ _ e; cases e
      | some rs =>
        simp only [eval, hf, hvs₁, hvs₂, hrs]
        exact ih (hP f fd hf) hvt₁ hvt₂ (lookupAll_forall2₂ hl hvs₁ hvs₂)
    | log _ he hp =>
      simp only [eval]
      refine NIPost.bind (ih he h₁ h₂ hl) fun v₁ v₂ σ₁ _ _ _ _ hv₁ _ hlow => ?_
      obtain rfl := hlow.eq_of_pub hv₁ hp
      exact NIPost.same fun _ _ _ h => by split at h <;> cases h; exact .unit
    | fetch _ he hp =>
      simp only [eval]
      refine NIPost.bind (ih he h₁ h₂ hl) fun v₁ v₂ σ₁ _ _ _ _ hv₁ _ hlow => ?_
      obtain rfl := hlow.eq_of_pub hv₁ hp
      exact NIPost.same fun _ _ _ h => by split at h <;> cases h; exact .int _
    | @transaction _ _ _ d body _ _ hb =>
      cases hd : ρ[d]? with
      | none =>
        simp only [eval, hd]
        intro _ _ _ _ _ _ e; cases e
      | some r =>
        cases r with
        | tx t =>
          simp only [eval, hd]
          intro _ _ _ _ _ _ e; cases e
        | res id =>
          simp only [eval, hd]
          have hni := ih hb h₁ h₂ hl (n₂ := m) (ρ := .tx σ.next :: ρ) (σ := ⟨σ.next + 1, true⟩)
          revert hni
          generalize eval O P n env₁ (.tx σ.next :: ρ) ⟨σ.next + 1, true⟩ body = r₁
          generalize eval O P m env₂ (.tx σ.next :: ρ) ⟨σ.next + 1, true⟩ body = r₂
          intro hni o₁ σ₁ tr₁ o₂ σ₂ tr₂ e₁ e₂
          cases r₁ with
          | done a₁ s₁ t₁ =>
            cases r₂ with
            | done a₂ s₂ t₂ =>
              obtain ⟨rfl, rfl, ho⟩ := hni _ _ _ _ _ _ rfl rfl
              cases a₁ <;> cases a₂ <;> simp only [OutLow] at ho
              · cases hl₂ : s₁.live <;> simp only [hl₂, Bool.false_eq_true, ite_true, ite_false]
                  at e₁ e₂ <;> cases e₁ <;> cases e₂
                exact ⟨rfl, rfl, ho⟩
              · cases e₁; cases e₂; exact ⟨rfl, rfl, trivial⟩
            | _ => cases e₂
          | _ => cases e₁
    | @store _ _ _ op c args _ _ hargs _ hpub =>
      obtain ⟨vs₁, hvs₁, hvt₁⟩ := lookupAll_forall2 h₁ hargs
      obtain ⟨vs₂, hvs₂, _⟩ := lookupAll_forall2 h₂ hargs
      obtain rfl := eq_of_pub_all (lookupAll_forall2₂ hl hvs₁ hvs₂) hvt₁ hpub
      simp only [eval, hvs₁, hvs₂]
      refine NIPost.same fun _ _ _ h => ?_
      split at h
      · split at h
        · cases h; exact storeResult_ty O _ _ _
        · cases h
      · cases h
    | commit _ =>
      simp only [eval]
      refine NIPost.same fun _ _ _ h => ?_
      split at h
      · split at h
        · split at h <;> cases h <;> exact .bool _
        · cases h
      · cases h
    | rollback _ =>
      simp only [eval]
      refine NIPost.same fun _ _ _ h => ?_
      split at h
      · split at h
        · cases h; exact .unit
        · cases h
      · cases h
    | abort _ =>
      simp only [eval]
      exact NIPost.same fun _ _ _ h => by cases h
    | len ha =>
      obtain ⟨v₁, hv₁, hvt₁⟩ := h₁.get ha
      obtain ⟨v₂, hv₂, _⟩ := h₂.get ha
      obtain rfl := (hl.get₂ hv₁ hv₂).eq_of_pub hvt₁ rfl
      cases hvt₁
      simp only [eval, hv₁, hv₂]
      exact NIPost.same fun _ _ _ h => by cases h; exact .int _
    | index ha hi =>
      obtain ⟨v₁, hv₁, hvt₁⟩ := h₁.get ha
      obtain ⟨v₂, hv₂, _⟩ := h₂.get ha
      obtain ⟨w₁, hw₁, hwt₁⟩ := h₁.get hi
      obtain ⟨w₂, hw₂, _⟩ := h₂.get hi
      obtain rfl := (hl.get₂ hv₁ hv₂).eq_of_pub hvt₁ rfl
      obtain rfl := (hl.get₂ hw₁ hw₂).eq_of_pub hwt₁ rfl
      cases hvt₁; cases hwt₁
      simp only [eval, hv₁, hw₁, hv₂, hw₂]
      refine NIPost.same fun _ _ _ h => ?_
      simp only [indexResult] at h
      split at h
      · cases h; exact .int _
      · cases h
    | wrap he =>
      simp only [eval]
      refine NIPost.bind (ih he h₁ h₂ hl) fun v₁ v₂ σ₁ _ _ _ _ hv₁ hv₂ _ => ?_
      intro _ _ _ _ _ _ e₁ e₂; cases e₁; cases e₂
      exact ⟨rfl, rfl, .lab hv₁, .lab hv₂, .inr ⟨rfl, rfl⟩⟩
    | lbind he hb =>
      simp only [eval]
      refine NIPost.bind (ih he h₁ h₂ hl) fun v₁ v₂ σ₁ _ _ _ _ hv₁ hv₂ _ => ?_
      cases hv₁ <;> cases hv₂ <;> dsimp only <;> refine NIPost.of_labRes (σ := σ₁) ?_ ?_ <;>
        first
        | exact labRes_labErr
        | exact labRes_body O hP hb h₁ ‹_› σ₁
        | exact labRes_body O hP hb h₂ ‹_› σ₁

/-! ## Main theorem -/

/-- **Theorem 9 (non-interference, termination-insensitive).** Run an entry
function of a well-typed program twice — with the same oracle and the same
capabilities, on well-typed arguments that agree on every parameter whose type is
not labeled (`PubAgree`). If both runs terminate (with any amounts of fuel), they
have the same trace (every observable effect: logs, fetches, transactions and
store operations, commits/rollbacks), the same final state, and low-equivalent
outcomes (both return, or both `abort`; the returned values agree up to their
labeled parts). So public outputs do not depend on labeled inputs. -/
theorem noninterference (O : Oracle) {P : Prog} (hP : WTProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) {vs₁ vs₂ : List Val} (hvs₁ : EnvOk vs₁ fd.params)
    (hvs₂ : EnvOk vs₂ fd.params) (hpub : PubAgree fd.params vs₁ vs₂) (caps : List Nat)
    {n₁ n₂ : Nat} {o₁ o₂ : Outcome} {σ₁ σ₂ : St} {tr₁ tr₂ : List Event}
    (h₁ : runFun O P n₁ f vs₁ caps = .done o₁ σ₁ tr₁)
    (h₂ : runFun O P n₂ f vs₂ caps = .done o₂ σ₂ tr₂) :
    tr₁ = tr₂ ∧ σ₁ = σ₂ ∧ OutLow fd.ret o₁ o₂ := by
  simp only [runFun, hf] at h₁ h₂
  exact eval_ni O hP n₁ (hP f fd hf) hvs₁ hvs₂ (envLow_of_pubAgree hvs₁ hvs₂ hpub) _ _ _ _ _ _
    h₁ h₂

/-- **Corollary.** When the return type is public, the two runs return the very
same outcome. -/
theorem noninterference_public (O : Oracle) {P : Prog} (hP : WTProg P) {f : Nat}
    {fd : FunDef} (hf : P[f]? = some fd) (hret : fd.ret.pub = true) {vs₁ vs₂ : List Val}
    (hvs₁ : EnvOk vs₁ fd.params) (hvs₂ : EnvOk vs₂ fd.params)
    (hpub : PubAgree fd.params vs₁ vs₂) (caps : List Nat)
    {n₁ n₂ : Nat} {o₁ o₂ : Outcome} {σ₁ σ₂ : St} {tr₁ tr₂ : List Event}
    (h₁ : runFun O P n₁ f vs₁ caps = .done o₁ σ₁ tr₁)
    (h₂ : runFun O P n₂ f vs₂ caps = .done o₂ σ₂ tr₂) :
    tr₁ = tr₂ ∧ σ₁ = σ₂ ∧ o₁ = o₂ := by
  obtain ⟨h1, h2, h3⟩ := noninterference O hP hf hvs₁ hvs₂ hpub caps h₁ h₂
  exact ⟨h1, h2, h3.eq_of_pub hret⟩

end Kekkai
