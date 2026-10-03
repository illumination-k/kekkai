import Kekkai.IR.Syntax

/-!
# Runtime values of the IR reference interpreter
-/

namespace Kekkai.IR

inductive Val where
  | unit
  | bool (b : Bool)
  /-- always within the signed 64-bit range -/
  | int (i : Int)
  | str (s : String)
  | struct (fields : Array Val)
  /-- a variant value carries only the fields of its own tag -/
  | variant (tag : Nat) (fields : Array Val)
  /-- opaque host value (capabilities, `Request`, `TxError`, ...) -/
  | opaque (ext : String)
  /-- the default value of reference-typed locals (wasm `ref.null`) -/
  | null
  deriving Repr, Inhabited

/-- Default value of a local of the given type, as initialised by wasm
(`i32 0`, `i64 0`, `ref.null`). -/
def Ty.default (t : Ty) : Val :=
  match t.kind with
  | .unit => .unit
  | .bool => .bool false
  | .int => .int 0
  | .string | .ext | .agg => .null

end Kekkai.IR
