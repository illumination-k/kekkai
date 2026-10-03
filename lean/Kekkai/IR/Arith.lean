/-!
# 64-bit two's complement arithmetic for the IR reference interpreter

Integers are represented as mathematical `Int`s that are kept in the range
`[-2^63, 2^63)` by `wrap` after every operation. This mirrors the wasm
backend, which uses `i64` instructions (arithmetic modulo `2^64`).

Division and remainder are total, exactly as in `internal/wasm/codegen.go`:

* `div x 0 = 0`, `div x (-1) = 0 - x` (wrapping; so `MIN / -1 = MIN`),
  otherwise truncating division (`i64.div_s`).
* `rem x 0 = x`, otherwise truncating remainder (`i64.rem_s`, sign of the
  dividend; `MIN rem -1 = 0`).
-/

namespace Kekkai.IR

/-- `2^63` -/
def half : Int := 9223372036854775808
/-- `2^64` -/
def modulus : Int := 18446744073709551616

def minI64 : Int := -9223372036854775808
def maxI64 : Int := 9223372036854775807

/-- Reduce an integer to the signed 64-bit range (two's complement wrap). -/
def wrap (x : Int) : Int := (x + half) % modulus - half

/-- The value is representable as an `i64`. -/
def InRange (x : Int) : Prop := minI64 ≤ x ∧ x ≤ maxI64

instance (x : Int) : Decidable (InRange x) := by unfold InRange; infer_instance

def inRange (x : Int) : Bool := decide (InRange x)

def i64Add (x y : Int) : Int := wrap (x + y)
def i64Sub (x y : Int) : Int := wrap (x - y)
def i64Mul (x y : Int) : Int := wrap (x * y)
def i64Neg (x : Int) : Int := wrap (0 - x)

def i64Div (x y : Int) : Int :=
  if y = 0 then 0
  else if y = -1 then wrap (0 - x)
  else wrap (Int.tdiv x y)

def i64Rem (x y : Int) : Int :=
  if y = 0 then x
  else wrap (Int.tmod x y)

/-! ## Bitwise operations (`int.bit_and` ... `int.ushr`, inlined by the
backend as `i64.and/or/xor/shl/shr_s/shr_u`; shift counts are taken mod 64) -/

/-- The unsigned 64-bit pattern of `x`. -/
def toU64 (x : Int) : Nat := (x % modulus).toNat

def i64And (x y : Int) : Int := wrap ((toU64 x &&& toU64 y : Nat) : Int)
def i64Or (x y : Int) : Int := wrap ((toU64 x ||| toU64 y : Nat) : Int)
def i64Xor (x y : Int) : Int := wrap ((toU64 x ^^^ toU64 y : Nat) : Int)
def shiftCount (y : Int) : Nat := (y % 64).toNat
def i64Shl (x y : Int) : Int := wrap (x * (2 ^ shiftCount y : Nat))
/-- Arithmetic shift: floor division by a power of two. -/
def i64ShrS (x y : Int) : Int := wrap (x / ((2 ^ shiftCount y : Nat) : Int))
def i64ShrU (x y : Int) : Int := wrap ((toU64 x >>> shiftCount y : Nat) : Int)

/-! ## Lemmas -/

theorem wrap_inRange (x : Int) : InRange (wrap x) := by
  unfold InRange wrap half modulus minI64 maxI64
  omega

theorem wrap_of_inRange {x : Int} (h : InRange x) : wrap x = x := by
  unfold InRange minI64 maxI64 at h
  unfold wrap half modulus
  omega

theorem wrap_wrap (x : Int) : wrap (wrap x) = wrap x :=
  wrap_of_inRange (wrap_inRange x)

/-- `wrap` agrees with reduction modulo `2^64`. -/
theorem wrap_emod (x : Int) : wrap x % modulus = x % modulus := by
  unfold wrap half modulus
  omega

/-- Two integers wrap to the same value iff they are congruent mod `2^64`. -/
theorem wrap_eq_iff (x y : Int) : wrap x = wrap y ↔ x % modulus = y % modulus := by
  unfold wrap half modulus
  omega

/-- Wrapping intermediate results does not change the final result of a
sum: per-operation wrapping (as in the interpreter) equals wasm's
modulo-`2^64` addition. -/
theorem wrap_add_wrap (x y : Int) : wrap (wrap x + y) = wrap (x + y) := by
  unfold wrap half modulus
  omega

theorem wrap_sub_wrap (x y : Int) : wrap (wrap x - y) = wrap (x - y) := by
  unfold wrap half modulus
  omega

theorem i64Add_inRange (x y : Int) : InRange (i64Add x y) := wrap_inRange _
theorem i64Sub_inRange (x y : Int) : InRange (i64Sub x y) := wrap_inRange _
theorem i64Mul_inRange (x y : Int) : InRange (i64Mul x y) := wrap_inRange _
theorem i64Neg_inRange (x : Int) : InRange (i64Neg x) := wrap_inRange _

theorem i64Div_inRange (x y : Int) : InRange (i64Div x y) := by
  unfold i64Div
  split
  · unfold InRange minI64 maxI64; omega
  · split <;> exact wrap_inRange _

theorem i64Rem_inRange {x : Int} (hx : InRange x) (y : Int) : InRange (i64Rem x y) := by
  unfold i64Rem
  split
  · exact hx
  · exact wrap_inRange _

theorem i64Div_zero (x : Int) : i64Div x 0 = 0 := by simp [i64Div]
theorem i64Rem_zero (x : Int) : i64Rem x 0 = x := by simp [i64Rem]

/-- `MIN / -1 = MIN` (the case where `i64.div_s` would trap). -/
theorem i64Div_min_neg_one : i64Div minI64 (-1) = minI64 := by decide

/-- `MIN rem -1 = 0`. -/
theorem i64Rem_min_neg_one : i64Rem minI64 (-1) = 0 := by decide

theorem i64Add_comm (x y : Int) : i64Add x y = i64Add y x := by
  unfold i64Add; rw [Int.add_comm]

theorem i64Add_assoc (x y z : Int) : i64Add (i64Add x y) z = i64Add x (i64Add y z) := by
  unfold i64Add wrap half modulus
  omega

#guard i64And (-1) 12345 == 12345
#guard i64Xor (-1) 0 == -1
#guard i64Or minI64 1 == minI64 + 1
#guard i64Shl 1 63 == minI64
#guard i64Shl 1 64 == 1
#guard i64Shl 3 (-1) == minI64
#guard i64ShrS (-8) 1 == -4
#guard i64ShrS (-1) 63 == -1
#guard i64ShrU (-1) 1 == maxI64
#guard i64ShrU (-1) 0 == -1

end Kekkai.IR
