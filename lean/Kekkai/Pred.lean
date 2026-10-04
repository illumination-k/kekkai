import Kekkai.Syntax
import Kekkai.Basic

/-!
# Meaning of refinement predicates; entailment

Predicates (`Kekkai.Pred`, defined in `Kekkai.Syntax`) are interpreted in an
*integer environment* `IEnv := Atom → Int`: every atom (`var i`, `len i`) is an
opaque integer, exactly as the compiler's solver sees them (`"x"`, `"len(v)"`).

`Entails Φ p` — "the verification condition `Φ ⟹ p` is valid" — is defined
**semantically**: every integer environment satisfying all facts `Φ` satisfies
`p`. Nothing about the solver is trusted in the proofs; the trusted assumption is
only that when the compiler's Omega-test solver answers `Valid` for
`smt_prove(Φ, p)`, then `Entails Φ p` holds.

A runtime value environment `env : List Val` induces the integer environment
`toI env` (`var i` = the integer, or `0`/`1` for a boolean; `len i` = the array
length). Since `Entails` quantifies over *all* integer environments, it is
stronger than "holds for every runtime environment"; in particular facts such as
`0 ≤ len i` must be stated explicitly (rule `Ref.lenNonneg`).

The section on renaming (`Pred.map`) provides the de Bruijn bookkeeping: shifting
under a binder, inserting the result variable, and substituting arguments for
parameters at calls.
-/

namespace Kekkai

/-- Integer environments: a value for every atom. -/
abbrev IEnv := Atom → Int

def Term.eval (I : IEnv) : Term → Int
  | .const k => k
  | .atom a => I a
  | .add t u => t.eval I + u.eval I
  | .sub t u => t.eval I - u.eval I
  | .mul t u => t.eval I * u.eval I

def Pred.holds (I : IEnv) : Pred → Prop
  | .tt => True
  | .ff => False
  | .le t u => t.eval I ≤ u.eval I
  | .lt t u => t.eval I < u.eval I
  | .eq t u => t.eval I = u.eval I
  | .not p => ¬ p.holds I
  | .and p q => p.holds I ∧ q.holds I
  | .or p q => p.holds I ∨ q.holds I

/-- A list of facts (hypotheses), read as a conjunction. -/
abbrev Facts := List Pred

/-- All facts hold. -/
def HoldsAll (I : IEnv) (Φ : Facts) : Prop := ∀ p ∈ Φ, p.holds I

/-- **Semantic entailment** (validity of the verification condition `Φ ⟹ p`
over the integers). This is what the compiler's solver is assumed to decide
soundly. -/
def Entails (Φ : Facts) (p : Pred) : Prop := ∀ I : IEnv, HoldsAll I Φ → p.holds I

/-! ## Handy predicate constructors -/

namespace Pred
def ne (t u : Term) : Pred := .not (.eq t u)
def imp (p q : Pred) : Pred := .or (.not p) q
end Pred

namespace Term
/-- The atom `var i` as a term. -/
abbrev v (i : Nat) : Term := .atom (.var i)
/-- The atom `len i` as a term. -/
abbrev ln (i : Nat) : Term := .atom (.len i)
/-- An integer constant. -/
abbrev c (k : Int) : Term := .const k
end Term

/-! ## Renaming atoms -/

def Atom.idx : Atom → Nat
  | .var i => i
  | .len i => i

def Atom.map (f : Nat → Nat) : Atom → Atom
  | .var i => .var (f i)
  | .len i => .len (f i)

def Term.map (f : Nat → Nat) : Term → Term
  | .const k => .const k
  | .atom a => .atom (a.map f)
  | .add t u => .add (t.map f) (u.map f)
  | .sub t u => .sub (t.map f) (u.map f)
  | .mul t u => .mul (t.map f) (u.map f)

def Pred.map (f : Nat → Nat) : Pred → Pred
  | .tt => .tt
  | .ff => .ff
  | .le t u => .le (t.map f) (u.map f)
  | .lt t u => .lt (t.map f) (u.map f)
  | .eq t u => .eq (t.map f) (u.map f)
  | .not p => .not (p.map f)
  | .and p q => .and (p.map f) (q.map f)
  | .or p q => .or (p.map f) (q.map f)

theorem Term.eval_map (f : Nat → Nat) (I : IEnv) :
    ∀ t : Term, (t.map f).eval I = t.eval (fun a => I (a.map f))
  | .const _ => rfl
  | .atom _ => rfl
  | .add t u => by simp [Term.map, Term.eval, Term.eval_map f I t, Term.eval_map f I u]
  | .sub t u => by simp [Term.map, Term.eval, Term.eval_map f I t, Term.eval_map f I u]
  | .mul t u => by simp [Term.map, Term.eval, Term.eval_map f I t, Term.eval_map f I u]

theorem Pred.holds_map (f : Nat → Nat) (I : IEnv) :
    ∀ p : Pred, (p.map f).holds I ↔ p.holds (fun a => I (a.map f))
  | .tt | .ff => Iff.rfl
  | .le t u | .lt t u | .eq t u => by
      simp only [Pred.map, Pred.holds, Term.eval_map]
  | .not p => by simp only [Pred.map, Pred.holds, Pred.holds_map f I p]
  | .and p q => by simp only [Pred.map, Pred.holds, Pred.holds_map f I p, Pred.holds_map f I q]
  | .or p q => by simp only [Pred.map, Pred.holds, Pred.holds_map f I p, Pred.holds_map f I q]

theorem holdsAll_map (f : Nat → Nat) (I : IEnv) (Φ : Facts) :
    HoldsAll I (Φ.map (Pred.map f)) ↔ HoldsAll (fun a => I (a.map f)) Φ := by
  simp only [HoldsAll, List.mem_map, forall_exists_index, and_imp, forall_apply_eq_imp_iff₂,
    Pred.holds_map]

theorem holdsAll_append {I : IEnv} {Φ Ψ : Facts} :
    HoldsAll I (Φ ++ Ψ) ↔ HoldsAll I Φ ∧ HoldsAll I Ψ := by
  simp only [HoldsAll, List.mem_append]
  constructor
  · intro h; exact ⟨fun p hp => h p (.inl hp), fun p hp => h p (.inr hp)⟩
  · rintro ⟨h1, h2⟩ p (hp | hp)
    · exact h1 p hp
    · exact h2 p hp

theorem holdsAll_cons {I : IEnv} {p : Pred} {Φ : Facts} :
    HoldsAll I (p :: Φ) ↔ p.holds I ∧ HoldsAll I Φ := by
  simp [HoldsAll]

theorem holdsAll_nil {I : IEnv} : HoldsAll I [] := nofun

theorem Entails.apply {Φ : Facts} {p : Pred} {I : IEnv} (h : Entails Φ p) (hI : HoldsAll I Φ) :
    p.holds I := h I hI

/-! ## De Bruijn bookkeeping -/

/-- Shift under a new innermost binder. -/
def Pred.shift (p : Pred) : Pred := p.map Nat.succ
def Facts.shift (Φ : Facts) : Facts := Φ.map Pred.shift

/-- Insert a binder at position `1` (just under the result variable `0`). -/
def up1 : Nat → Nat
  | 0 => 0
  | j + 1 => j + 2

def Pred.up1 (p : Pred) : Pred := p.map Kekkai.up1

/-- Parameter `j` of a callee is the caller's variable `args[j]`. -/
def argMap (args : List Nat) (j : Nat) : Nat := args.getD j 0

/-- Same, under the result variable `0`. -/
def resMap (args : List Nat) : Nat → Nat
  | 0 => 0
  | j + 1 => args.getD j 0 + 1

/-- A callee's precondition, instantiated with the arguments. -/
def Pred.substArgs (p : Pred) (args : List Nat) : Pred := p.map (argMap args)
/-- A callee's postcondition, instantiated with the arguments. -/
def Pred.substRes (p : Pred) (args : List Nat) : Pred := p.map (resMap args)

/-! ## Runtime environments as integer environments -/

/-- The integer an atom `var i` denotes for a value. -/
def Val.toInt : Val → Int
  | .int n => n
  | .bool b => if b then 1 else 0
  | _ => 0

/-- The integer an atom `len i` denotes for a value. -/
def Val.length : Val → Int
  | .arr xs => xs.length
  | _ => 0

/-- The integer environment of a runtime value environment. -/
def toI (env : List Val) : IEnv
  | .var i => match env[i]? with
    | some v => v.toInt
    | none => 0
  | .len i => match env[i]? with
    | some v => v.length
    | none => 0

theorem toI_map_eq {env env' : List Val} {f : Nat → Nat} {n : Nat}
    (h : ∀ i, i < n → env'[f i]? = env[i]?) :
    ∀ a : Atom, a.idx < n → toI env' (a.map f) = toI env a
  | .var i, hi | .len i, hi => by simp only [Atom.map, toI, h i hi]

theorem toI_map_funext {env env' : List Val} {f : Nat → Nat}
    (h : ∀ i, env'[f i]? = env[i]?) : (fun a => toI env' (a.map f)) = toI env := by
  funext a
  exact toI_map_eq (n := a.idx + 1) (fun i _ => h i) a (Nat.lt_succ_self _)

theorem holds_shift {v : Val} {env : List Val} {p : Pred} :
    p.shift.holds (toI (v :: env)) ↔ p.holds (toI env) := by
  rw [Pred.shift, Pred.holds_map, toI_map_funext (f := Nat.succ) (fun i => rfl)]

theorem holdsAll_shift {v : Val} {env : List Val} {Φ : Facts} :
    HoldsAll (toI (v :: env)) Φ.shift ↔ HoldsAll (toI env) Φ := by
  rw [Facts.shift, show Pred.shift = Pred.map Nat.succ from rfl, holdsAll_map,
    toI_map_funext (f := Nat.succ) (fun i => rfl)]

theorem holds_up1 {r v : Val} {env : List Val} {p : Pred} :
    p.up1.holds (toI (r :: v :: env)) ↔ p.holds (toI (r :: env)) := by
  rw [Pred.up1, Pred.holds_map, toI_map_funext]
  intro i; cases i <;> rfl

theorem lookupAll_length {α : Type} {l : List α} :
    ∀ {is : List Nat} {as : List α}, lookupAll l is = some as → as.length = is.length
  | [], as, h => by simp [lookupAll] at h; subst h; rfl
  | i :: is, as, h => by
      obtain ⟨_, _, _, hbs, rfl⟩ := lookupAll_cons.mp h
      simp [lookupAll_length hbs]

theorem lookupAll_getD {α : Type} {l : List α} {is : List Nat} {as : List α}
    (h : lookupAll l is = some as) {j : Nat} (hj : j < is.length) :
    l[is.getD j 0]? = as[j]? := by
  rw [List.getD_eq_getElem?_getD, List.getElem?_eq_getElem hj, Option.getD_some]
  exact (lookupAll_getElem h (List.getElem?_eq_getElem hj)).symm

/-- Every atom index is below `n` (the predicate only talks about the first `n`
variables). -/
def Term.wf (n : Nat) : Term → Bool
  | .const _ => true
  | .atom a => decide (a.idx < n)
  | .add t u | .sub t u | .mul t u => t.wf n && u.wf n

def Pred.wf (n : Nat) : Pred → Bool
  | .tt | .ff => true
  | .le t u | .lt t u | .eq t u => t.wf n && u.wf n
  | .not p => p.wf n
  | .and p q | .or p q => p.wf n && q.wf n

theorem Term.eval_congr {n : Nat} {I J : IEnv} (hIJ : ∀ a : Atom, a.idx < n → I a = J a) :
    ∀ t : Term, t.wf n = true → t.eval I = t.eval J
  | .const _, _ => rfl
  | .atom a, h => by simp only [Term.wf, decide_eq_true_eq] at h; exact hIJ a h
  | .add t u, h | .sub t u, h | .mul t u, h => by
      simp only [Term.wf, Bool.and_eq_true] at h
      simp only [Term.eval, Term.eval_congr hIJ t h.1, Term.eval_congr hIJ u h.2]

theorem Pred.holds_congr {n : Nat} {I J : IEnv} (hIJ : ∀ a : Atom, a.idx < n → I a = J a) :
    ∀ p : Pred, p.wf n = true → (p.holds I ↔ p.holds J)
  | .tt, _ | .ff, _ => Iff.rfl
  | .le t u, h | .lt t u, h | .eq t u, h => by
      simp only [Pred.wf, Bool.and_eq_true] at h
      simp only [Pred.holds, Term.eval_congr hIJ t h.1, Term.eval_congr hIJ u h.2]
  | .not p, h => by
      simp only [Pred.wf] at h
      simp only [Pred.holds, Pred.holds_congr hIJ p h]
  | .and p q, h | .or p q, h => by
      simp only [Pred.wf, Bool.and_eq_true] at h
      simp only [Pred.holds, Pred.holds_congr hIJ p h.1, Pred.holds_congr hIJ q h.2]

/-- Instantiating a callee's precondition with the arguments. -/
theorem holds_substArgs {env vs : List Val} {args : List Nat} {p : Pred}
    (h : lookupAll env args = some vs) (hp : p.wf args.length = true) :
    (p.substArgs args).holds (toI env) ↔ p.holds (toI vs) := by
  rw [Pred.substArgs, Pred.holds_map]
  exact Pred.holds_congr (toI_map_eq fun i hi => lookupAll_getD h hi) p hp

/-- Instantiating a callee's postcondition with the arguments. -/
theorem holds_substRes {v : Val} {env vs : List Val} {args : List Nat} {p : Pred}
    (h : lookupAll env args = some vs) (hp : p.wf (args.length + 1) = true) :
    (p.substRes args).holds (toI (v :: env)) ↔ p.holds (toI (v :: vs)) := by
  rw [Pred.substRes, Pred.holds_map]
  refine Pred.holds_congr (toI_map_eq fun i hi => ?_) p hp
  cases i with
  | zero => rfl
  | succ j =>
    simp only [resMap, List.getElem?_cons_succ]
    exact lookupAll_getD h (by omega)

end Kekkai
