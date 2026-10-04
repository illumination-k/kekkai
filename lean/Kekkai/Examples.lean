import Kekkai.Typing
import Kekkai.Semantics
import Kekkai.Safety
import Kekkai.Refine

/-!
# Examples

Sanity checks: a well-typed handler, its evaluation by the reference interpreter,
and programs that the type system rejects (forgotten commit, double commit,
`fetch` inside a transaction).

Refinements (P1): a bounds-checked array read and a caller that establishes its
precondition with an `if`, a division guarded by `if d != 0`, a postcondition
with an overflow check, and programs that are well typed but rejected because a
verification condition fails.
-/

namespace Kekkai.Examples

/-- ```
fn save(amount: Int; db: &Db, log: &Log) -> Bool {
  let ok = db.transaction(|tx| { tx.put(amount); tx.commit() });
  log.info(ok);
  ok
}
```
Value variable 0 = `amount`; capability variables 0 = `db`, 1 = `log`. -/
def save : FunDef where
  params := [.int]
  caps := [.db, .log]
  ret := .bool
  body :=
    .let_ (.transaction 0 (.let_ (.store .put 0 [0]) (.commit 0)))
      (.let_ (.log 1 (.var 0)) (.var 1))

def prog : Prog := [save]

theorem save_wt : WTFun prog save := by
  unfold WTFun save
  refine .let_ (.transaction rfl (.let_ (.store rfl rfl nofun) (.commit rfl)))
    (.let_ (.log rfl (.var rfl)) (.var rfl))

theorem prog_wt : WTProg prog := by
  intro f fd h
  cases f with
  | zero => cases h; exact save_wt
  | succ f => simp [prog] at h

/-- An oracle where every commit succeeds. -/
def okOracle : Oracle := ⟨fun _ _ => 0, fun _ _ => 0, fun _ => true⟩

/-- An oracle where every commit fails (e.g. an optimistic-concurrency conflict). -/
def conflictOracle : Oracle := ⟨fun _ _ => 0, fun _ _ => 0, fun _ => false⟩

/-- Successful run: the capability ids `7` (db) and `8` (log) are provided. -/
example : runFun okOracle prog 10 0 [.int 5] [7, 8] =
    .done (.ok (.bool true)) ⟨1, false⟩
      [.txBegin 7 0, .txOp 0 .put [.int 5], .txCommit 0, .log 8 (.bool true)] := by
  rfl

/-- A failed commit still ends the transaction (as a rollback) exactly once. -/
example : runFun conflictOracle prog 10 0 [.int 5] [7, 8] =
    .done (.ok (.bool false)) ⟨1, false⟩
      [.txBegin 7 0, .txOp 0 .put [.int 5], .txRollback 0, .log 8 (.bool false)] := by
  rfl

/-- `abort` inside a transaction rolls it back automatically. -/
example : eval okOracle [] 10 [] [.res 7] ⟨0, false⟩
    (.transaction 0 (.let_ (.store .put 0 []) .abort)) =
    .done .err ⟨1, false⟩ [.txBegin 7 0, .txOp 0 .put [], .txRollback 0] := by
  rfl

/-- Forgetting to commit is a type error. -/
example (P : Prog) (τ : Ty) (s' : TxSt) :
    ¬ HasType P [] [some .db] .none (.transaction 0 (.val .unit)) τ s' := by
  intro h
  cases h with
  | transaction _ hb => cases hb

/-- Committing twice is a type error. -/
example (P : Prog) (τ : Ty) (s' : TxSt) :
    ¬ HasType P [] [some .db] .none (.transaction 0 (.let_ (.commit 0) (.commit 0))) τ s' := by
  intro h
  cases h with
  | transaction _ hb =>
    cases hb with
    | let_ h₁ h₂ => cases h₁; cases h₂

/-- Using `Net` inside a transaction is a type error: the outer `net`
(capability variable 1 outside, 2 inside) is masked. -/
example (P : Prog) (τ : Ty) (s' : TxSt) :
    ¬ HasType P [] [some .db, some .net] .none
      (.transaction 0 (.let_ (.fetch 2 (.val .unit)) (.commit 0))) τ s' := by
  intro h
  cases h with
  | transaction _ hb =>
    cases hb with
    | let_ h₁ _ =>
      cases h₁ with
      | fetch hc _ => simp [mask, maskSlot] at hc

/-- A store operation after commit is a type error. -/
example (P : Prog) (τ : Ty) (s' : TxSt) :
    ¬ HasType P [] [some .db] .none
      (.transaction 0 (.let_ (.commit 0) (.store .get 0 []))) τ s' := by
  intro h
  cases h with
  | transaction _ hb =>
    cases hb with
    | let_ h₁ h₂ =>
      cases h₁
      cases h₂ with
      | store _ _ hs => exact hs rfl

/-! ## Refinements -/

open Kekkai.Term (v ln c)

/-- Discharge an entailment between concrete predicates (stands in for the
compiler's solver: unfold the semantics, then `omega`). -/
syntax "entails" (" [" Lean.Parser.Tactic.simpLemma,* "]")? : tactic
macro_rules
  | `(tactic| entails) => `(tactic| entails [])
  | `(tactic| entails [$xs,*]) => `(tactic| (
  intro I hI
  simp [$xs,*, HoldsAll, Facts.shift, Pred.shift, Pred.up1, Pred.substArgs, Pred.substRes, Pred.map,
    Term.map, Atom.map, up1, argMap, resMap, Pred.holds, Term.eval, Pred.ne, boolFact, opFact,
    opVC, inI64, valFact, i64Min, i64Max] at hI ⊢
  try omega))

/-- ```
fn get(a: [Int], i: Int) -> Int where 0 <= i, i < a.len() { a[i] }
```
-/
def getFn : FunDef where
  params := [.arr, .int]
  caps := []
  ret := .int
  body := .index 0 1
  pre := .and (.le (c 0) (v 1)) (.lt (v 1) (ln 0))

/-- ```
fn first(a: [Int]) -> Int { let z = 0; if z < a.len() { get(a, z) } else { -1 } }
```
In the body after `let`, variable 0 is `z` and 1 is `a`. -/
def firstFn : FunDef where
  params := [.arr]
  caps := []
  ret := .int
  body := .let_ (.val (.int 0))
    (.ite (.bin .lt (.var 0) (.len 1)) (.call 0 [1, 0] []) (.val (.int (-1))))

def arrProg : Prog := [getFn, firstFn]

theorem arrProg_wt : WTProg arrProg := by
  intro f fd h
  match f, h with
  | 0, h => cases h; exact .index rfl rfl
  | 1, h =>
    cases h
    exact .let_ (.val (.int 0)) (.ite (.bin (.var rfl) (.len rfl)) (.call (fd := getFn) rfl rfl rfl nofun)
      (.val (.int _)))

/-- The bounds check of `get` follows from its precondition. -/
theorem getFn_ref : WTRefFun arrProg getFn :=
  ⟨rfl, rfl, .index (by entails [getFn])⟩

/-- `first` establishes `get`'s precondition from `z == 0` and the path
condition `z < a.len()`. -/
theorem firstFn_ref : WTRefFun arrProg firstFn := by
  refine ⟨rfl, rfl, ?_⟩
  refine .let_ .val ?_
  refine .ite (Ψc := boolFact (.lt (v 1) (ln 2))) (pT := .lt (v 0) (ln 1)) (pF := .tt)
    (.bin .var .len (by entails [getFn, firstFn]) (by entails [getFn, firstFn])) (by entails [getFn, firstFn]) (by entails [getFn, firstFn]) ?_ ?_
  · exact .conseq (.call rfl rfl (by entails [getFn, firstFn])) (by entails [getFn, firstFn])
  · exact .conseq .val (by entails [getFn, firstFn])

theorem arrProg_ref : WTRefProg arrProg := by
  intro f fd h
  match f, h with
  | 0, h => cases h; exact getFn_ref
  | 1, h => cases h; exact firstFn_ref

example : runFun okOracle arrProg 10 1 [.arr [10, 20, 30]] [] = .done (.ok (.int 10)) St.init [] := by
  rfl

example : runFun okOracle arrProg 10 1 [.arr []] [] = .done (.ok (.int (-1))) St.init [] := by
  rfl

/-- Calling `get` directly, outside its precondition, does fault. -/
example : runFun okOracle arrProg 10 0 [.arr [10, 20, 30], .int 3] [] = .fault .outOfBounds := by
  rfl

/-- `first` can never fault nor get stuck, on any array. -/
theorem first_safe (O : Oracle) (xs : List Int) (n : Nat) :
    runFun O arrProg n 1 [.arr xs] [] ≠ .stuck ∧ ∀ fl, runFun O arrProg n 1 [.arr xs] [] ≠ .fault fl :=
  let h := refined_safety O arrProg_wt arrProg_ref (f := 1) rfl nofun (.cons (.arr xs) .nil) .nil
    trivial n
  ⟨h.1, h.2.1⟩

/-- ```
fn safe_div(n: Int, d: Int) -> Int where 0 <= n { if d != 0 { n / d } else { 0 } }
```
`d != 0` rules out the division by zero; `0 <= n` rules out the only overflow
of division, `MIN / -1`. -/
def safeDiv : FunDef where
  params := [.int, .int]
  caps := []
  ret := .int
  body := .ite (.bin .ne (.var 1) (.val (.int 0))) (.bin .div (.var 0) (.var 1)) (.val (.int 0))
  pre := .le (c 0) (v 0)

theorem safeDiv_ref : WTRefFun [safeDiv] safeDiv := by
  refine ⟨rfl, rfl, ?_⟩
  refine .ite (Ψc := boolFact (.ne (v 2) (c 0))) (pT := .ne (v 1) (c 0)) (pF := .tt)
    (.bin .var .val (by entails [safeDiv]) (by entails [safeDiv])) (by entails [safeDiv]) (by entails [safeDiv]) ?_ ?_
  · exact .bin .var .var (by entails [safeDiv]) (by entails [safeDiv])
  · exact .conseq .val (by entails [safeDiv])

example : runFun okOracle [safeDiv] 10 0 [.int 7, .int 2] [] = .done (.ok (.int 3)) St.init [] := by
  rfl

example : runFun okOracle [safeDiv] 10 0 [.int 7, .int 0] [] = .done (.ok (.int 0)) St.init [] := by
  rfl

/-- ```
fn abs(x: Int) -> Int where MIN < x, 0 <= result { if x < 0 { 0 - x } else { x } }
```
The subtraction's no-overflow VC needs the precondition `MIN < x`. -/
def absFn : FunDef where
  params := [.int]
  caps := []
  ret := .int
  body := .ite (.bin .lt (.var 0) (.val (.int 0))) (.bin .sub (.val (.int 0)) (.var 0)) (.var 0)
  pre := .lt (c i64Min) (v 0)
  post := .le (c 0) (v 0)

theorem absFn_ref : WTRefFun [absFn] absFn := by
  refine ⟨rfl, rfl, ?_⟩
  refine .ite (Ψc := boolFact (.lt (v 1) (c 0))) (pT := .lt (v 0) (c 0))
    (pF := .not (.lt (v 0) (c 0)))
    (.bin .var .val (by entails [absFn]) (by entails [absFn])) (by entails [absFn]) (by entails [absFn]) ?_ ?_
  · exact .bin .val .var (by entails [absFn]) (by entails [absFn])
  · exact .conseq .var (by entails [absFn])

example : runFun okOracle [absFn] 10 0 [.int (-5)] [] = .done (.ok (.int 5)) St.init [] := by
  rfl

/-- Outside the precondition, `0 - MIN` overflows. -/
example : runFun okOracle [absFn] 10 0 [.int i64Min] [] = .fault .overflow := by
  rfl

/-- The postcondition, for every run on an admissible input. -/
example (O : Oracle) (x : Int) (hx : i64Min < x) (n : Nat) (w : Val) (σ : St) (tr : List Event)
    (h : runFun O [absFn] n 0 [.int x] [] = .done (.ok w) σ tr) : 0 ≤ w.toInt := by
  have := postcondition_holds O (P := [absFn]) (f := 0)
    (fun f fd h => by match f, h with | 0, h => cases h; exact absFn_ref) rfl
    (by simpa [absFn, Pred.holds, Term.eval, toI, Val.toInt] using hx) h
  simpa [absFn, Pred.holds, Term.eval, toI] using this

/-! ### Rejected programs

`badDiv` and `badGet` are well typed, but their VCs fail: there is a
counterexample (the solver's model), and no refinement derivation exists at all,
because the program does fault on an input satisfying the (trivial)
precondition — which `refinement_safety` rules out. -/

/-- `fn bad_div(n: Int, d: Int) -> Int { n / d }` -/
def badDiv : FunDef where
  params := [.int, .int]
  caps := []
  ret := .int
  body := .bin .div (.var 0) (.var 1)

theorem badDiv_wt : WTProg [badDiv] := by
  intro f fd h
  match f, h with
  | 0, h => cases h; exact .bin (.var rfl) (.var rfl)

/-- The division VC of `bad_div` is not valid: `d = 0` is a counterexample. -/
example : ¬ Entails (Facts.shift (Facts.shift [Pred.tt]) ++
    [(Pred.and (.eq (v 0) (v 1)) (.eq (ln 0) (ln 1))).shift,
     (Pred.and (.eq (v 0) (v 2)) (.eq (ln 0) (ln 2))).up1]) (opVC .div) := by
  intro h
  have := h (fun _ => 0) (by simp [HoldsAll, Facts.shift, Pred.shift, Pred.up1, Pred.map,
    Term.map, Atom.map, up1, Pred.holds, Term.eval])
  simp [opVC, Pred.ne, Pred.holds, Term.eval] at this

/-- `bad_div` is rejected by the refinement layer. -/
theorem badDiv_rejected : ¬ WTRefProg [badDiv] := by
  intro hR
  exact refinement_safety okOracle hR (f := 0) (vs := [.int 1, .int 0]) rfl trivial [] 10
    .divByZero rfl

/-- `fn bad_get(a: [Int], i: Int) -> Int { a[i] }` -/
def badGet : FunDef where
  params := [.arr, .int]
  caps := []
  ret := .int
  body := .index 0 1

theorem badGet_wt : WTProg [badGet] := by
  intro f fd h
  match f, h with
  | 0, h => cases h; exact .index rfl rfl

/-- The bounds VC of `bad_get` is not valid: `i = 0`, `a.len() = 0` is a
counterexample. -/
example : ¬ Entails [Pred.tt] (.and (.le (c 0) (v 1)) (.lt (v 1) (ln 0))) := by
  intro h
  have := h (fun _ => 0) (by simp [HoldsAll, Pred.holds])
  simp [Pred.holds, Term.eval] at this

/-- `bad_get` is rejected by the refinement layer. -/
theorem badGet_rejected : ¬ WTRefProg [badGet] := by
  intro hR
  exact refinement_safety okOracle hR (f := 0) (vs := [.arr [1, 2], .int 2]) rfl trivial [] 10
    .outOfBounds rfl

end Kekkai.Examples
