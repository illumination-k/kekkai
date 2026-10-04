/-!
# Kekkai IR: abstract syntax

Mirrors `compiler/ir.kek`. Source types are erased to representation
types; aggregates refer to `Program.types` by index.
-/

namespace Kekkai.IR

inductive TyKind where
  | unit | bool | int | string | ext | agg
  deriving Repr, BEq, Inhabited, DecidableEq

structure Ty where
  kind : TyKind
  agg : Nat := 0
  ext : String := ""
  deriving Repr, BEq, Inhabited

structure TypeDef where
  name : String
  isVariant : Bool
  fields : Array Ty := #[]
  variants : Array (Array Ty) := #[]
  /-- `"vec"` (element type `elem`) or `""` -/
  coll : String := ""
  elem : Option Ty := none
  deriving Repr, Inhabited

inductive Const where
  | unit
  | bool (b : Bool)
  | int (i : Int)
  | str (s : String)
  deriving Repr, Inhabited

inductive UnOp where
  | neg | not
  deriving Repr, BEq, Inhabited

inductive BinOp where
  | add | sub | mul | div | rem | lt | le | gt | ge | eq | ne | concat
  deriving Repr, BEq, Inhabited

/-- Locals are referred to by index. Every instruction writes `dst`. -/
abbrev Local := Nat

inductive Instr where
  | const (dst : Local) (c : Const)
  | copy (dst : Local) (src : Local)
  | unop (dst : Local) (op : UnOp) (x : Local)
  | binop (dst : Local) (op : BinOp) (x y : Local)
  | call (dst : Local) (fn : String) (args : Array Local)
  /-- `ty` is the collection aggregate for `vec.*` operations -/
  | host (dst : Local) (name : String) (ty : Nat) (args : Array Local)
  | await (dst : Local) (name : String) (args : Array Local)
  | struct (dst : Local) (ty : Nat) (args : Array Local)
  | field (dst : Local) (ty : Nat) (index : Nat) (x : Local)
  /-- `x.index := v`; structs are mutable references -/
  | setfield (dst : Local) (ty : Nat) (index : Nat) (x v : Local)
  | variant (dst : Local) (ty : Nat) (tag : Nat) (args : Array Local)
  | tag (dst : Local) (ty : Nat) (x : Local)
  | vfield (dst : Local) (ty : Nat) (tag : Nat) (index : Nat) (x : Local)
  deriving Repr, Inhabited

inductive Term where
  | jump (target : Nat)
  | br (cond : Local) (thenT elseT : Nat)
  /-- `targets[k]` when the scrutinee equals `k`; the last target is the default. -/
  | switch (x : Local) (targets : Array Nat)
  | ret (x : Local)
  | unreachable
  deriving Repr, Inhabited

structure Block where
  instrs : Array Instr
  term : Term
  deriving Repr, Inhabited

structure Func where
  name : String
  nparams : Nat
  locals : Array Ty
  result : Ty
  async : Bool
  blocks : Array Block
  deriving Repr, Inhabited

structure Program where
  types : Array TypeDef
  funcs : Array Func
  deriving Repr, Inhabited

def Program.findFunc? (p : Program) (name : String) : Option Func :=
  p.funcs.find? (·.name == name)

end Kekkai.IR
