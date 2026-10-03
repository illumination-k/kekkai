import Kekkai.IR.Syntax

/-!
# Runtime values and the heap

Structs, `Vec`s and `Map`s are mutable objects with reference semantics
(wasm GC structs and arrays), so they live in a heap and values
refer to them by address. Variants are immutable in wasm, so they are
represented directly (their fields may hold references).
-/

namespace Kekkai.IR

inductive Val where
  | unit
  | bool (b : Bool)
  /-- always within the signed 64-bit range -/
  | int (i : Int)
  | str (s : String)
  /-- reference to a heap object (struct, Vec or Map) -/
  | ref (addr : Nat)
  /-- a variant value carries only the fields of its own tag -/
  | variant (tag : Nat) (fields : Array Val)
  /-- opaque host value (capabilities, `Request`, `TxError`, ...) -/
  | opaque (ext : String)
  /-- the default value of reference-typed locals (wasm `ref.null`) -/
  | null
  deriving Repr, Inhabited

inductive HeapObj where
  | struct (fields : Array Val)
  | vec (elems : Array Val)
  /-- insertion-ordered -/
  | map (entries : Array (Val × Val))
  deriving Repr, Inhabited

/-- Log entries `(level, message)` emitted by `log.info/warn/error`. -/
abbrev LogEntry := String × String

/-- Mutable state shared by the whole run. -/
structure Store where
  heap : Array HeapObj := #[]
  log : Array LogEntry := #[]
  deriving Inhabited

/-- Computations that may fail and that read/write the store. -/
abbrev HM := StateT Store (Except String)

def alloc (o : HeapObj) : HM Val := do
  let s ← get
  set { s with heap := s.heap.push o }
  pure (.ref s.heap.size)

def deref : Val → HM HeapObj
  | .ref a => do
    match (← get).heap[a]? with
    | some o => pure o
    | none => throw s!"internal error: dangling reference {a}"
  | .null => throw "null dereference"
  | v => throw s!"type error: expected a reference, got {repr v}"

def store (addr : Val) (o : HeapObj) : HM Unit := do
  match addr with
  | .ref a => modify fun s => { s with heap := s.heap.set! a o }
  | _ => throw "internal error: store through a non-reference"

def emitLog (e : LogEntry) : HM Unit :=
  modify fun s => { s with log := s.log.push e }

/-- Default value of a local of the given type, as initialised by wasm
(`i32 0`, `i64 0`, `ref.null`). -/
def Ty.default (t : Ty) : Val :=
  match t.kind with
  | .unit => .unit
  | .bool => .bool false
  | .int => .int 0
  | .string | .ext | .agg => .null

end Kekkai.IR
