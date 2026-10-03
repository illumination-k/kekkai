import Kekkai.Semantics

/-!
# A trace monitor for transactional discipline

`TxSafe tr` says that the trace `tr` is accepted by a small automaton that tracks
the currently open transaction:

* `txBegin _ t` is allowed only when no transaction is open and `t` was never used
  before; it opens `t`;
* `txOp t …` is allowed only while `t` is open;
* `txCommit t` / `txRollback t` are allowed only while `t` is open; they close it;
* `fetch` (an irrevocable effect) is allowed only when no transaction is open;
* at the end of the trace no transaction is open.

From acceptance we derive the readable trace properties (`TxSafe.linear`):
every `txBegin _ t` is followed by **exactly one** ending event for `t`; between
them there is no `fetch` (and no other ending of `t`); after it, `t` never occurs
again (in particular no `txOp t …`).
-/

namespace Kekkai

/-- Monitor state: the open transaction (if any) and all transaction ids seen. -/
structure Mon where
  active : Option Nat
  seen : List Nat

/-- One step of the monitor; `none` = violation. -/
def Mon.step (m : Mon) : Event → Option Mon
  | .log _ _ => some m
  | .fetch _ _ => if m.active = none then some m else none
  | .txBegin _ t => if m.active = none ∧ t ∉ m.seen then some ⟨some t, t :: m.seen⟩ else none
  | .txOp t _ _ => if m.active = some t then some m else none
  | .txCommit t => if m.active = some t then some ⟨none, m.seen⟩ else none
  | .txRollback t => if m.active = some t then some ⟨none, m.seen⟩ else none

/-- Run the monitor over a trace. -/
def Mon.run : Mon → List Event → Option Mon
  | m, [] => some m
  | m, ev :: tr =>
    match m.step ev with
    | some m' => m'.run tr
    | none => none

/-- Initial monitor state. -/
def Mon.init : Mon := ⟨none, []⟩

/-- The trace respects the transactional discipline. -/
def TxSafe (tr : List Event) : Prop :=
  ∃ m, Mon.init.run tr = some m ∧ m.active = none

theorem Mon.run_cons {m : Mon} {ev : Event} {tr : List Event} {m' : Mon} :
    m.run (ev :: tr) = some m' ↔ ∃ m₁, m.step ev = some m₁ ∧ m₁.run tr = some m' := by
  simp only [Mon.run]
  split <;> simp_all

theorem Mon.run_append {m : Mon} {a b : List Event} {m' : Mon} :
    m.run (a ++ b) = some m' ↔ ∃ m₁, m.run a = some m₁ ∧ m₁.run b = some m' := by
  induction a generalizing m with
  | nil => simp [Mon.run]
  | cons ev a ih =>
    simp only [List.cons_append, Mon.run_cons, ih]
    constructor
    · rintro ⟨m₁, h1, m₂, h2, h3⟩; exact ⟨m₂, ⟨m₁, h1, h2⟩, h3⟩
    · rintro ⟨m₂, ⟨m₁, h1, h2⟩, h3⟩; exact ⟨m₁, h1, m₂, h2, h3⟩

theorem Mon.run_nil {m m' : Mon} : m.run [] = some m' ↔ m' = m := by
  simp [Mon.run, eq_comm]

/-- The event ends transaction `t`. -/
def Event.Ends (t : Nat) : Event → Prop
  | .txCommit t' => t' = t
  | .txRollback t' => t' = t
  | _ => False

/-- The event is an irrevocable effect. -/
def Event.IsIrrevocable : Event → Prop
  | .fetch _ _ => True
  | _ => False

/-- The transaction id mentioned by an event, if any. -/
def Event.tx? : Event → Option Nat
  | .txBegin _ t => some t
  | .txOp t _ _ => some t
  | .txCommit t => some t
  | .txRollback t => some t
  | _ => none

/-- While `t` is open, the monitor reaches a closed state only through an ending
event of `t`, with neither a `fetch` nor another ending of `t` before it. -/
theorem Mon.until_end {t : Nat} :
    ∀ (b : List Event) (m m' : Mon), m.active = some t → t ∈ m.seen → m.run b = some m' →
      m'.active = none →
      ∃ (b₁ : List Event) (e : Event) (b₂ : List Event) (m₂ : Mon), b = b₁ ++ e :: b₂ ∧ e.Ends t ∧
        (∀ x ∈ b₁, ¬ x.Ends t ∧ ¬ x.IsIrrevocable) ∧
        m₂.active = none ∧ t ∈ m₂.seen ∧ m₂.run b₂ = some m'
  | [], m, m', ha, _, hr, hn => by
      rw [Mon.run_nil] at hr; subst hr; rw [ha] at hn; cases hn
  | ev :: b, m, m', ha, hs, hr, hn => by
      obtain ⟨m₁, hst, hr'⟩ := Mon.run_cons.mp hr
      -- events that keep the monitor unchanged
      have keep : m₁ = m → ¬ ev.Ends t → ¬ ev.IsIrrevocable →
          ∃ (b₁ : List Event) (e : Event) (b₂ : List Event) (m₂ : Mon), ev :: b = b₁ ++ e :: b₂ ∧ e.Ends t ∧
            (∀ x ∈ b₁, ¬ x.Ends t ∧ ¬ x.IsIrrevocable) ∧
            m₂.active = none ∧ t ∈ m₂.seen ∧ m₂.run b₂ = some m' := fun hm hne hni => by
        subst hm
        obtain ⟨b₁, e, b₂, m₂, rfl, he, hb₁, h3⟩ := Mon.until_end b m₁ m' ha hs hr' hn
        refine ⟨ev :: b₁, e, b₂, m₂, rfl, he, ?_, h3⟩
        intro x hx
        simp only [List.mem_cons] at hx
        rcases hx with rfl | hx
        · exact ⟨hne, hni⟩
        · exact hb₁ x hx
      cases ev with
      | log c v =>
        simp [Mon.step] at hst
        exact keep hst.symm (fun h => h) (fun h => h)
      | fetch c v => simp [Mon.step, ha] at hst
      | txBegin d t' => simp [Mon.step, ha] at hst
      | txOp t' op vs =>
        simp only [Mon.step] at hst
        split at hst
        · cases hst
          exact keep rfl (fun h => h) (fun h => h)
        · cases hst
      | txCommit t' | txRollback t' =>
        simp only [Mon.step, ha, Option.some.injEq] at hst
        split at hst
        · rename_i h
          subst h; cases hst
          exact ⟨[], _, b, ⟨none, m.seen⟩, rfl, rfl, by simp, rfl, hs, hr'⟩
        · cases hst

/-- After `t` has been closed (it is seen but not open), it never occurs again. -/
theorem Mon.after_end {t : Nat} :
    ∀ (b : List Event) (m m' : Mon), m.active ≠ some t → t ∈ m.seen → m.run b = some m' →
      ∀ x ∈ b, x.tx? ≠ some t
  | [], _, _, _, _, _ => by simp
  | ev :: b, m, m', ha, hs, hr => by
      obtain ⟨m₁, hst, hr'⟩ := Mon.run_cons.mp hr
      intro x hx
      simp only [List.mem_cons] at hx
      cases ev with
      | log c v =>
        simp [Mon.step] at hst; subst hst
        rcases hx with rfl | hx
        · simp [Event.tx?]
        · exact Mon.after_end b _ m' ha hs hr' x hx
      | fetch c v =>
        simp only [Mon.step] at hst
        split at hst <;> cases hst
        rcases hx with rfl | hx
        · simp [Event.tx?]
        · exact Mon.after_end b _ m' ha hs hr' x hx
      | txBegin d t' =>
        simp only [Mon.step] at hst
        split at hst
        · rename_i h
          cases hst
          have hne : t' ≠ t := fun e => h.2 (e ▸ hs)
          rcases hx with rfl | hx
          · simp [Event.tx?, hne]
          · refine Mon.after_end b _ m' ?_ (List.mem_cons_of_mem _ hs) hr' x hx
            simp [hne]
        · cases hst
      | txOp t' op vs =>
        simp only [Mon.step] at hst
        split at hst
        · rename_i h
          cases hst
          have hne : t' ≠ t := fun e => ha (e ▸ h)
          rcases hx with rfl | hx
          · simp [Event.tx?, hne]
          · exact Mon.after_end b _ m' ha hs hr' x hx
        · cases hst
      | txCommit t' | txRollback t' =>
        simp only [Mon.step] at hst
        split at hst
        · rename_i h
          cases hst
          have hne : t' ≠ t := fun e => ha (e ▸ h)
          rcases hx with rfl | hx
          · simp [Event.tx?, hne]
          · exact Mon.after_end b ⟨none, m.seen⟩ m' (by simp) hs hr' x hx
        · cases hst

/-- **Linearity of transactions on traces.** In a `TxSafe` trace, every
`txBegin _ t` is followed by exactly one ending event `e` of `t`
(`txCommit t` or `txRollback t`); between the two there is no other ending of
`t` and no irrevocable effect; after `e`, the id `t` never appears again (no
`txOp t …`, no second commit/rollback). -/
theorem TxSafe.linear {tr : List Event} (h : TxSafe tr) {a b : List Event} {d t : Nat}
    (hsplit : tr = a ++ .txBegin d t :: b) :
    ∃ b₁ e b₂, b = b₁ ++ e :: b₂ ∧ e.Ends t ∧
      (∀ x ∈ b₁, ¬ x.Ends t ∧ ¬ x.IsIrrevocable) ∧
      (∀ x ∈ b₂, x.tx? ≠ some t) := by
  obtain ⟨mf, hr, hf⟩ := h
  subst hsplit
  obtain ⟨ma, _, hb⟩ := Mon.run_append.mp hr
  obtain ⟨m₁, hst, hr₁⟩ := Mon.run_cons.mp hb
  simp only [Mon.step] at hst
  split at hst
  · cases hst
    obtain ⟨b₁, e, b₂, m₂, rfl, he, hb₁, hm₂, hs₂, hr₂⟩ :=
      Mon.until_end b _ mf rfl (List.mem_cons_self) hr₁ hf
    exact ⟨b₁, e, b₂, rfl, he, hb₁, Mon.after_end b₂ m₂ mf (by simp [hm₂]) hs₂ hr₂⟩
  · cases hst

/-- The prefix before the first ending of `t` is unique. -/
theorem first_end_unique {t : Nat} :
    ∀ {b₁ c₁ b₂ c₂ : List Event} {e e' : Event},
      b₁ ++ e :: b₂ = c₁ ++ e' :: c₂ → e.Ends t → e'.Ends t →
      (∀ y ∈ b₁, ¬ y.Ends t) → (∀ y ∈ c₁, ¬ y.Ends t) → b₁ = c₁
  | [], [], _, _, _, _, _, _, _, _, _ => rfl
  | [], y :: c₁, _, _, _, _, h, he, _, _, hc => by
      simp at h; exact absurd (h.1 ▸ he) (hc y (by simp))
  | y :: b₁, [], _, _, _, _, h, _, he', hb, _ => by
      simp at h; exact absurd (h.1 ▸ he') (hb y (by simp))
  | y :: b₁, y' :: c₁, _, _, _, _, h, he, he', hb, hc => by
      simp at h
      obtain ⟨rfl, h⟩ := h
      rw [first_end_unique h he he' (fun z hz => hb z (by simp [hz]))
        (fun z hz => hc z (by simp [hz]))]

/-- **No irrevocable effects inside a transaction (trace form).** In a `TxSafe`
trace, no `fetch` occurs between `txBegin _ t` and the (first, hence only)
ending event of `t`. -/
theorem TxSafe.no_irrevocable_inside {tr : List Event} (h : TxSafe tr) {a b₁ b₂ : List Event}
    {d t : Nat} {e x : Event} (hsplit : tr = a ++ .txBegin d t :: (b₁ ++ e :: b₂))
    (he : e.Ends t) (hb₁ : ∀ y ∈ b₁, ¬ y.Ends t) (hx : x ∈ b₁) : ¬ x.IsIrrevocable := by
  obtain ⟨c₁, e', c₂, hb, he', hc₁, _⟩ := h.linear hsplit
  have : b₁ = c₁ := first_end_unique hb he he' hb₁ (fun y hy => (hc₁ y hy).1)
  subst this
  exact (hc₁ x hx).2

end Kekkai
