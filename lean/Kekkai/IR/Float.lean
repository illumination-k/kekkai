import Kekkai.IR.Arith

/-!
# IEEE 754 binary64 for the IR reference interpreter

`Float` values are Lean's `Float` (binary64; `+ - * /`, `sqrt` and the
comparisons are the correctly rounded IEEE operations, as wasm's `f64`
instructions). Everything whose result is an integer or a bit pattern is
computed exactly from the bits here instead of through C conversions, so
it matches wasm's `i64.trunc_sat_f64_s` and `i64.reinterpret_f64`:

* `fToBits` maps every NaN to the canonical quiet NaN `0x7ff8000000000000`
  (the backend does the same; NaN payloads are not observable).
* `fTruncSat` truncates toward zero, saturating at the `i64` bounds, NaN
  to 0 (Rust's `as i64`).
* `fTrunc` is `floor` for non-negative values and `ceil` for negative ones
  (both exact; `-0.5` gives `-0.0` as `f64.trunc`).
-/

namespace Kekkai.IR

/-- The canonical quiet NaN as a signed 64-bit integer. -/
def canonNaN : Int := 9221120237041090560

def toSigned64 (n : Nat) : Int := if n < 2 ^ 63 then n else (n : Int) - 2 ^ 64

def fToBits (x : Float) : Int :=
  if x != x then canonNaN else toSigned64 x.toBits.toNat

def fOfBits (i : Int) : Float := Float.ofBits (UInt64.ofNat (i % 2 ^ 64).toNat)

def fOfInt (i : Int) : Float := (Int64.ofInt i).toFloat

def fTruncSat (x : Float) : Int :=
  let b := x.toBits.toNat
  let e := (b >>> 52) % 2048
  let m := b % 2 ^ 52
  let neg := b ≥ 2 ^ 63
  if e == 2047 then
    if m != 0 then 0 else if neg then minI64 else maxI64
  else
    let mant : Nat := if e == 0 then m else m + 2 ^ 52
    let sh : Int := (if e == 0 then (1 : Int) else (e : Int)) - 1075
    let mag : Nat := if sh ≥ 0 then mant <<< sh.toNat else mant >>> (-sh).toNat
    let v : Int := if neg then -(mag : Int) else mag
    max minI64 (min maxI64 v)

def fTrunc (x : Float) : Float := if x < 0 then x.ceil else x.floor

end Kekkai.IR
