// Random well-typed Kekkai programs for differential testing (WasmGC vs the
// Lean reference interpreter of the IR, see docs/design.md §検証戦略).
//
// This is a line-by-line port of internal/difftest/gen.go: the methods, the
// case numbering of every `switch r.Intn(n)` and the emitted text are kept
// identical so that changes to the Go generator can be re-applied here
// mechanically (`fmt.Fprintf(&g.b, f, ...)` -> `this.w(...)` with a template
// string, `g.r.Intn(n)` -> `this.r.intn(n)`). Go evaluates the arguments of
// `g.pick(...)` / `fmt.Sprintf(...)` left to right before the call; JS
// argument lists and template literals do the same, so expressions are
// written inline in the same order (this matters for the random stream and
// the fresh-name counter). Only the random source differs (Go's math/rand
// is not reproduced), so a seed gives a different program than the Go
// generator did; the distribution is the same.
//
// Programs are pure functions over Int and Bool exercising arithmetic edge
// cases, control flow, enums, Option, `?`, loops, string host operations,
// Vec/Map collections, `for` loops with break/continue and mutable structs
// shared through aliases. Function i may only call functions j < i, loops
// are bounded (`while` and `for` over ranges with small constant trip
// counts; `for` over a Vec never pushes in its body), so every program
// terminates.

/** Small deterministic PRNG (splitmix32 seeding + sfc32). */
export class Rand {
  constructor(seed) {
    let s = (Number(seed) | 0) >>> 0;
    const next = () => {
      s = (s + 0x9e3779b9) >>> 0;
      let z = s;
      z = Math.imul(z ^ (z >>> 16), 0x85ebca6b) >>> 0;
      z = Math.imul(z ^ (z >>> 13), 0xc2b2ae35) >>> 0;
      return (z ^ (z >>> 16)) >>> 0;
    };
    this.a = next(); this.b = next(); this.c = next(); this.d = next();
    for (let i = 0; i < 12; i++) this.u32();
  }
  u32() {
    const t = (((this.a + this.b) >>> 0) + this.d) >>> 0;
    this.d = (this.d + 1) >>> 0;
    this.a = this.b ^ (this.b >>> 9);
    this.b = (this.c + (this.c << 3)) >>> 0;
    this.c = ((this.c << 21) | (this.c >>> 11)) >>> 0;
    this.c = (this.c + t) >>> 0;
    return t;
  }
  /** Uniform integer in [0, n). */
  intn(n) {
    if (!(n > 0)) throw new Error("intn: n must be positive");
    return Math.floor((this.u32() / 0x100000000) * n);
  }
}

export const prelude = `enum Shape {
    Dot,
    Line(Int),
    Rect(Int, Int),
    Flag(Bool, Int),
}

fn half(x: Int) -> Option<Int> {
    if x % 2 == 0 { Some(x / 2) } else { None }
}

fn quarter(x: Int) -> Option<Int> {
    let h = half(x)?;
    half(h)
}

fn area(s: Shape) -> Int {
    match s {
        Shape::Dot => 0,
        Shape::Line(n) => n,
        Shape::Rect(w, h) => w * h,
        Shape::Flag(true, n) => n,
        Shape::Flag(false, _) => -1,
    }
}

struct Acc {
    total: Int,
    hits: Int,
    log: Vec<Int>,
}

// mutates its argument: visible through every alias of acc
fn bump(acc: Acc, x: Int) {
    acc.total = acc.total + x;
    acc.hits = acc.hits + 1;
    acc.log.push(x);
}

fn vsum(v: Vec<Int>) -> Int {
    let mut s = 0;
    for x in v {
        s = s * 3 + x;
    }
    s
}

fn mfold(m: Map<Int, Int>) -> Int {
    let mut s = 0;
    for k in m.keys() {
        s = s * 31 + k * 7 + m.get(k).unwrap_or(0);
    }
    s
}

fn sfold(m: Map<String, Int>) -> Int {
    let mut s = 0;
    for k in m.keys() {
        s = s * 31 + hash(k) + m.get(k).unwrap_or(0);
    }
    s
}

fn hash(s: String) -> Int {
    let mut h = 7;
    for b in s.to_bytes() {
        h = h * 31 + b;
    }
    h
}

`;

export const interesting = [
  "0", "1", "2", "3", "7", "10", "100", "255", "65536",
  "2147483647", "4294967296", "9223372036854775807", "(-9223372036854775807 - 1)",
];

export const strLits = [`""`, `","`, `"a"`, `"ab"`, `"a,b,,c"`, `"  x y  "`, `"12"`, `"-7"`, `"hello"`, `"9223372036854775808"`, `"Hi, There"`];

/**
 * A variable in scope: { name, ty, mut }; ty is "Int", "Bool", "Vec"
 * (Vec<Int>), "MapI" (Map<Int, Int>), "MapS" (Map<String, Int>) or "Acc".
 */
const genVar = (name, ty, mut) => ({ name, ty, mut });

export class Gen {
  constructor(seed) {
    this.r = new Rand(seed);
    this.b = "";
    this.nfuncs = 0;
    this.vars = [];
    this.depth = 0;
    this.fresh = 0;
    // loop nesting depth of the statement being generated
    this.loops = 0;
    // inside `for x in vec`: no pushes (the loop re-reads the length)
    this.noPush = false;
  }

  w(s) { this.b += s; }

  /** @returns {{source: string, funcs: string[]}} each fn takes (a: Int, b: Int, c: Bool) -> Int */
  generate() {
    this.b = "";
    this.w(prelude);
    this.nfuncs = 2 + this.r.intn(4);
    const names = [];
    for (let i = 0; i < this.nfuncs; i++) {
      const name = `f${i}`;
      names.push(name);
      this.fn(i, name);
    }
    return { source: this.b, funcs: names };
  }

  pick(...xs) { return xs[this.r.intn(xs.length)]; }

  name(prefix) {
    this.fresh++;
    return `${prefix}${this.fresh}`;
  }

  fn(idx, name) {
    this.vars = [genVar("a", "Int", false), genVar("b", "Int", false), genVar("c", "Bool", false)];
    this.depth = 0;
    this.w(`fn ${name}(a: Int, b: Int, c: Bool) -> Int {\n`);
    const n = 1 + this.r.intn(7);
    for (let i = 0; i < n; i++) this.stmt(idx, "    ");
    this.w(`    ${this.intExpr(idx, 3)}\n}\n\n`);
  }

  varsOf(ty, mutOnly) {
    return this.vars.filter((v) => v.ty === ty && (!mutOnly || v.mut));
  }

  stmt(idx, ind) {
    const k = this.r.intn(18);
    if (k >= 5) {
      // 0..9 are collection statements; 10..12 favour mutating existing ones
      let c = k - 5;
      if (c >= 10) c = [1, 3, 5][c - 10];
      this.collStmt(idx, ind, c);
      return;
    }
    switch (k) {
      case 0:
      case 1: {
        const v = this.name("x");
        this.w(`${ind}let ${v} = ${this.intExpr(idx, 3)};\n`);
        this.vars.push(genVar(v, "Int", false));
        break;
      }
      case 2: {
        const v = this.name("p");
        this.w(`${ind}let ${v} = ${this.boolExpr(idx, 2)};\n`);
        this.vars.push(genVar(v, "Bool", false));
        break;
      }
      case 3: {
        // bounded loop accumulating into a mutable variable
        const acc = this.name("acc"), i = this.name("i");
        this.w(`${ind}let mut ${acc} = ${this.intExpr(idx, 2)};\n`);
        this.w(`${ind}let mut ${i} = 0;\n`);
        const saved = [...this.vars];
        this.vars.push(genVar(acc, "Int", true));
        this.w(`${ind}while ${i} < ${1 + this.r.intn(6)} {\n`);
        this.w(`${ind}    ${acc} = ${this.intExpr(idx, 2)};\n`);
        this.w(`${ind}    ${i} = ${i} + 1;\n`);
        this.w(`${ind}}\n`);
        this.vars = [...saved, genVar(acc, "Int", true)];
        break;
      }
      case 4: {
        const ms = this.varsOf("Int", true);
        if (ms.length > 0) {
          const v = ms[this.r.intn(ms.length)];
          this.w(`${ind}${v.name} = ${this.intExpr(idx, 2)};\n`);
        } else {
          const v = this.name("m");
          this.w(`${ind}let mut ${v} = ${this.intExpr(idx, 2)};\n`);
          this.vars.push(genVar(v, "Int", true));
        }
        break;
      }
    }
  }

  intExpr(idx, d) {
    if (d <= 0) return this.intLeaf();
    const k = this.r.intn(22);
    if (k >= 14) return this.collIntExpr(idx, d, k - 14);
    switch (this.r.intn(14)) {
      case 0:
      case 1:
        return this.intLeaf();
      case 2:
      case 3:
      case 4: {
        const op = this.pick("+", "-", "*", "/", "%", "+", "-");
        return `(${this.intExpr(idx, d - 1)} ${op} ${this.intExpr(idx, d - 1)})`;
      }
      case 5:
        return `(-${this.intExpr(idx, d - 1)})`;
      case 6:
        return `(if ${this.boolExpr(idx, d - 1)} { ${this.intExpr(idx, d - 1)} } else { ${this.intExpr(idx, d - 1)} })`;
      case 7:
        if (idx > 0) {
          const callee = this.r.intn(idx);
          return `f${callee}(${this.intExpr(idx, d - 1)}, ${this.intExpr(idx, d - 1)}, ${this.boolExpr(idx, d - 1)})`;
        }
        return this.intLeaf();
      case 8: {
        const x = this.name("y");
        return `(match quarter(${this.intExpr(idx, d - 1)}) { Some(${x}) => ${x} + ${this.intLeaf()}, None => ${this.intExpr(idx, d - 1)} })`;
      }
      case 9:
        return `area(${this.shape(idx, d - 1)})`;
      case 10:
        return `${this.intExpr(idx, d - 1)}.to_string().len()`;
      case 11:
        return `${this.intExpr(idx, d - 1)}.abs()`;
      case 12:
        return `(match ${this.intExpr(idx, d - 1)} { 0 => ${this.intLeaf()}, 1 => ${this.intLeaf()}, _ => ${this.intExpr(idx, d - 1)} })`;
      default: {
        const v = this.name("z");
        return `{ let ${v} = ${this.intExpr(idx, d - 1)}; ${v} * 2 - ${v} }`;
      }
    }
  }

  shape(idx, d) {
    switch (this.r.intn(4)) {
      case 0:
        return "Shape::Dot";
      case 1:
        return `Shape::Line(${this.intExpr(idx, d)})`;
      case 2:
        return `Shape::Rect(${this.intExpr(idx, d)}, ${this.intExpr(idx, d)})`;
      default:
        return `Shape::Flag(${this.boolExpr(idx, d)}, ${this.intExpr(idx, d)})`;
    }
  }

  intLeaf() {
    const vs = this.varsOf("Int", false);
    if (vs.length > 0 && this.r.intn(3) > 0) return vs[this.r.intn(vs.length)].name;
    return interesting[this.r.intn(interesting.length)];
  }

  boolExpr(idx, d) {
    if (d <= 0) return this.boolLeaf();
    switch (this.r.intn(7)) {
      case 0:
        return this.boolLeaf();
      case 1:
      case 2: {
        const op = this.pick("<", "<=", ">", ">=", "==", "!=");
        return `(${this.intExpr(idx, d - 1)} ${op} ${this.intExpr(idx, d - 1)})`;
      }
      case 3:
        return `(${this.boolExpr(idx, d - 1)} ${this.pick("&&", "||")} ${this.boolExpr(idx, d - 1)})`;
      case 4:
        return `(!${this.boolExpr(idx, d - 1)})`;
      case 5:
        return `(${this.intExpr(idx, d - 1)}.to_string() == ${this.intExpr(idx, d - 1)}.to_string())`;
      default:
        return `(${this.boolExpr(idx, d - 1)} == ${this.boolExpr(idx, d - 1)})`;
    }
  }

  boolLeaf() {
    const vs = this.varsOf("Bool", false);
    if (vs.length > 0 && this.r.intn(2) === 0) return vs[this.r.intn(vs.length)].name;
    return this.pick("true", "false");
  }

  // ---- collections, structs, for loops and strings ----

  /** A random variable of type ty, or null. */
  pickVar(ty) {
    const vs = this.varsOf(ty, false);
    if (vs.length === 0) return null;
    return vs[this.r.intn(vs.length)];
  }

  declare(ty, prefix, mut) {
    const v = this.name(prefix);
    this.vars.push(genVar(v, ty, mut));
    return v;
  }

  // smallInt is an Int expression in [0, 8] used as a loop trip count
  // (`lo..(lo + n)` is then empty or has at most 8 iterations, even when
  // `lo + n` wraps around).
  smallInt(idx) {
    if (this.r.intn(2) === 0) return String(this.r.intn(9));
    return `(${this.intExpr(idx, 1)} % 9).abs()`;
  }

  collStmt(idx, ind, k) {
    switch (k) {
      case 0: { // new Vec, optionally prefilled
        const v = this.name("v");
        this.w(`${ind}let ${v}: Vec<Int> = Vec::new();\n`);
        for (let i = this.r.intn(4); i > 0; i--) {
          this.w(`${ind}${v}.push(${this.intExpr(idx, 2)});\n`);
        }
        this.vars.push(genVar(v, "Vec", false));
        break;
      }
      case 1: { // Vec mutation
        const v = this.pickVar("Vec");
        if (!v) { this.collStmt(idx, ind, 0); return; }
        switch (this.r.intn(4)) {
          case 0:
            if (!this.noPush) {
              this.w(`${ind}${v.name}.push(${this.intExpr(idx, 2)});\n`);
              return;
            }
          // fallthrough
          case 1: {
            const i = this.vecIndex(idx), x = this.intExpr(idx, 2);
            const p = this.declare("Bool", "p", false);
            this.w(`${ind}let ${p} = ${v.name}.set(${i}, ${x});\n`);
            break;
          }
          case 2: {
            const d = this.intLeaf();
            const x = this.declare("Int", "x", false);
            this.w(`${ind}let ${x} = ${v.name}.pop().unwrap_or(${d});\n`);
            break;
          }
          default: { // alias
            const w = this.declare("Vec", "w", false);
            this.w(`${ind}let ${w} = ${v.name};\n`);
          }
        }
        break;
      }
      case 2: // new Map
        if (this.r.intn(2) === 0) {
          const m = this.name("m");
          this.w(`${ind}let ${m}: Map<Int, Int> = Map::new();\n`);
          this.vars.push(genVar(m, "MapI", false));
          for (let i = this.r.intn(4); i > 0; i--) {
            this.w(`${ind}${m}.insert(${this.mapKey(idx, "MapI")}, ${this.intExpr(idx, 2)});\n`);
          }
        } else {
          const m = this.name("n");
          this.w(`${ind}let ${m}: Map<String, Int> = Map::new();\n`);
          this.vars.push(genVar(m, "MapS", false));
          for (let i = this.r.intn(4); i > 0; i--) {
            this.w(`${ind}${m}.insert(${this.mapKey(idx, "MapS")}, ${this.intExpr(idx, 2)});\n`);
          }
        }
        break;
      case 3: { // Map mutation
        const ty = this.pick("MapI", "MapS");
        const m = this.pickVar(ty);
        if (!m) { this.collStmt(idx, ind, 2); return; }
        const key = this.mapKey(idx, ty);
        switch (this.r.intn(4)) {
          case 0:
          case 1:
            this.w(`${ind}${m.name}.insert(${key}, ${this.intExpr(idx, 2)});\n`);
            break;
          case 2:
            this.w(`${ind}${m.name}.remove(${key});\n`);
            break;
          default: {
            const w = this.declare(ty, "alias", false);
            this.w(`${ind}let ${w} = ${m.name};\n`);
          }
        }
        break;
      }
      case 4: { // struct
        const e = this.intExpr(idx, 2);
        const a = this.name("s");
        this.w(`${ind}let ${a} = Acc { total: ${e}, hits: 0, log: Vec::new() };\n`);
        this.vars.push(genVar(a, "Acc", false));
        break;
      }
      case 5: { // struct mutation through an alias or a call
        const a = this.pickVar("Acc");
        if (!a) { this.collStmt(idx, ind, 4); return; }
        switch (this.r.intn(4)) {
          case 0:
            this.w(`${ind}${a.name}.total = ${this.intExpr(idx, 2)};\n`);
            break;
          case 1:
            if (!this.noPush) {
              this.w(`${ind}bump(${a.name}, ${this.intExpr(idx, 2)});\n`);
              return;
            }
            this.w(`${ind}${a.name}.hits = ${a.name}.hits * 2;\n`);
            break;
          case 2: {
            const w = this.declare("Acc", "t", false);
            this.w(`${ind}let ${w} = ${a.name};\n`);
            break;
          }
          default:
            if (!this.noPush) {
              this.w(`${ind}${a.name}.log.push(${this.intExpr(idx, 1)});\n`);
              return;
            }
            this.w(`${ind}${a.name}.total = ${a.name}.total - ${a.name}.log.len();\n`);
        }
        break;
      }
      case 6:
      case 7: // for loops
        if (this.loops >= 2) { this.stmt(idx, ind); return; }
        this.forLoop(idx, ind);
        break;
      case 8: { // string-valued binding folded into an Int
        const e = this.strExpr(idx, 3);
        const x = this.declare("Int", "h", false);
        this.w(`${ind}let ${x} = hash(${e});\n`);
        break;
      }
      case 9: { // log-like Vec<String> join
        const e = `hash(${this.strExpr(idx, 2)}.split(${this.strLit()}).join(${this.strLit()}))`;
        const x = this.declare("Int", "j", false);
        this.w(`${ind}let ${x} = ${e};\n`);
        break;
      }
      default:
        this.stmt(idx, ind);
    }
  }

  vecIndex(idx) {
    return this.pick("0", "1", "2", "-1", "3", "100", `(${this.intExpr(idx, 1)} % 4)`);
  }

  mapKey(idx, ty) {
    if (ty === "MapI") return this.pick("0", "1", "2", "-1", `(${this.intExpr(idx, 1)} % 3)`, this.intLeaf());
    return this.pick(`"a"`, `"b"`, `""`, `"ab"`, `(${this.intExpr(idx, 1)} % 3).to_string()`);
  }

  forLoop(idx, ind) {
    const acc = this.name("acc");
    this.w(`${ind}let mut ${acc} = ${this.intExpr(idx, 1)};\n`);
    const saved = [...this.vars], savedNoPush = this.noPush;
    const x = this.name("k");
    let v, a;
    if ((v = this.pickVar("Vec")) && this.r.intn(2) === 0) {
      this.w(`${ind}for ${x} in ${v.name} {\n`);
      this.noPush = true;
    } else if ((a = this.pickVar("Acc")) && this.r.intn(3) === 0) {
      this.w(`${ind}for ${x} in ${a.name}.log {\n`);
      this.noPush = true;
    } else {
      const lo = this.pick("0", "1", "-2", this.intLeaf());
      this.w(`${ind}for ${x} in ${lo}..(${lo} + ${this.smallInt(idx)}) {\n`);
    }
    this.vars.push(genVar(x, "Int", false), genVar(acc, "Int", true));
    this.loops++;
    const inner = ind + "    ";
    for (let n = 1 + this.r.intn(3); n > 0; n--) {
      switch (this.r.intn(5)) {
        case 0:
          this.w(`${inner}if ${this.boolExpr(idx, 1)} { continue; }\n`);
          break;
        case 1:
          this.w(`${inner}if ${this.boolExpr(idx, 1)} { break; }\n`);
          break;
        default:
          this.stmt(idx, inner);
      }
    }
    this.w(`${inner}${acc} = ${this.intExpr(idx, 2)};\n`);
    this.loops--;
    this.w(`${ind}}\n`);
    // collections declared in the body are dropped; outer ones may have been mutated
    this.vars = [...saved, genVar(acc, "Int", true)];
    this.noPush = savedNoPush;
  }

  collIntExpr(idx, d, k) {
    switch (k) {
      case 0:
      case 1: {
        const v = this.pickVar("Vec");
        if (v) {
          switch (this.r.intn(3)) {
            case 0: return `${v.name}.len()`;
            case 1: return `${v.name}.get(${this.vecIndex(idx)}).unwrap_or(${this.intLeaf()})`;
            default: return `vsum(${v.name})`;
          }
        }
        break;
      }
      case 2: {
        const ty = this.pick("MapI", "MapS");
        const m = this.pickVar(ty);
        if (m) {
          const key = this.mapKey(idx, ty);
          switch (this.r.intn(4)) {
            case 0: return `${m.name}.len()`;
            case 1: return `${m.name}.get(${key}).unwrap_or(${this.intLeaf()})`;
            case 2: return `(if ${m.name}.contains(${key}) { ${this.intLeaf()} } else { ${this.intLeaf()} })`;
            default: return ty === "MapI" ? `mfold(${m.name})` : `sfold(${m.name})`;
          }
        }
        break;
      }
      case 3: {
        const a = this.pickVar("Acc");
        if (a) return this.pick(a.name + ".total", a.name + ".hits", a.name + ".log.len()", "vsum(" + a.name + ".log)");
        break;
      }
      case 4:
        return `hash(${this.strExpr(idx, d - 1)})`;
      case 5: {
        const s = this.strExpr(idx, d - 1);
        switch (this.r.intn(5)) {
          case 0: return `${s}.len()`;
          case 1: return `${s}.index_of(${this.strExpr(idx, d - 1)}).unwrap_or(-1)`;
          case 2: return `${s}.char_at(${this.vecIndex(idx)}).unwrap_or(-1)`;
          case 3: return `${s}.split(${this.strLit()}).len()`;
          default: return `${s}.parse_int().unwrap_or(${this.intLeaf()})`;
        }
      }
      case 6:
        return `${this.intExpr(idx, d - 1)}.${this.pick("min", "max")}(${this.intExpr(idx, d - 1)})`;
      case 7:
        return `${this.intExpr(idx, d - 1)}.${this.pick("bit_and", "bit_or", "bit_xor", "shl", "shr", "ushr")}(${this.intExpr(idx, d - 1)})`;
    }
    return this.intLeaf();
  }

  strLit() { return strLits[this.r.intn(strLits.length)]; }

  strExpr(idx, d) {
    if (d <= 0) return this.strLit();
    switch (this.r.intn(10)) {
      case 0:
      case 1: return this.strLit();
      case 2: return `${this.intExpr(idx, d - 1)}.to_string()`;
      case 3: return `(${this.strExpr(idx, d - 1)} + ${this.strExpr(idx, d - 1)})`;
      case 4: return `${this.strExpr(idx, d - 1)}.slice(${this.vecIndex(idx)}, ${this.vecIndex(idx)})`;
      case 5: return `${this.strExpr(idx, d - 1)}.replace(${this.strLit()}, ${this.strLit()})`;
      case 6: return `${this.strExpr(idx, d - 1)}.${this.pick("trim", "to_upper", "to_lower")}()`;
      case 7: return `String::from_char(32 + (${this.intExpr(idx, d - 1)} % 90).abs())`;
      case 8: return `String::from_bytes(${this.strExpr(idx, d - 1)}.to_bytes())`;
      default: return `${this.strExpr(idx, d - 1)}.split(${this.strLit()}).join(${this.strLit()})`;
    }
  }
}

/** The arguments every generated function is called with. */
export const argSets = [
  ["0", "0", false], ["1", "-1", true], ["7", "3", true], ["-13", "4", false],
  ["9223372036854775807", "-1", true], ["-9223372036854775808", "-1", false],
  ["123456789", "0", true], ["42", "65536", false],
];

// node tests/difftest/gen.mjs [seed]: print the program for a seed.
if (process.argv[1] && import.meta.url === new URL(`file://${process.argv[1]}`).href) {
  process.stdout.write(new Gen(Number(process.argv[2] ?? 1)).generate().source);
}
