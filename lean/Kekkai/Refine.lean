import Kekkai.Typing
import Kekkai.Semantics
import Kekkai.Pred
import Kekkai.Safety

/-!
# Refinement layer: verification conditions

The judgment

```
Ref P Φ e Ψ
```

says: under the facts `Φ` (predicates over the value context of `e`), all
verification conditions (VCs) of `e` are valid, and when `e` returns a value `r`,
`Ψ` holds, where `Ψ` is a predicate over `r :: context` (atom index `0` is the
result, `i + 1` is variable `i`).

It sits on top of `HasType`: refinement safety (`Kekkai.refinement_safety`) only
needs `Ref`, and together with `HasType` (`Kekkai.refined_safety`) a program
neither gets stuck nor faults.

Each VC is a premise `Entails Φ' p` — semantic validity over the integers, i.e.
what the compiler's solver is trusted to establish. The VCs, matching
`docs/refinement.md` (検証条件と事実):

| construct | VC |
| --- | --- |
| `call f args` | the callee's precondition, instantiated with the arguments (`Ref.call`) |
| function body | the postcondition (`WTRefFun`) |
| `a[i]` | `0 ≤ i ∧ i < len a` (`Ref.index`) |
| `/`, `%` | divisor `≠ 0` (and, for `/`, not `MIN / -1`) (`opVC`) |
| `+`, `-`, `*` | the result is in `[-2^63, 2^63)` (`opVC`) |

and the facts:

* the precondition (`WTRefFun`), `0 ≤ len a` (`Ref.lenNonneg`);
* path conditions of `ite` (`Ref.ite`: the condition, negated in the `else`
  branch);
* `let x = e`: the facts about the result of `e` (`Ref.let_`; for a linear `e`
  this includes `x == e`);
* the result of a call: the callee's postcondition (`Ref.call`);
* the result of an operator: `opFact` (e.g. `r == a + b`, or `r == 1 ↔ a < b`).

Facts about intermediate results are eliminated by entailment (`Ref.conseq`, and
the result premises of `Ref.ite`/`Ref.bin`), so that facts are always predicates
over the current context.
-/

namespace Kekkai

open Term (v ln c)

/-- `lo ≤ t ≤ hi` for the 64-bit range. -/
def inI64 (t : Term) : Pred := .and (.le (.c i64Min) t) (.le t (.c i64Max))

/-- The VC of an operator, over `rb :: ra :: _` (index `1` is the left operand,
`0` the right one). -/
def opVC : BinOp → Pred
  | .add => inI64 (.add (v 1) (v 0))
  | .sub => inI64 (.sub (v 1) (v 0))
  | .mul => inI64 (.mul (v 1) (v 0))
  | .div => .and (.ne (v 0) (c 0)) (.not (.and (.eq (v 1) (c i64Min)) (.eq (v 0) (c (-1)))))
  | .mod => .ne (v 0) (c 0)
  | .lt | .le | .eq | .ne => .tt

/-- A boolean result `r` (index `0`) reflects `p`. -/
def boolFact (p : Pred) : Pred := .or (.and (.eq (v 0) (c 1)) p) (.and (.eq (v 0) (c 0)) (.not p))

/-- The fact about the result of an operator, over `r :: rb :: ra :: _`. -/
def opFact : BinOp → Pred
  | .add => .eq (v 0) (.add (v 2) (v 1))
  | .sub => .eq (v 0) (.sub (v 2) (v 1))
  | .mul => .eq (v 0) (.mul (v 2) (v 1))
  | .div | .mod => .tt
  | .lt => boolFact (.lt (v 2) (v 1))
  | .le => boolFact (.le (v 2) (v 1))
  | .eq => boolFact (.eq (v 2) (v 1))
  | .ne => boolFact (.ne (v 2) (v 1))

/-- The fact about a literal result. -/
def valFact : Val → Pred
  | .unit => .tt
  | .bool b => .eq (v 0) (c (if b then 1 else 0))
  | .int k => .eq (v 0) (c k)
  | .arr xs => .eq (ln 0) (c xs.length)

/-- The refinement judgment (verification conditions hold). -/
inductive Ref (P : Prog) : Facts → Expr → Pred → Prop where
  /-- weaken the result facts (consequence) -/
  | conseq {Φ e Ψ Ψ'} : Ref P Φ e Ψ → Entails (Φ.shift ++ [Ψ]) Ψ' → Ref P Φ e Ψ'
  /-- array lengths are non-negative -/
  | lenNonneg {Φ e Ψ} (i : Nat) : Ref P (Φ ++ [.le (c 0) (ln i)]) e Ψ → Ref P Φ e Ψ
  | val {Φ w} : Ref P Φ (.val w) (valFact w)
  | var {Φ i} : Ref P Φ (.var i) (.and (.eq (v 0) (v (i + 1))) (.eq (ln 0) (ln (i + 1))))
  /-- `let x = e₁ in e₂`: the facts `Ψ₁` about `x` are available in `e₂`; the
  result facts `Ψ` must not mention `x` (`Ψ.up1` skips it). -/
  | let_ {Φ e₁ e₂ Ψ₁ Ψ} :
      Ref P Φ e₁ Ψ₁ → Ref P (Φ.shift ++ [Ψ₁]) e₂ Ψ.up1 → Ref P Φ (.let_ e₁ e₂) Ψ
  /-- path conditions: `pT` (resp. `pF`) is what the condition being `true`
  (resp. `false`) tells about the context. -/
  | ite {Φ c' t e Ψc pT pF Ψ} :
      Ref P Φ c' Ψc →
      Entails (Φ.shift ++ [Ψc, .eq (v 0) (c 1)]) pT.shift →
      Entails (Φ.shift ++ [Ψc, .eq (v 0) (c 0)]) pF.shift →
      Ref P (Φ ++ [pT]) t Ψ → Ref P (Φ ++ [pF]) e Ψ → Ref P Φ (.ite c' t e) Ψ
  /-- operators: the operator's VC over the two operand results (`rb :: ra :: _`),
  and result facts `Ψ` entailed by `opFact` (over `r :: rb :: ra :: _`). -/
  | bin {Φ op a b Ψa Ψb Ψ} :
      Ref P Φ a Ψa → Ref P Φ b Ψb →
      Entails (Φ.shift.shift ++ [Ψa.shift, Ψb.up1]) (opVC op) →
      Entails (Φ.shift.shift.shift ++ [Ψa.shift.shift, Ψb.up1.shift, opFact op]) Ψ.up1.up1 →
      Ref P Φ (.bin op a b) Ψ
  /-- calls: the callee's precondition is a VC, its postcondition a fact. -/
  | call {Φ f args cs fd} :
      P[f]? = some fd → args.length = fd.params.length →
      Entails Φ (fd.pre.substArgs args) →
      Ref P Φ (.call f args cs) (fd.post.substRes args)
  | log {Φ c' e Ψ} : Ref P Φ e Ψ → Ref P Φ (.log c' e) .tt
  | fetch {Φ c' e Ψ} : Ref P Φ e Ψ → Ref P Φ (.fetch c' e) .tt
  | transaction {Φ d body Ψ} : Ref P Φ body Ψ → Ref P Φ (.transaction d body) Ψ
  | store {Φ op c' args} : Ref P Φ (.store op c' args) .tt
  | commit {Φ c'} : Ref P Φ (.commit c') .tt
  | rollback {Φ c'} : Ref P Φ (.rollback c') .tt
  | abort {Φ Ψ} : Ref P Φ .abort Ψ
  | len {Φ a} : Ref P Φ (.len a) (.eq (v 0) (ln (a + 1)))
  /-- indexing: the bounds check is a VC. -/
  | index {Φ a i} :
      Entails Φ (.and (.le (c 0) (v i)) (.lt (v i) (ln a))) → Ref P Φ (.index a i) .tt

/-- A function satisfies its contract: its precondition and postcondition talk
only about its parameters (and the result), and under the precondition the body's
VCs hold and its result satisfies the postcondition. -/
def WTRefFun (P : Prog) (fd : FunDef) : Prop :=
  fd.pre.wf fd.params.length = true ∧ fd.post.wf (fd.params.length + 1) = true ∧
    Ref P [fd.pre] fd.body fd.post

/-- Every function of the program satisfies its contract. -/
def WTRefProg (P : Prog) : Prop := ∀ (f : Nat) (fd : FunDef), P[f]? = some fd → WTRefFun P fd

/-! ## Soundness -/

/-- Postcondition of the refinement soundness lemma: no fault, and a returned
value satisfies `Ψ`. -/
def RefPost (Ψ : Pred) (env : List Val) : Result → Prop
  | .fault _ => False
  | .done (.ok w) _ _ => Ψ.holds (toI (w :: env))
  | _ => True

theorem RefPost.ne_fault {Ψ env r} (h : RefPost Ψ env r) (f : Fault) : r ≠ .fault f := by
  rintro rfl; exact h

theorem RefPost.map {Ψ Ψ' : Pred} {env env' : List Val} {r : Result}
    (hm : ∀ w, Ψ.holds (toI (w :: env)) → Ψ'.holds (toI (w :: env'))) :
    RefPost Ψ env r → RefPost Ψ' env' r := by
  cases r with
  | done o σ tr => cases o with
    | ok w => exact hm w
    | err => exact id
  | _ => exact id

theorem RefPost.prepend {Ψ env r tr} (h : RefPost Ψ env r) : RefPost Ψ env (r.prepend tr) := by
  cases r with
  | done o σ tr' => cases o <;> exact h
  | _ => exact h

theorem RefPost.bind {Ψ₁ Ψ : Pred} {env : List Val} {r : Result} {k : Val → St → Result}
    (hr : RefPost Ψ₁ env r)
    (hk : ∀ w σ tr, r = .done (.ok w) σ tr → Ψ₁.holds (toI (w :: env)) → RefPost Ψ env (k w σ)) :
    RefPost Ψ env (r.bind k) := by
  cases r with
  | done o σ tr =>
    cases o with
    | ok w => exact (hk w σ tr rfl hr).prepend
    | err => trivial
  | timeout | stuck => trivial
  | fault => exact hr

theorem toI_cons_zero {w : Val} {env : List Val} : toI (w :: env) (.var 0) = w.toInt := rfl

theorem toI_len_nonneg (env : List Val) (i : Nat) : 0 ≤ toI env (.len i) := by
  simp only [toI]
  split
  · rename_i w _; cases w <;> simp [Val.length]
  · exact Int.le_refl 0

theorem opVC_sound {op : BinOp} {a b : Int} {env : List Val}
    (h : (opVC op).holds (toI (.int b :: .int a :: env))) : ∃ w, arith op a b = .ok w := by
  cases op <;>
    simp only [opVC, inI64, Pred.ne, Pred.holds, Term.eval, toI, List.getElem?_cons_zero,
      List.getElem?_cons_succ, Val.toInt] at h <;>
    simp only [arith, checkI64]
  all_goals (try split) <;> (try split) <;> first | exact ⟨_, rfl⟩ | (exfalso; omega)

theorem opFact_sound {op : BinOp} {a b : Int} {w : Val} {env : List Val}
    (h : arith op a b = .ok w) : (opFact op).holds (toI (w :: .int b :: .int a :: env)) := by
  cases op <;> simp only [arith, checkI64] at h
  all_goals first
    | (cases h; simp [opFact, boolFact, Pred.ne, Pred.holds, Term.eval, toI, Val.toInt] <;> omega)
    | (split at h <;> cases h; simp [opFact, Pred.holds, Term.eval, toI, Val.toInt])
    | (split at h; · cases h
       split at h <;> cases h; simp [opFact, Pred.holds])

/-- **Refinement soundness (core lemma).** Under facts that hold of the runtime
environment, an expression whose VCs hold never faults, and its result satisfies
the result facts — for any fuel, oracle and capabilities. No typing assumption is
needed (a dynamic type error is `stuck`, not a fault). -/
theorem eval_refine (O : Oracle) {P : Prog} (hR : WTRefProg P) :
    ∀ (n : Nat) {Φ : Facts} {e : Expr} {Ψ : Pred} {env : List Val} {ρ : List RCap} {σ : St},
      Ref P Φ e Ψ → HoldsAll (toI env) Φ → RefPost Ψ env (eval O P n env ρ σ e) := by
  intro n
  induction n with
  | zero => intros; simp [eval, RefPost]
  | succ n ih =>
    intro Φ e Ψ env ρ σ h
    induction h generalizing env ρ σ with
    | conseq _ hE ih' =>
      intro hΦ
      refine RefPost.map (fun w hw => hE.apply ?_) (ih' hΦ)
      simp only [holdsAll_append, holdsAll_cons, holdsAll_shift]
      exact ⟨hΦ, hw, holdsAll_nil⟩
    | lenNonneg i _ ih' =>
      intro hΦ
      refine ih' ?_
      simp only [holdsAll_append, holdsAll_cons]
      exact ⟨hΦ, toI_len_nonneg env i, holdsAll_nil⟩
    | @val _ w =>
      intro _
      simp only [eval, RefPost]
      cases w <;> simp [valFact, Pred.holds, Term.eval, toI, Val.toInt, Val.length]
    | var =>
      intro _
      simp only [eval]
      split
      · rename_i i w hw
        simp [RefPost, Pred.holds, Term.eval, toI, hw]
      · trivial
    | let_ h₁ h₂ _ _ =>
      intro hΦ
      simp only [eval]
      refine RefPost.bind (ih h₁ hΦ) fun w σ₁ _ _ hw => ?_
      refine RefPost.map (fun w' hw' => holds_up1.mp hw') (ih h₂ ?_)
      simp only [holdsAll_append, holdsAll_cons, holdsAll_shift]
      exact ⟨hΦ, hw, holdsAll_nil⟩
    | ite hc hT hF ht hf _ _ _ =>
      intro hΦ
      simp only [eval]
      refine RefPost.bind (ih hc hΦ) fun w σ₁ _ _ hw => ?_
      split
      · refine ih ht ?_
        refine holdsAll_append.mpr ⟨hΦ, holdsAll_cons.mpr ⟨(holds_shift (v := Val.bool true)).mp (hT.apply ?_), holdsAll_nil⟩⟩
        simp only [holdsAll_append, holdsAll_cons, holdsAll_shift]
        exact ⟨hΦ, hw, rfl, holdsAll_nil⟩
      · refine ih hf ?_
        refine holdsAll_append.mpr ⟨hΦ, holdsAll_cons.mpr ⟨(holds_shift (v := Val.bool false)).mp (hF.apply ?_), holdsAll_nil⟩⟩
        simp only [holdsAll_append, holdsAll_cons, holdsAll_shift]
        exact ⟨hΦ, hw, rfl, holdsAll_nil⟩
      · trivial
    | bin ha hb hvc hfact _ _ =>
      intro hΦ
      simp only [eval]
      refine RefPost.bind (ih ha hΦ) fun w₁ σ₁ _ _ hw₁ => ?_
      refine RefPost.bind (ih hb hΦ) fun w₂ σ₂ _ _ hw₂ => ?_
      cases w₁ with
      | int x =>
        cases w₂ with
        | int y =>
        have hv := hvc.apply (I := toI (.int y :: .int x :: env)) (by
          simp only [holdsAll_append, holdsAll_cons, holdsAll_shift, holds_shift, holds_up1]
          exact ⟨hΦ, hw₁, hw₂, holdsAll_nil⟩)
        obtain ⟨w, hw⟩ := opVC_sound hv
        simp only [binResult, hw, RefPost]
        have := hfact.apply (I := toI (w :: .int y :: .int x :: env)) (by
          simp only [holdsAll_append, holdsAll_cons, holdsAll_shift, holds_shift, holds_up1]
          exact ⟨hΦ, hw₁, hw₂, opFact_sound hw, holdsAll_nil⟩)
        exact holds_up1.mp (holds_up1.mp this)
        | _ => simp [binResult, RefPost]
      | _ => simp [binResult, RefPost]
    | @call _ f args cs fd hf hlen hpre =>
      intro hΦ
      simp only [eval]
      split
      · rename_i fd' vs rs hf' hvs _
        rw [hf] at hf'; cases hf'
        obtain ⟨hwpre, hwpost, hbody⟩ := hR f fd hf
        refine RefPost.map (fun w hw => ?_) (ih hbody (env := vs) ?_)
        · exact (holds_substRes hvs (by rw [hlen]; exact hwpost)).mpr hw
        · exact holdsAll_cons.mpr
            ⟨(holds_substArgs hvs (by rw [hlen]; exact hwpre)).mp (hpre.apply hΦ), holdsAll_nil⟩
      · trivial
    | log he _ | fetch he _ =>
      intro hΦ
      simp only [eval]
      refine RefPost.bind (ih he hΦ) fun _ _ _ _ _ => ?_
      split <;> trivial
    | @transaction _ d body _ hb _ =>
      intro hΦ
      simp only [eval]
      split
      · have := ih hb hΦ (ρ := .tx σ.next :: ρ) (σ := ⟨σ.next + 1, true⟩)
        revert this
        cases eval O P n env (.tx σ.next :: ρ) ⟨σ.next + 1, true⟩ body with
        | done o σ₂ tr =>
          cases o with
          | ok w => intro h; simp only; split <;> first | trivial | exact h
          | err => intro; trivial
        | _ => exact id
      · trivial
    | store | commit | rollback =>
      intro _
      simp only [eval]
      repeat (first | trivial | split)
    | abort => intro _; trivial
    | len =>
      intro _
      simp only [eval]
      split
      · rename_i a xs hxs
        simp [RefPost, Pred.holds, Term.eval, toI, hxs, Val.toInt, Val.length]
      · trivial
    | @index _ a i hb =>
      intro hΦ
      simp only [eval]
      split
      · rename_i xs k hxs hk
        have := hb.apply hΦ
        simp only [Pred.holds, Term.eval, toI, hxs, hk, Val.toInt, Val.length] at this
        simp [indexResult, this, RefPost, Pred.holds]
      · trivial

/-! ## Main theorems -/

/-- **Refinement safety.** If every function of the program satisfies its
contract (its VCs hold), then running a function on arguments that satisfy its
precondition never faults: no division by zero, no index out of range, no
overflow — for any fuel, oracle and capabilities. -/
theorem refinement_safety (O : Oracle) {P : Prog} (hR : WTRefProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) {vs : List Val} (hpre : fd.pre.holds (toI vs))
    (caps : List Nat) (n : Nat) (fl : Fault) : runFun O P n f vs caps ≠ .fault fl := by
  simp only [runFun, hf]
  exact (eval_refine O hR n (hR f fd hf).2.2 (holdsAll_cons.mpr ⟨hpre, holdsAll_nil⟩)).ne_fault fl

/-- **Postconditions hold.** Under the same assumptions, a returned value
satisfies the function's postcondition (index `0` of `post` is the result, `i + 1`
the `i`-th argument). -/
theorem postcondition_holds (O : Oracle) {P : Prog} (hR : WTRefProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) {vs : List Val} (hpre : fd.pre.holds (toI vs))
    {caps : List Nat} {n : Nat} {w : Val} {σ : St} {tr : List Event}
    (h : runFun O P n f vs caps = .done (.ok w) σ tr) : fd.post.holds (toI (w :: vs)) := by
  simp only [runFun, hf] at h
  have := eval_refine O hR n (ρ := caps.map .res) (σ := St.init) (hR f fd hf).2.2
    (holdsAll_cons.mpr ⟨hpre, holdsAll_nil⟩)
  rw [h] at this
  exact this

/-- The individual guarantees recorded by `kek assure` (`refine.no_div_zero`,
`refine.index_safe`, `refine.no_overflow`). -/
theorem no_div_by_zero (O : Oracle) {P : Prog} (hR : WTRefProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) {vs : List Val} (hpre : fd.pre.holds (toI vs)) (caps : List Nat)
    (n : Nat) : runFun O P n f vs caps ≠ .fault .divByZero :=
  refinement_safety O hR hf hpre caps n _

theorem index_safe (O : Oracle) {P : Prog} (hR : WTRefProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) {vs : List Val} (hpre : fd.pre.holds (toI vs)) (caps : List Nat)
    (n : Nat) : runFun O P n f vs caps ≠ .fault .outOfBounds :=
  refinement_safety O hR hf hpre caps n _

theorem no_overflow (O : Oracle) {P : Prog} (hR : WTRefProg P) {f : Nat} {fd : FunDef}
    (hf : P[f]? = some fd) {vs : List Val} (hpre : fd.pre.holds (toI vs)) (caps : List Nat)
    (n : Nat) : runFun O P n f vs caps ≠ .fault .overflow :=
  refinement_safety O hR hf hpre caps n _

/-- **Type safety + refinement safety.** A well-typed program whose VCs hold,
run on well-typed arguments satisfying the entry function's precondition, neither
gets stuck nor faults; a returned value has the declared type and satisfies the
postcondition. The only way not to return is running out of fuel or `abort`. -/
theorem refined_safety (O : Oracle) {P : Prog} (hP : WTProg P) (hR : WTRefProg P) {f : Nat}
    {fd : FunDef} (hf : P[f]? = some fd) (hntx : CapKind.tx ∉ fd.caps)
    {vs : List Val} {caps : List Nat} (hvs : EnvOk vs fd.params)
    (hcaps : CapsOk (caps.map .res) (fd.caps.map some)) (hpre : fd.pre.holds (toI vs))
    (n : Nat) :
    runFun O P n f vs caps ≠ .stuck ∧ (∀ fl, runFun O P n f vs caps ≠ .fault fl) ∧
      ∀ w σ tr, runFun O P n f vs caps = .done (.ok w) σ tr →
        ValTy w fd.ret ∧ fd.post.holds (toI (w :: vs)) := by
  obtain ⟨h1, h2⟩ := type_safety O hP hf hntx hvs hcaps n
  exact ⟨h1, refinement_safety O hR hf hpre caps n,
    fun w σ tr h => ⟨h2 w σ tr h, postcondition_holds O hR hf hpre h⟩⟩

end Kekkai
