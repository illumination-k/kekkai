/-!
# Small list utilities used throughout the development
-/

namespace Kekkai

/-- Pointwise relation between two lists of equal length. -/
inductive Forall2 {α β : Type} (R : α → β → Prop) : List α → List β → Prop where
  | nil : Forall2 R [] []
  | cons {a b l₁ l₂} : R a b → Forall2 R l₁ l₂ → Forall2 R (a :: l₁) (b :: l₂)

theorem Forall2.get {α β : Type} {R : α → β → Prop} :
    ∀ {l₁ : List α} {l₂ : List β} {i : Nat} {b : β},
      Forall2 R l₁ l₂ → l₂[i]? = some b → ∃ a, l₁[i]? = some a ∧ R a b
  | _, _, _, _, .nil, h => by simp at h
  | _, _, 0, _, .cons hab _, h => by
      simp at h; subst h; exact ⟨_, rfl, hab⟩
  | _, _, i+1, _, .cons _ hl, h => by
      simp at h; simpa using Forall2.get hl h

theorem Forall2.map_right {α β : Type} {R : α → β → Prop} {S : α → β → Prop} {f : β → β}
    (hf : ∀ a b, R a b → S a (f b)) :
    ∀ {l₁ : List α} {l₂ : List β}, Forall2 R l₁ l₂ → Forall2 S l₁ (l₂.map f)
  | _, _, .nil => .nil
  | _, _, .cons h hl => .cons (hf _ _ h) (Forall2.map_right hf hl)

/-- Look up a list of indices; `none` if any index is out of range. -/
def lookupAll {α : Type} (l : List α) : List Nat → Option (List α)
  | [] => some []
  | i :: is =>
    match l[i]?, lookupAll l is with
    | some a, some as => some (a :: as)
    | _, _ => none

theorem lookupAll_cons {α : Type} {l : List α} {i : Nat} {is : List Nat} {as : List α} :
    lookupAll l (i :: is) = some as ↔
      ∃ a as', l[i]? = some a ∧ lookupAll l is = some as' ∧ as = a :: as' := by
  simp only [lookupAll]
  constructor
  · intro h
    split at h
    · rename_i a as' h1 h2
      cases h
      exact ⟨a, as', h1, h2, rfl⟩
    · cases h
  · rintro ⟨a, as', h1, h2, rfl⟩
    simp [h1, h2]

theorem lookupAll_forall2 {α β : Type} {R : α → β → Prop} {l₁ : List α} {l₂ : List β}
    (hl : Forall2 R l₁ l₂) :
    ∀ {is : List Nat} {bs : List β}, lookupAll l₂ is = some bs →
      ∃ as, lookupAll l₁ is = some as ∧ Forall2 R as bs
  | [], bs, h => by
      simp [lookupAll] at h; subst h; exact ⟨[], rfl, .nil⟩
  | i :: is, bs, h => by
      obtain ⟨b, bs', hb, hbs, rfl⟩ := lookupAll_cons.mp h
      obtain ⟨a, ha, hab⟩ := hl.get hb
      obtain ⟨as, has, hr⟩ := lookupAll_forall2 hl hbs
      exact ⟨a :: as, lookupAll_cons.mpr ⟨a, as, ha, has, rfl⟩, .cons hab hr⟩

theorem lookupAll_get {α : Type} {l : List α} :
    ∀ {is : List Nat} {as : List α} {j : Nat} {a : α},
      lookupAll l is = some as → as[j]? = some a → ∃ i, is[j]? = some i ∧ l[i]? = some a
  | [], as, j, a, h, ha => by
      simp [lookupAll] at h; subst h; simp at ha
  | i :: is, as, j, a, h, ha => by
      obtain ⟨b, bs, hb, hbs, rfl⟩ := lookupAll_cons.mp h
      cases j with
      | zero => simp at ha; subst ha; exact ⟨i, rfl, hb⟩
      | succ j =>
        simp at ha
        obtain ⟨i', h1, h2⟩ := lookupAll_get hbs ha
        exact ⟨i', by simpa using h1, h2⟩

theorem lookupAll_mem {α : Type} {l : List α} {is : List Nat} {as : List α} {a : α}
    (h : lookupAll l is = some as) (ha : a ∈ as) : a ∈ l := by
  obtain ⟨j, hj⟩ := List.getElem?_of_mem ha
  obtain ⟨i, _, hi⟩ := lookupAll_get h hj
  exact List.mem_of_getElem? hi

theorem lookupAll_nil {α : Type} {is : List Nat} {as : List α}
    (h : lookupAll ([] : List α) is = some as) : as = [] := by
  cases is with
  | nil => simp [lookupAll] at h; exact h
  | cons i is =>
    obtain ⟨_, _, hb, _⟩ := lookupAll_cons.mp h
    simp at hb

theorem lookupAll_getElem {α : Type} {l : List α} :
    ∀ {is : List Nat} {as : List α} {j i : Nat},
      lookupAll l is = some as → is[j]? = some i → as[j]? = l[i]?
  | [], _, _, _, _, h => by simp at h
  | i' :: is, as, j, i, h, hj => by
      obtain ⟨b, bs, hb, hbs, rfl⟩ := lookupAll_cons.mp h
      cases j with
      | zero => simp at hj; subst hj; simp [hb]
      | succ j => simp at hj; simpa using lookupAll_getElem hbs hj

end Kekkai
