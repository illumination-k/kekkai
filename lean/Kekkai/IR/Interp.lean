import Kekkai.IR.Arith
import Kekkai.IR.Value
import Kekkai.IR.Host

/-!
# Reference interpreter for the Kekkai IR

A small-step abstract machine with an explicit call stack, so that the
interpreter is total (`run` is structurally recursive on the fuel) and
deep recursion in the interpreted program does not consume native stack.

The semantics follows the WasmGC backend (`compiler/wasm_codegen.kek`,
`compiler/wasm_coll.kek`):

* every instruction writes its destination local; locals start at their
  type's default value (`0`, `false`, `ref.null`);
* structs, `Vec`s and `Map`s are heap objects with reference semantics
  (mutation through one alias is visible through all of them); variants
  are immutable values;
* `br` takes the first target when the condition is true;
* `switch` compares the integer scrutinee with `0, 1, ...` and falls back
  to the last target;
* `vfield` with a tag that differs from the value's tag reads the default
  value of the requested slot (wasm stores every variant's slots and fills
  the inactive ones with defaults);
* reading through `ref.null` is a trap, reported as `null dereference`;
* each instruction and each terminator costs one unit of fuel.
-/

namespace Kekkai.IR

structure Frame where
  fn : Func
  locals : Array Val
  block : Nat
  pc : Nat
  /-- caller local receiving this frame's return value -/
  retDst : Nat
  deriving Inhabited

structure Machine where
  /-- innermost frame first -/
  frames : List Frame
  depth : Nat
  store : Store
  deriving Inhabited

inductive Outcome where
  | running (m : Machine)
  | done (v : Val) (store : Store)

/-- Maximum call depth before reporting `stack overflow`. The wasm engine
also has a (host-dependent) stack limit; results in that regime are not
comparable. -/
def maxDepth : Nat := 10000

abbrev M := Except String

def getLocal (f : Frame) (i : Nat) : M Val :=
  match f.locals[i]? with
  | some v => pure v
  | none => throw s!"malformed IR: local {i} out of range in {f.fn.name}"

def setLocal (f : Frame) (i : Nat) (v : Val) : M Frame :=
  if i < f.locals.size then pure { f with locals := f.locals.set! i v }
  else throw s!"malformed IR: local {i} out of range in {f.fn.name}"

def asInt : Val → M Int
  | .int i => pure i
  | .null => throw "null dereference"
  | v => throw s!"type error: expected int, got {repr v}"

def asBool : Val → M Bool
  | .bool b => pure b
  | v => throw s!"type error: expected bool, got {repr v}"

def evalUnop : UnOp → Val → M Val
  | .neg, .int x => pure (.int (i64Neg x))
  | .not, .bool b => pure (.bool !b)
  | op, v => throw s!"type error: {repr op} on {repr v}"

def evalBinop : BinOp → Val → Val → M Val
  | .add, .int x, .int y => pure (.int (i64Add x y))
  | .sub, .int x, .int y => pure (.int (i64Sub x y))
  | .mul, .int x, .int y => pure (.int (i64Mul x y))
  | .div, .int x, .int y => pure (.int (i64Div x y))
  | .rem, .int x, .int y => pure (.int (i64Rem x y))
  | .lt, .int x, .int y => pure (.bool (decide (x < y)))
  | .le, .int x, .int y => pure (.bool (decide (x ≤ y)))
  | .gt, .int x, .int y => pure (.bool (decide (x > y)))
  | .ge, .int x, .int y => pure (.bool (decide (x ≥ y)))
  | .eq, .int x, .int y => pure (.bool (x == y))
  | .ne, .int x, .int y => pure (.bool (x != y))
  | .eq, .bool x, .bool y => pure (.bool (x == y))
  | .ne, .bool x, .bool y => pure (.bool (x != y))
  | .eq, .unit, .unit => pure (.bool true)
  | .ne, .unit, .unit => pure (.bool false)
  | .eq, .str x, .str y => pure (.bool (x == y))
  | .ne, .str x, .str y => pure (.bool (x != y))
  | .concat, .str x, .str y => pure (.str (x ++ y))
  | op, x, y => throw s!"type error: {repr op} on {repr x}, {repr y}"

def typeDef (p : Program) (ty : Nat) : M TypeDef :=
  match p.types[ty]? with
  | some td => pure td
  | none => throw s!"malformed IR: unknown aggregate {ty}"

/-! ## Collections -/

/-- Key equality of a JS `Map` (SameValueZero on the boxed keys: numbers
and strings by value, GC references by identity). -/
def keyEq : Val → Val → Bool
  | .int a, .int b => a == b
  | .str a, .str b => a == b
  | .bool a, .bool b => a == b
  | .unit, .unit => true
  | .ref a, .ref b => a == b
  | .null, .null => true
  | _, _ => false

def mapEntries (v : Val) : HM (Array (Val × Val)) := do
  match ← deref v with
  | .map es => pure es
  | o => throw s!"type error: expected a Map, got {repr o}"

/-- Collection operations (`vec.*`, `map.*`), implemented by the backend. -/
def evalColl (name : String) (args : Array Val) : HM Val := do
  let bad : HM Val := throw s!"{name}: bad arguments"
  match name, args with
  | "vec.new", #[] => newVec #[]
  | "vec.len", #[v] => return .int (← vecElems v).size
  | "vec.push", #[v, x] => do
    let xs ← vecElems v
    store v (.vec (xs.push x)); pure .unit
  | "vec.get", #[v, .int i] => do
    let xs ← vecElems v
    pure (optionVal (if 0 ≤ i ∧ i < xs.size then some xs[i.toNat]! else none))
  | "vec.set", #[v, .int i, x] => do
    let xs ← vecElems v
    if 0 ≤ i ∧ i < xs.size then
      store v (.vec (xs.set! i.toNat x)); pure (.bool true)
    else pure (.bool false)
  | "vec.pop", #[v] => do
    let xs ← vecElems v
    if xs.isEmpty then pure (optionVal none)
    else
      store v (.vec xs.pop); pure (optionVal (some xs.back!))
  | "vec.at", #[v, .int i] => do
    let xs ← vecElems v
    if 0 ≤ i ∧ i < xs.size then pure xs[i.toNat]! else throw "trap"
  | "vec.join", #[v, .str sep] => do
    let xs ← vecElems v
    let ss ← xs.toList.mapM fun
      | .str s => pure s
      | _ => throw "vec.join: non-string element"
    pure (.str (sep.intercalate ss))
  | "map.new", #[] => alloc (.map #[])
  | "map.len", #[m] => return .int (← mapEntries m).size
  | "map.insert", #[m, k, x] => do
    let es ← mapEntries m
    let es := match es.findIdx? (keyEq k ·.1) with
      | some i => es.set! i (k, x)
      | none => es.push (k, x)
    store m (.map es); pure .unit
  | "map.get", #[m, k] => do
    let es ← mapEntries m
    -- the host returns `null` for an absent key; a stored null reads as None too
    match es.find? (keyEq k ·.1) with
    | some (_, .null) | none => pure (optionVal none)
    | some (_, x) => pure (optionVal (some x))
  | "map.contains", #[m, k] => return .bool ((← mapEntries m).any (keyEq k ·.1))
  | "map.remove", #[m, k] => do
    let es ← mapEntries m
    store m (.map (es.filter fun e => !keyEq k e.1)); pure .unit
  | "map.keys", #[m] => do newVec ((← mapEntries m).map (·.1))
  | _, _ => bad

def isCollOp (name : String) : Bool :=
  name.startsWith "vec." || name.startsWith "map."

/-! ## The machine -/

def initFrame (fn : Func) (args : Array Val) (retDst : Nat) : M Frame := do
  if args.size != fn.nparams then
    throw s!"call to {fn.name}: expected {fn.nparams} arguments, got {args.size}"
  let locals := (fn.locals.extract args.size fn.locals.size).map Ty.default
  pure { fn, locals := args ++ locals, block := 0, pc := 0, retDst }

/-- Run a store computation inside the machine. -/
def liftHM (m : Machine) (x : HM α) : M (α × Machine) := do
  let (a, st) ← x.run m.store
  pure (a, { m with store := st })

/-- Execute one instruction in the top frame (whose `pc` has already been
advanced). Returns the updated machine. -/
def execInstr (p : Program) (m : Machine) (f : Frame) (rest : List Frame) (i : Instr) : M Machine := do
  let get := getLocal f
  let gets (xs : Array Nat) : M (Array Val) := xs.mapM get
  let writeIn (m : Machine) (dst : Nat) (v : Val) : M Machine := do
    pure { m with frames := (← setLocal f dst v) :: rest }
  let write := writeIn m
  let withStore (dst : Nat) (x : HM Val) : M Machine := do
    let (v, m') ← liftHM m x
    writeIn m' dst v
  match i with
  | .const dst c =>
    write dst (match c with
      | .unit => .unit | .bool b => .bool b | .int n => .int (wrap n) | .str s => .str s)
  | .copy dst x => write dst (← get x)
  | .unop dst op x => write dst (← evalUnop op (← get x))
  | .binop dst op x y => write dst (← evalBinop op (← get x) (← get y))
  | .call dst name xs =>
    let some callee := p.findFunc? name | throw s!"malformed IR: unknown function {name}"
    if m.depth + 1 ≥ maxDepth then throw "stack overflow"
    let fr ← initFrame callee (← gets xs) dst
    pure { m with frames := fr :: f :: rest, depth := m.depth + 1 }
  | .host dst name _ xs =>
    let args ← gets xs
    withStore dst (if isCollOp name then evalColl name args else evalHost name args)
  | .await _ name _ => throw s!"unsupported await {name}"
  | .struct dst _ xs => withStore dst (alloc (.struct (← gets xs)))
  | .field dst _ idx x =>
    let x ← get x
    withStore dst do
      match ← deref x with
      | .struct fs =>
        match fs[idx]? with
        | some v => pure v
        | none => throw s!"malformed IR: field {idx} out of range"
      | o => throw s!"type error: field of {repr o}"
  | .setfield dst _ idx x v =>
    let x ← get x
    let v ← get v
    withStore dst do
      match ← deref x with
      | .struct fs =>
        if idx < fs.size then store x (.struct (fs.set! idx v)); pure .unit
        else throw s!"malformed IR: field {idx} out of range"
      | o => throw s!"type error: setfield of {repr o}"
  | .variant dst _ tag xs => write dst (.variant tag (← gets xs))
  | .tag dst _ x =>
    match ← get x with
    | .variant t _ => write dst (.int t)
    | .null => throw "null dereference"
    | v => throw s!"type error: tag of {repr v}"
  | .vfield dst ty tag idx x =>
    match ← get x with
    | .variant t fs =>
      if t == tag then
        match fs[idx]? with
        | some v => write dst v
        | none => throw s!"malformed IR: variant field {tag}.{idx} out of range"
      else
        -- inactive slot: wasm reads the default value stored there
        let td ← typeDef p ty
        match td.variants[tag]? >>= (·[idx]?) with
        | some fty => write dst fty.default
        | none => throw s!"malformed IR: variant field {tag}.{idx} out of range"
    | .null => throw "null dereference"
    | v => throw s!"type error: vfield of {repr v}"

def gotoBlock (m : Machine) (f : Frame) (rest : List Frame) (b : Nat) : M Machine :=
  if b < f.fn.blocks.size then
    pure { m with frames := { f with block := b, pc := 0 } :: rest }
  else throw s!"malformed IR: block {b} out of range in {f.fn.name}"

def execTerm (m : Machine) (f : Frame) (rest : List Frame) : Term → M Outcome
  | .jump t => .running <$> gotoBlock m f rest t
  | .br c t e => do
    let b ← asBool (← getLocal f c)
    .running <$> gotoBlock m f rest (if b then t else e)
  | .switch x ts => do
    let k ← asInt (← getLocal f x)
    let n := ts.size - 1
    let target := if 0 ≤ k ∧ k < n then ts[k.toNat]! else ts[n]!
    .running <$> gotoBlock m f rest target
  | .ret x => do
    let v ← getLocal f x
    match rest with
    | [] => pure (.done v m.store)
    | caller :: rest' =>
      let caller ← setLocal caller f.retDst v
      pure (.running { m with frames := caller :: rest', depth := m.depth - 1 })
  | .unreachable => throw "unreachable"

def step (p : Program) (m : Machine) : M Outcome := do
  match m.frames with
  | [] => throw "internal error: empty stack"
  | f :: rest =>
    let some blk := f.fn.blocks[f.block]? | throw s!"malformed IR: block {f.block} out of range"
    match blk.instrs[f.pc]? with
    | some i => .running <$> execInstr p m { f with pc := f.pc + 1 } rest i
    | none => execTerm m f rest blk.term

/-- Run for at most `fuel` steps. -/
def run (p : Program) : Nat → Machine → M (Val × Store)
  | 0, _ => throw "timeout"
  | fuel + 1, m =>
    match step p m with
    | .error e => .error e
    | .ok (.done v st) => .ok (v, st)
    | .ok (.running m') => run p fuel m'

def defaultFuel : Nat := 1000000

/-- Call function `fn` of program `p` with the given arguments, starting
from store `st` (which holds the objects the arguments refer to). -/
def callFunc (p : Program) (fn : Func) (args : Array Val) (st : Store := {})
    (fuel : Nat := defaultFuel) : M (Val × Store) := do
  let fr ← initFrame fn args 0
  run p fuel { frames := [fr], depth := 0, store := st }

end Kekkai.IR
