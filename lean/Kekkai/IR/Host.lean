import Kekkai.IR.Arith
import Kekkai.IR.Value

/-!
# Pure host operations

Reimplements the synchronous, deterministic part of the `host` object in
`internal/glue/runtime.js`. Strings are Lean strings; lengths are counted
in UTF-16 code units like JavaScript's `String.prototype.length`.
-/

namespace Kekkai.IR

/-- Log entries `(level, message)` emitted by `log.info/warn/error`. -/
abbrev LogEntry := String × String

/-- `clock.now_ms` returns this fixed instant so runs are reproducible. -/
def fixedNowMs : Int := 1700000000000

/-- Length in UTF-16 code units (JavaScript `s.length`). -/
def utf16Length (s : String) : Nat :=
  s.toList.foldl (fun n c => n + (if c.val > 0xFFFF then 2 else 1)) 0

/-- JavaScript `WhiteSpace` and `LineTerminator` code points (used by `trim`). -/
def isJsSpace (c : Char) : Bool :=
  let v := c.val
  v == 0x09 || v == 0x0A || v == 0x0B || v == 0x0C || v == 0x0D || v == 0x20 ||
  v == 0xA0 || v == 0x1680 || (0x2000 ≤ v && v ≤ 0x200A) || v == 0x2028 || v == 0x2029 ||
  v == 0x202F || v == 0x205F || v == 0x3000 || v == 0xFEFF

def jsTrim (s : String) : String :=
  String.ofList ((s.toList.dropWhile isJsSpace).reverse.dropWhile isJsSpace).reverse

/-- ASCII-only case mapping (JS maps all of Unicode; tests stay ASCII). -/
def asciiUpper (s : String) : String :=
  String.ofList (s.toList.map fun c => if 'a' ≤ c && c ≤ 'z' then Char.ofNat (c.toNat - 32) else c)

def asciiLower (s : String) : String :=
  String.ofList (s.toList.map fun c => if 'A' ≤ c && c ≤ 'Z' then Char.ofNat (c.toNat + 32) else c)

def listIsInfix (needle hay : List Char) : Bool :=
  match hay with
  | [] => needle.isEmpty
  | _ :: rest => needle.isPrefixOf hay || listIsInfix needle rest

def strContains (s t : String) : Bool := listIsInfix t.toList s.toList
def strStartsWith (s t : String) : Bool := t.toList.isPrefixOf s.toList
def strEndsWith (s t : String) : Bool := t.toList.reverse.isPrefixOf s.toList.reverse

/-- `string.parse_int`: accepts `^[+-]?[0-9]+$` and values that fit in an
`i64`; anything else is `none`. -/
def parseI64 (s : String) : Option Int :=
  let cs := s.toList
  let (neg, digits) := match cs with
    | '-' :: r => (true, r)
    | '+' :: r => (false, r)
    | r => (false, r)
  if digits.isEmpty || !digits.all Char.isDigit then none
  else
    let mag : Nat := digits.foldl (fun n c => n * 10 + (c.toNat - '0'.toNat)) 0
    let v : Int := if neg then -(mag : Int) else mag
    if inRange v then some v else none

/-- Host `Option` results are lifted into the variant `None = 0 | Some = 1`. -/
def optionVal : Option Val → Val
  | none => .variant 0 #[]
  | some v => .variant 1 #[v]

/-- Evaluate a synchronous host operation. Returns the result value and the
log entries it produced. -/
def evalHost (name : String) (args : Array Val) : Except String (Val × Array LogEntry) :=
  let bad : Except String (Val × Array LogEntry) :=
    .error s!"host op {name}: bad arguments"
  let pure' (v : Val) : Except String (Val × Array LogEntry) := .ok (v, #[])
  let logAt (level : String) : Except String (Val × Array LogEntry) :=
    match args with
    | #[_, .str m] => .ok (.unit, #[(level, m)])
    | _ => bad
  let int2 (f : Int → Int → Int) : Except String (Val × Array LogEntry) :=
    match args with
    | #[.int x, .int y] => pure' (.int (f x y))
    | _ => bad
  match name with
  | "int.to_string" => match args with | #[.int x] => pure' (.str (toString x)) | _ => bad
  | "int.abs" => match args with
    | #[.int x] => pure' (.int (wrap (if x < 0 then -x else x)))
    | _ => bad
  | "int.bit_and" => int2 i64And
  | "int.bit_or" => int2 i64Or
  | "int.bit_xor" => int2 i64Xor
  | "int.shl" => int2 i64Shl
  | "int.shr" => int2 i64ShrS
  | "int.ushr" => int2 i64ShrU
  | "bool.to_string" => match args with
    | #[.bool b] => pure' (.str (if b then "true" else "false"))
    | _ => bad
  | "string.len" => match args with | #[.str s] => pure' (.int (utf16Length s)) | _ => bad
  | "string.parse_int" => match args with
    | #[.str s] => pure' (optionVal ((parseI64 s).map Val.int))
    | _ => bad
  | "string.contains" => match args with
    | #[.str s, .str t] => pure' (.bool (strContains s t)) | _ => bad
  | "string.starts_with" => match args with
    | #[.str s, .str t] => pure' (.bool (strStartsWith s t)) | _ => bad
  | "string.ends_with" => match args with
    | #[.str s, .str t] => pure' (.bool (strEndsWith s t)) | _ => bad
  | "string.trim" => match args with | #[.str s] => pure' (.str (jsTrim s)) | _ => bad
  | "string.to_upper" => match args with | #[.str s] => pure' (.str (asciiUpper s)) | _ => bad
  | "string.to_lower" => match args with | #[.str s] => pure' (.str (asciiLower s)) | _ => bad
  | "log.info" => logAt "info"
  | "log.warn" => logAt "warn"
  | "log.error" => logAt "error"
  | "clock.now_ms" => match args with | #[_] => pure' (.int fixedNowMs) | _ => bad
  | _ => .error s!"unsupported host op {name}"

/-! Sanity checks (evaluated at compile time). -/
#guard parseI64 "42" == some 42
#guard parseI64 "+7" == some 7
#guard parseI64 "-0012" == some (-12)
#guard parseI64 "9223372036854775807" == some 9223372036854775807
#guard parseI64 "9223372036854775808" == none
#guard parseI64 "-9223372036854775808" == some (-9223372036854775808)
#guard parseI64 "" == none
#guard parseI64 "-" == none
#guard parseI64 "1 " == none
#guard parseI64 "1a" == none
#guard jsTrim " \t ab c\n " == "ab c"
#guard strContains "hello" "" && strContains "hello" "ll" && !strContains "hello" "lo!"
#guard strStartsWith "hello" "he" && !strStartsWith "he" "hello"
#guard strEndsWith "hello" "lo" && strEndsWith "hello" ""
#guard asciiUpper "abZ-1" == "ABZ-1" && asciiLower "AbZ-1" == "abz-1"
#guard utf16Length "abc" == 3

end Kekkai.IR
