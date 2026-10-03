import Kekkai.Typing
import Kekkai.Semantics
import Kekkai.Safety

/-!
# Examples

Sanity checks: a well-typed handler, its evaluation by the reference interpreter,
and programs that the type system rejects (forgotten commit, double commit,
`fetch` inside a transaction).
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

end Kekkai.Examples
