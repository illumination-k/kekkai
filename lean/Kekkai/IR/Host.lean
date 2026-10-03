import Kekkai.IR.Arith
import Kekkai.IR.Value

/-!
# Pure host operations

Reimplements the synchronous, deterministic builtin operations (in wasm
they are functions of the runtime prelude, `lib/prelude`). Strings are
Lean strings; lengths are counted in UTF-16 code units.
-/

namespace Kekkai.IR

/-- `clock.now_ms` returns this fixed instant so runs are reproducible. -/
def fixedNowMs : Int := 1700000000000

/-- Length in UTF-16 code units. -/
def utf16Length (s : String) : Nat :=
  s.toList.foldl (fun n c => n + (if c.val > 0xFFFF then 2 else 1)) 0

/-- Unicode white space and line terminator code points (used by `trim`). -/
def isJsSpace (c : Char) : Bool :=
  let v := c.val
  v == 0x09 || v == 0x0A || v == 0x0B || v == 0x0C || v == 0x0D || v == 0x20 ||
  v == 0xA0 || v == 0x1680 || (0x2000 ≤ v && v ≤ 0x200A) || v == 0x2028 || v == 0x2029 ||
  v == 0x202F || v == 0x205F || v == 0x3000 || v == 0xFEFF

def jsTrim (s : String) : String :=
  String.ofList ((s.toList.dropWhile isJsSpace).reverse.dropWhile isJsSpace).reverse

/-- ASCII-only case mapping. -/
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

/-! ## UTF-16 / UTF-8 helpers (strings are UTF-16 in wasm) -/

def utf16Units (s : String) : Array Nat :=
  s.toList.foldl (fun acc c =>
    let v := c.toNat
    if v < 0x10000 then acc.push v
    else
      let w := v - 0x10000
      (acc.push (0xD800 + w / 0x400)).push (0xDC00 + w % 0x400)) #[]

/-- Decode UTF-16 code units. A lone surrogate (which a wasm string can hold
but a Lean string cannot) becomes U+FFFD. -/
def ofUtf16Units (us : List Nat) : String :=
  let rec go : List Nat → List Char → List Char
    | [], acc => acc.reverse
    | h :: l :: rest, acc =>
      if 0xD800 ≤ h && h < 0xDC00 && 0xDC00 ≤ l && l < 0xE000 then
        go rest (Char.ofNat (0x10000 + (h - 0xD800) * 0x400 + (l - 0xDC00)) :: acc)
      else go (l :: rest) (unit1 h :: acc)
    | [h], acc => go [] (unit1 h :: acc)
  String.ofList (go us [])
where
  unit1 (u : Nat) : Char := if 0xD800 ≤ u && u < 0xE000 then '\uFFFD' else Char.ofNat u

/-- The WHATWG UTF-8 decoder: invalid
sequences become U+FFFD (maximal subparts), and a leading BOM is removed. -/
def utf8DecodeWhatwg (bytes : List Nat) : String :=
  let bytes := match bytes with
    | 0xEF :: 0xBB :: 0xBF :: rest => rest
    | bs => bs
  let rec go (fuel : Nat) (bs : List Nat) (needed seen cp lower upper : Nat) (acc : List Char) : List Char :=
    match fuel with
    | 0 => acc.reverse
    | fuel + 1 =>
    match bs with
    | [] => (if needed != 0 then '\uFFFD' :: acc else acc).reverse
    | b :: rest =>
      if needed == 0 then
        if b ≤ 0x7F then go fuel rest 0 0 0 0x80 0xBF (Char.ofNat b :: acc)
        else if 0xC2 ≤ b && b ≤ 0xDF then go fuel rest 1 0 (b % 0x20) 0x80 0xBF acc
        else if 0xE0 ≤ b && b ≤ 0xEF then
          go fuel rest 2 0 (b % 0x10) (if b == 0xE0 then 0xA0 else 0x80) (if b == 0xED then 0x9F else 0xBF) acc
        else if 0xF0 ≤ b && b ≤ 0xF4 then
          go fuel rest 3 0 (b % 0x08) (if b == 0xF0 then 0x90 else 0x80) (if b == 0xF4 then 0x8F else 0xBF) acc
        else go fuel rest 0 0 0 0x80 0xBF ('\uFFFD' :: acc)
      else if b < lower || b > upper then
        -- reprocess this byte from the initial state
        go fuel (b :: rest) 0 0 0 0x80 0xBF ('\uFFFD' :: acc)
      else
        let cp := cp * 0x40 + b % 0x40
        if seen + 1 == needed then go fuel rest 0 0 0 0x80 0xBF (Char.ofNat cp :: acc)
        else go fuel rest needed (seen + 1) cp 0x80 0xBF acc
  String.ofList (go (2 * bytes.length + 1) bytes 0 0 0 0x80 0xBF [])

def utf8Encode (s : String) : List Nat := s.toUTF8.toList.map (·.toNat)

def listSplitOn (sep : List Char) (s : List Char) : List (List Char) :=
  -- `s.split(sep)` for a non-empty separator
  let rec go (fuel : Nat) (s cur : List Char) (acc : List (List Char)) : List (List Char) :=
    match fuel with
    | 0 => (cur.reverse :: acc).reverse
    | fuel + 1 =>
      match s with
      | [] => (cur.reverse :: acc).reverse
      | c :: rest =>
        if sep.isPrefixOf s then go fuel (s.drop sep.length) [] (cur.reverse :: acc)
        else go fuel rest (c :: cur) acc
  go (s.length + 1) s [] []

def jsSplit (s sep : String) : List String :=
  if sep.isEmpty then s.toList.map fun c => String.singleton c   -- code points
  else (listSplitOn sep.toList s.toList).map String.ofList

def jsReplaceAll (s a b : String) : String :=
  if a.isEmpty then s else b.intercalate (jsSplit s a)

/-- The first index of `t` in `s`, in UTF-16 code units. -/
def jsIndexOf (s t : String) : Option Nat :=
  let su := (utf16Units s).toList
  let tu := (utf16Units t).toList
  let rec go (i : Nat) : List Nat → Option Nat
    | [] => if tu.isEmpty then some i else none
    | l@(_ :: rest) => if tu.isPrefixOf l then some i else go (i + 1) rest
  go 0 su

/-- `string.slice` with indices clamped to `[0, len]`. -/
def jsSlice (s : String) (a b : Int) : String :=
  let us := utf16Units s
  let n : Int := us.size
  let clamp (x : Int) : Int := if x < 0 then 0 else if x > n then n else x
  let lo := clamp a
  let hi := clamp b
  if hi ≤ lo then "" else ofUtf16Units (us.extract lo.toNat hi.toNat).toList

/-- The one-code-unit string `c mod 2^16`. -/
def jsFromCharCode (c : Int) : String :=
  ofUtf16Units [(c % 65536).toNat]

/-! ## Host values -/

/-- Host `Option` results are lifted into the variant `None = 0 | Some = 1`. -/
def optionVal : Option Val → Val
  | none => .variant 0 #[]
  | some v => .variant 1 #[v]

def newVec (xs : Array Val) : HM Val := alloc (.vec xs)

def vecElems (v : Val) : HM (Array Val) := do
  match ← deref v with
  | .vec xs => pure xs
  | o => throw s!"type error: expected a Vec, got {repr o}"

/-- Evaluate a synchronous builtin operation (implemented by the runtime prelude). -/
def evalHost (name : String) (args : Array Val) : HM Val := do
  let bad : HM Val := throw s!"host op {name}: bad arguments"
  let int2 (f : Int → Int → Int) : HM Val :=
    match args with
    | #[.int x, .int y] => pure (.int (f x y))
    | _ => bad
  let logAt (level : String) : HM Val :=
    match args with
    | #[_, .str m] => do emitLog (level, m); pure .unit
    | _ => bad
  match name with
  | "int.to_string" => match args with | #[.int x] => pure (.str (toString x)) | _ => bad
  | "int.abs" => match args with
    | #[.int x] => pure (.int (wrap (if x < 0 then -x else x)))
    | _ => bad
  | "int.min" => int2 min
  | "int.max" => int2 max
  | "int.bit_and" => int2 i64And
  | "int.bit_or" => int2 i64Or
  | "int.bit_xor" => int2 i64Xor
  | "int.shl" => int2 i64Shl
  | "int.shr" => int2 i64ShrS
  | "int.ushr" => int2 i64ShrU
  | "bool.to_string" => match args with
    | #[.bool b] => pure (.str (if b then "true" else "false"))
    | _ => bad
  | "string.len" => match args with | #[.str s] => pure (.int (utf16Length s)) | _ => bad
  | "string.parse_int" => match args with
    | #[.str s] => pure (optionVal ((parseI64 s).map Val.int))
    | _ => bad
  | "string.contains" => match args with
    | #[.str s, .str t] => pure (.bool (strContains s t)) | _ => bad
  | "string.starts_with" => match args with
    | #[.str s, .str t] => pure (.bool (strStartsWith s t)) | _ => bad
  | "string.ends_with" => match args with
    | #[.str s, .str t] => pure (.bool (strEndsWith s t)) | _ => bad
  | "string.trim" => match args with | #[.str s] => pure (.str (jsTrim s)) | _ => bad
  | "string.to_upper" => match args with | #[.str s] => pure (.str (asciiUpper s)) | _ => bad
  | "string.to_lower" => match args with | #[.str s] => pure (.str (asciiLower s)) | _ => bad
  | "string.char_at" => match args with
    | #[.str s, .int i] =>
      let us := utf16Units s
      pure (optionVal (if 0 ≤ i ∧ i < us.size then some (.int us[i.toNat]!) else none))
    | _ => bad
  | "string.slice" => match args with
    | #[.str s, .int a, .int b] => pure (.str (jsSlice s a b)) | _ => bad
  | "string.index_of" => match args with
    | #[.str s, .str t] => pure (optionVal ((jsIndexOf s t).map fun i => .int i)) | _ => bad
  | "string.replace" => match args with
    | #[.str s, .str a, .str b] => pure (.str (jsReplaceAll s a b)) | _ => bad
  | "string.split" => match args with
    | #[.str s, .str sep] => newVec ((jsSplit s sep).toArray.map Val.str) | _ => bad
  | "string.to_bytes" => match args with
    | #[.str s] => newVec ((utf8Encode s).toArray.map fun (b : Nat) => .int (b : Int)) | _ => bad
  | "string.from_char" => match args with
    | #[.int c] => pure (.str (jsFromCharCode c)) | _ => bad
  | "string.from_bytes" => match args with
    | #[v] => do
      let xs ← vecElems v
      let bs ← xs.toList.mapM fun
        | .int b => pure (b % 256).toNat
        | _ => throw "host op string.from_bytes: bad element"
      pure (.str (utf8DecodeWhatwg bs))
    | _ => bad
  | "log.info" => logAt "info"
  | "log.warn" => logAt "warn"
  | "log.error" => logAt "error"
  | "clock.now_ms" => match args with | #[_] => pure (.int fixedNowMs) | _ => bad
  | _ => throw s!"unsupported host op {name}"

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
#guard jsSplit "a,b,,c" "," == ["a", "b", "", "c"]
#guard jsSplit "" "," == [""]
#guard jsSplit "a,b," "," == ["a", "b", ""]
#guard jsSplit "abc" "" == ["a", "b", "c"]
#guard jsSplit "aab" "ab" == ["a", ""]
#guard jsReplaceAll "a-b-c" "-" "+" == "a+b+c"
#guard jsReplaceAll "abc" "" "x" == "abc"
#guard jsIndexOf "hello" "ll" == some 2 && jsIndexOf "hello" "" == some 0 && jsIndexOf "hello" "z" == none
#guard jsSlice "hello" 1 3 == "el" && jsSlice "hello" (-5) 99 == "hello" && jsSlice "hello" 3 1 == ""
#guard jsFromCharCode 65 == "A" && jsFromCharCode (65 + 65536) == "A" && jsFromCharCode (-65471) == "A"
#guard utf8DecodeWhatwg [104, 105] == "hi"
#guard utf8DecodeWhatwg [0xEF, 0xBB, 0xBF, 65] == "A"
#guard utf8DecodeWhatwg [0xE3, 0x81, 0x82] == "あ"
#guard utf8DecodeWhatwg [0xE3, 0x81, 65] == "\uFFFDA"
#guard utf8DecodeWhatwg [0xFF, 0xC0, 0x80] == "\uFFFD\uFFFD\uFFFD"
#guard utf8DecodeWhatwg [0xF0, 0x9F, 0x98] == "\uFFFD"
#guard utf8Encode "あ" == [0xE3, 0x81, 0x82]
#guard ofUtf16Units (utf16Units "a😀b").toList == "a😀b"

end Kekkai.IR
