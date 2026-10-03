import Lean.Data.Json
import Kekkai.IR.Syntax

/-!
# Decoding the JSON encoding of the IR

The IR is produced by `kek ir -json` using Go's `encoding/json`. Fields
tagged `omitempty` are absent when they hold their zero value (`0`,
`false`, `""`, empty slice), and nil slices without `omitempty` are
encoded as `null`. The decoders below therefore treat a missing field or
`null` as the zero value.
-/

namespace Kekkai.IR
open Lean (Json)

abbrev P := Except String

private def field? (j : Json) (k : String) : Option Json :=
  match j.getObjVal? k with
  | .ok Json.null => none
  | .ok v => some v
  | .error _ => none

private def natD (j : Json) (k : String) : P Nat := do
  match field? j k with
  | none => pure 0
  | some v =>
    let i ← v.getInt?
    if i < 0 then throw s!"field {k}: negative value {i}" else pure i.toNat

private def intD (j : Json) (k : String) : P Int := do
  match field? j k with
  | none => pure 0
  | some v => v.getInt?

private def strD (j : Json) (k : String) : P String := do
  match field? j k with
  | none => pure ""
  | some v => v.getStr?

private def boolD (j : Json) (k : String) : P Bool := do
  match field? j k with
  | none => pure false
  | some v => v.getBool?

private def arrD (j : Json) (k : String) : P (Array Json) := do
  match field? j k with
  | none => pure #[]
  | some v => v.getArr?

private def req (j : Json) (k : String) : P Json :=
  match field? j k with
  | some v => pure v
  | none => throw s!"missing field {k}"

def decodeTyKind : String → P TyKind
  | "unit" => pure .unit
  | "bool" => pure .bool
  | "int" => pure .int
  | "string" => pure .string
  | "ext" => pure .ext
  | "agg" => pure .agg
  | k => throw s!"unknown type kind {k}"

def decodeTy (j : Json) : P Ty := do
  let kind ← decodeTyKind (← strD j "kind")
  return { kind, agg := ← natD j "agg", ext := ← strD j "ext" }

def decodeTys (js : Array Json) : P (Array Ty) := js.mapM decodeTy

def decodeTypeDef (j : Json) : P TypeDef := do
  let variants ← (← arrD j "variants").mapM fun v =>
    match v with
    | Json.null => pure #[]
    | v => do decodeTys (← v.getArr?)
  return {
    name := ← strD j "name"
    isVariant := ← boolD j "variant"
    fields := ← decodeTys (← arrD j "fields")
    variants
    coll := ← strD j "coll"
    elem := ← (field? j "elem").mapM decodeTy
    key := ← (field? j "key").mapM decodeTy }

def decodeConst (j : Json) : P Const := do
  match ← decodeTyKind (← strD j "kind") with
  | .unit => pure .unit
  | .bool => return .bool (← boolD j "bool")
  | .int => return .int (← intD j "int")
  | .string => return .str (← strD j "str")
  | k => throw s!"bad constant kind {repr k}"

def decodeUnOp : String → P UnOp
  | "neg" => pure .neg
  | "not" => pure .not
  | n => throw s!"unknown unop {n}"

def decodeBinOp : String → P BinOp
  | "add" => pure .add | "sub" => pure .sub | "mul" => pure .mul
  | "div" => pure .div | "rem" => pure .rem
  | "lt" => pure .lt | "le" => pure .le | "gt" => pure .gt | "ge" => pure .ge
  | "eq" => pure .eq | "ne" => pure .ne | "concat" => pure .concat
  | n => throw s!"unknown binop {n}"

private def natArr (js : Array Json) : P (Array Nat) :=
  js.mapM fun v => do
    let i ← v.getInt?
    if i < 0 then throw s!"negative index {i}" else pure i.toNat

private def arg (args : Array Nat) (k : Nat) (op : String) : P Nat :=
  match args[k]? with
  | some a => pure a
  | none => throw s!"{op}: missing argument {k}"

def decodeInstr (j : Json) : P Instr := do
  let op ← strD j "op"
  let dst ← natD j "dst"
  let args ← natArr (← arrD j "args")
  let name ← strD j "name"
  let ty ← natD j "type"
  let tag ← natD j "tag"
  let index ← natD j "index"
  match op with
  | "const" => return .const dst (← decodeConst (← req j "const"))
  | "copy" => return .copy dst (← arg args 0 op)
  | "unop" => return .unop dst (← decodeUnOp name) (← arg args 0 op)
  | "binop" => return .binop dst (← decodeBinOp name) (← arg args 0 op) (← arg args 1 op)
  | "call" => return .call dst name args
  | "host" => return .host dst name ty args
  | "await" => return .await dst name args
  | "struct" => return .struct dst ty args
  | "field" => return .field dst ty index (← arg args 0 op)
  | "setfield" => return .setfield dst ty index (← arg args 0 op) (← arg args 1 op)
  | "variant" => return .variant dst ty tag args
  | "tag" => return .tag dst ty (← arg args 0 op)
  | "vfield" => return .vfield dst ty tag index (← arg args 0 op)
  | _ => throw s!"unknown instruction {op}"

def decodeTerm (j : Json) : P Term := do
  let op ← strD j "op"
  let args ← natArr (← arrD j "args")
  let targets ← natArr (← arrD j "targets")
  let tgt (k : Nat) : P Nat :=
    match targets[k]? with
    | some t => pure t
    | none => throw s!"{op}: missing target {k}"
  match op with
  | "jump" => return .jump (← tgt 0)
  | "br" => return .br (← arg args 0 op) (← tgt 0) (← tgt 1)
  | "switch" =>
    if targets.isEmpty then throw "switch: no targets"
    return .switch (← arg args 0 op) targets
  | "ret" => return .ret (← arg args 0 op)
  | "unreachable" => return .unreachable
  | _ => throw s!"unknown terminator {op}"

def decodeBlock (j : Json) : P Block := do
  return { instrs := ← (← arrD j "instrs").mapM decodeInstr, term := ← decodeTerm (← req j "term") }

def decodeFunc (j : Json) : P Func := do
  let name ← strD j "name"
  let f : P Func := do
    let locals ← decodeTys (← arrD j "locals")
    let nparams ← natD j "nparams"
    if nparams > locals.size then throw "nparams exceeds the number of locals"
    return {
      name, nparams, locals
      result := ← decodeTy (← req j "result")
      async := ← boolD j "async"
      blocks := ← (← arrD j "blocks").mapM decodeBlock }
  match f with
  | .ok f => pure f
  | .error e => throw s!"function {name}: {e}"

def decodeProgram (j : Json) : P Program := do
  return {
    types := ← (← arrD j "types").mapM decodeTypeDef
    funcs := ← (← arrD j "funcs").mapM decodeFunc }

def parseProgram (s : String) : P Program := do
  decodeProgram (← Json.parse s)

end Kekkai.IR
