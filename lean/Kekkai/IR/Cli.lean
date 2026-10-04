import Lean.Data.Json
import Kekkai.IR.Json
import Kekkai.IR.Interp

/-!
# Command line driver for the reference interpreter

    kekkai-ref <ir.json> <funcName> <arg>...

Each argument is a JSON value (`42`, `-7`, `true`, `"abc"`, `null` for
unit, `{"fields":[...]}` / `{"tag":k,"fields":[...]}` for aggregates,
`{"vec":[...]}`).
Parameters of type `ext` (capabilities) receive an opaque value and do not
consume an argument.

Output (one line on stdout, exit code 0):

    {"ok": <value>, "log": [["info","..."], ...]}
    {"error": "<message>"}
-/

namespace Kekkai.IR
open Lean (Json)

/-- Render a value, dereferencing heap objects. A reference to an object
that is already being rendered (a cycle) is shown as `{"cycle":true}`. -/
partial def renderVal (heap : Array HeapObj) (path : List Nat := []) : Val → String
  | .unit => "null"
  | .bool b => if b then "true" else "false"
  | .int i => toString i
  | .str s => (Json.str s).compress
  | .variant t fs =>
    "{\"tag\":" ++ toString t ++ ",\"fields\":[" ++ ",".intercalate (fs.toList.map (renderVal heap path)) ++ "]}"
  | .opaque e => "{\"ext\":" ++ (Json.str e).compress ++ "}"
  | .null => "{\"null\":true}"
  | .ref a =>
    if path.contains a then "{\"cycle\":true}" else
    let r := renderVal heap (a :: path)
    match heap[a]? with
    | some (.struct fs) => "{\"fields\":[" ++ ",".intercalate (fs.toList.map r) ++ "]}"
    | some (.vec xs) => "{\"vec\":[" ++ ",".intercalate (xs.toList.map r) ++ "]}"
    | none => "{\"dangling\":true}"

def renderLog (log : Array LogEntry) : String :=
  "[" ++ ",".intercalate (log.toList.map fun (l, m) =>
    "[" ++ (Json.str l).compress ++ "," ++ (Json.str m).compress ++ "]") ++ "]"

def renderOk (v : Val) (st : Store) : String :=
  "{\"ok\":" ++ renderVal st.heap [] v ++ ",\"log\":" ++ renderLog st.log ++ "}"

def renderError (e : String) : String :=
  "{\"error\":" ++ (Json.str e).compress ++ "}"

/-- Convert a JSON argument to a value of IR type `t`, allocating heap
objects for structs (`{"fields":[...]}`) and Vecs (`{"vec":[...]}`). -/
partial def argVal (p : Program) (t : Ty) (j : Json) : HM Val := do
  let lift {α} (x : Except String α) : HM α := liftM (m := Except String) x
  match t.kind, j with
  | .unit, Json.null => pure .unit
  | .bool, Json.bool b => pure (.bool b)
  | .int, _ =>
    let i ← lift j.getInt?
    if inRange i then pure (.int i) else throw s!"integer {i} out of i64 range"
  | .string, Json.str s => pure (.str s)
  | .ext, _ => pure (.opaque t.ext)
  | .agg, _ =>
    let some td := p.types[t.agg]? | throw s!"unknown aggregate {t.agg}"
    match td.coll, td.elem with
    | "vec", some et =>
      let xs ← lift (j.getObjValAs? (Array Json) "vec")
      alloc (.vec (← xs.mapM (argVal p et)))
    | _, _ =>
      let fs ← lift (j.getObjValAs? (Array Json) "fields")
      if td.isVariant then
        let tag ← lift (j.getObjValAs? Nat "tag")
        let some tys := td.variants[tag]? | throw s!"bad tag {tag} for {td.name}"
        if tys.size != fs.size then throw s!"wrong field count for {td.name}"
        pure (.variant tag (← (tys.zip fs).mapM fun (t, j) => argVal p t j))
      else
        if td.fields.size != fs.size then throw s!"wrong field count for {td.name}"
        alloc (.struct (← (td.fields.zip fs).mapM fun (t, j) => argVal p t j))
  | _, _ => throw s!"argument {j.compress} does not match type {repr t.kind}"

/-- Build the argument vector: `ext` parameters get an opaque capability. -/
def buildArgs (p : Program) (fn : Func) (cli : List String) : HM (Array Val) := do
  let mut out := #[]
  let mut rest := cli
  for t in fn.locals.extract 0 fn.nparams do
    if t.kind == .ext then
      out := out.push (.opaque t.ext)
    else
      match rest with
      | [] => throw s!"{fn.name}: too few arguments"
      | a :: r =>
        let j ← match Json.parse a with
          | .ok j => pure j
          | .error e => throw s!"cannot parse argument {a}: {e}"
        out := out.push (← argVal p t j)
        rest := r
  if !rest.isEmpty then throw s!"{fn.name}: too many arguments"
  return out

def usage : String :=
  "usage: kekkai-ref <ir.json> <funcName> <arg>...\n" ++
  "  args are JSON values; capability (ext) parameters are supplied automatically"

def cliMain (argv : List String) : IO UInt32 := do
  match argv with
  | file :: fname :: rest =>
    let src ← try IO.FS.readFile file catch e => do
      IO.eprintln s!"kekkai-ref: cannot read {file}: {e}"; return 1
    let p ← match parseProgram src with
      | .ok p => pure p
      | .error e => do IO.eprintln s!"kekkai-ref: bad IR: {e}"; return 1
    let some fn := p.findFunc? fname
      | do IO.eprintln s!"kekkai-ref: no function {fname}"; return 1
    let (args, st) ← match (buildArgs p fn rest).run {} with
      | .ok a => pure a
      | .error e => do IO.eprintln s!"kekkai-ref: {e}"; return 1
    match callFunc p fn args st with
    | .ok (v, st) => IO.println (renderOk v st)
    | .error e => IO.println (renderError e)
    return 0
  | _ =>
    IO.eprintln usage
    return 2

end Kekkai.IR
