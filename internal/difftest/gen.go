// Package difftest generates random well-typed Kekkai programs and
// compares the compiled WasmGC against the Lean reference interpreter of
// the IR (differential testing, see docs/design.md §検証戦略).
package difftest

import (
	"fmt"
	"math/rand"
	"strings"
)

// Gen produces random programs made of pure functions over Int and Bool,
// exercising arithmetic edge cases, control flow, enums, Option, `?`,
// loops, string host operations, Vec/Map collections, `for` loops with
// break/continue and mutable structs shared through aliases. Function i
// may only call functions j < i, loops are bounded (`while` and `for` over
// ranges with small constant trip counts; `for` over a Vec never pushes in
// its body), so every program terminates.
type Gen struct {
	r      *rand.Rand
	b      strings.Builder
	nfuncs int
	vars   []genVar
	depth  int
	fresh  int
	// loop nesting depth of the statement being generated
	loops int
	// inside `for x in vec`: no pushes (the loop re-reads the length)
	noPush bool
}

type genVar struct {
	name string
	ty   string // "Int", "Bool", "Vec" (Vec<Int>), "MapI" (Map<Int, Int>), "MapS" (Map<String, Int>), "Acc"
	mut  bool
}

// Program is a generated program and the functions to test.
type Program struct {
	Source string
	Funcs  []string // each takes (a: Int, b: Int, c: Bool) -> Int
}

const prelude = `enum Shape {
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

`

var interesting = []string{
	"0", "1", "2", "3", "7", "10", "100", "255", "65536",
	"2147483647", "4294967296", "9223372036854775807", "(-9223372036854775807 - 1)",
}

func New(seed int64) *Gen { return &Gen{r: rand.New(rand.NewSource(seed))} }

func (g *Gen) Generate() Program {
	g.b.Reset()
	g.b.WriteString(prelude)
	g.nfuncs = 2 + g.r.Intn(4)
	var names []string
	for i := 0; i < g.nfuncs; i++ {
		name := fmt.Sprintf("f%d", i)
		names = append(names, name)
		g.fn(i, name)
	}
	return Program{Source: g.b.String(), Funcs: names}
}

func (g *Gen) pick(xs ...string) string { return xs[g.r.Intn(len(xs))] }

func (g *Gen) name(prefix string) string {
	g.fresh++
	return fmt.Sprintf("%s%d", prefix, g.fresh)
}

func (g *Gen) fn(idx int, name string) {
	g.vars = []genVar{{"a", "Int", false}, {"b", "Int", false}, {"c", "Bool", false}}
	g.depth = 0
	fmt.Fprintf(&g.b, "fn %s(a: Int, b: Int, c: Bool) -> Int {\n", name)
	n := 1 + g.r.Intn(7)
	for i := 0; i < n; i++ {
		g.stmt(idx, "    ")
	}
	fmt.Fprintf(&g.b, "    %s\n}\n\n", g.intExpr(idx, 3))
}

func (g *Gen) varsOf(ty string, mutOnly bool) []genVar {
	var out []genVar
	for _, v := range g.vars {
		if v.ty == ty && (!mutOnly || v.mut) {
			out = append(out, v)
		}
	}
	return out
}

func (g *Gen) stmt(idx int, ind string) {
	k := g.r.Intn(18)
	if k >= 5 {
		// 0..9 are collection statements; 10..12 favour mutating existing ones
		c := k - 5
		if c >= 10 {
			c = []int{1, 3, 5}[c-10]
		}
		g.collStmt(idx, ind, c)
		return
	}
	switch k {
	case 0, 1:
		v := g.name("x")
		fmt.Fprintf(&g.b, "%slet %s = %s;\n", ind, v, g.intExpr(idx, 3))
		g.vars = append(g.vars, genVar{v, "Int", false})
	case 2:
		v := g.name("p")
		fmt.Fprintf(&g.b, "%slet %s = %s;\n", ind, v, g.boolExpr(idx, 2))
		g.vars = append(g.vars, genVar{v, "Bool", false})
	case 3:
		// bounded loop accumulating into a mutable variable
		acc, i := g.name("acc"), g.name("i")
		fmt.Fprintf(&g.b, "%slet mut %s = %s;\n", ind, acc, g.intExpr(idx, 2))
		fmt.Fprintf(&g.b, "%slet mut %s = 0;\n", ind, i)
		saved := g.vars
		g.vars = append(g.vars, genVar{acc, "Int", true})
		fmt.Fprintf(&g.b, "%swhile %s < %d {\n", ind, i, 1+g.r.Intn(6))
		fmt.Fprintf(&g.b, "%s    %s = %s;\n", ind, acc, g.intExpr(idx, 2))
		fmt.Fprintf(&g.b, "%s    %s = %s + 1;\n", ind, i, i)
		fmt.Fprintf(&g.b, "%s}\n", ind)
		g.vars = append(saved, genVar{acc, "Int", true})
	case 4:
		if ms := g.varsOf("Int", true); len(ms) > 0 {
			v := ms[g.r.Intn(len(ms))]
			fmt.Fprintf(&g.b, "%s%s = %s;\n", ind, v.name, g.intExpr(idx, 2))
		} else {
			v := g.name("m")
			fmt.Fprintf(&g.b, "%slet mut %s = %s;\n", ind, v, g.intExpr(idx, 2))
			g.vars = append(g.vars, genVar{v, "Int", true})
		}
	}
}

func (g *Gen) intExpr(idx, d int) string {
	if d <= 0 {
		return g.intLeaf()
	}
	if k := g.r.Intn(22); k >= 14 {
		return g.collIntExpr(idx, d, k-14)
	}
	switch g.r.Intn(14) {
	case 0, 1:
		return g.intLeaf()
	case 2, 3, 4:
		op := g.pick("+", "-", "*", "/", "%", "+", "-")
		return fmt.Sprintf("(%s %s %s)", g.intExpr(idx, d-1), op, g.intExpr(idx, d-1))
	case 5:
		return fmt.Sprintf("(-%s)", g.intExpr(idx, d-1))
	case 6:
		return fmt.Sprintf("(if %s { %s } else { %s })", g.boolExpr(idx, d-1), g.intExpr(idx, d-1), g.intExpr(idx, d-1))
	case 7:
		if idx > 0 {
			callee := g.r.Intn(idx)
			return fmt.Sprintf("f%d(%s, %s, %s)", callee, g.intExpr(idx, d-1), g.intExpr(idx, d-1), g.boolExpr(idx, d-1))
		}
		return g.intLeaf()
	case 8:
		x := g.name("y")
		return fmt.Sprintf("(match quarter(%s) { Some(%s) => %s + %s, None => %s })",
			g.intExpr(idx, d-1), x, x, g.intLeaf(), g.intExpr(idx, d-1))
	case 9:
		return fmt.Sprintf("area(%s)", g.shape(idx, d-1))
	case 10:
		return fmt.Sprintf("%s.to_string().len()", g.intExpr(idx, d-1))
	case 11:
		return fmt.Sprintf("%s.abs()", g.intExpr(idx, d-1))
	case 12:
		return fmt.Sprintf("(match %s { 0 => %s, 1 => %s, _ => %s })", g.intExpr(idx, d-1), g.intLeaf(), g.intLeaf(), g.intExpr(idx, d-1))
	default:
		v := g.name("z")
		return fmt.Sprintf("{ let %s = %s; %s * 2 - %s }", v, g.intExpr(idx, d-1), v, v)
	}
}

func (g *Gen) shape(idx, d int) string {
	switch g.r.Intn(4) {
	case 0:
		return "Shape::Dot"
	case 1:
		return fmt.Sprintf("Shape::Line(%s)", g.intExpr(idx, d))
	case 2:
		return fmt.Sprintf("Shape::Rect(%s, %s)", g.intExpr(idx, d), g.intExpr(idx, d))
	default:
		return fmt.Sprintf("Shape::Flag(%s, %s)", g.boolExpr(idx, d), g.intExpr(idx, d))
	}
}

func (g *Gen) intLeaf() string {
	vs := g.varsOf("Int", false)
	if len(vs) > 0 && g.r.Intn(3) > 0 {
		return vs[g.r.Intn(len(vs))].name
	}
	return interesting[g.r.Intn(len(interesting))]
}

func (g *Gen) boolExpr(idx, d int) string {
	if d <= 0 {
		return g.boolLeaf()
	}
	switch g.r.Intn(7) {
	case 0:
		return g.boolLeaf()
	case 1, 2:
		op := g.pick("<", "<=", ">", ">=", "==", "!=")
		return fmt.Sprintf("(%s %s %s)", g.intExpr(idx, d-1), op, g.intExpr(idx, d-1))
	case 3:
		return fmt.Sprintf("(%s %s %s)", g.boolExpr(idx, d-1), g.pick("&&", "||"), g.boolExpr(idx, d-1))
	case 4:
		return fmt.Sprintf("(!%s)", g.boolExpr(idx, d-1))
	case 5:
		return fmt.Sprintf("(%s.to_string() == %s.to_string())", g.intExpr(idx, d-1), g.intExpr(idx, d-1))
	default:
		return fmt.Sprintf("(%s == %s)", g.boolExpr(idx, d-1), g.boolExpr(idx, d-1))
	}
}

func (g *Gen) boolLeaf() string {
	vs := g.varsOf("Bool", false)
	if len(vs) > 0 && g.r.Intn(2) == 0 {
		return vs[g.r.Intn(len(vs))].name
	}
	return g.pick("true", "false")
}

// ---- collections, structs, for loops and strings ----

func (g *Gen) pickVar(ty string) (genVar, bool) {
	vs := g.varsOf(ty, false)
	if len(vs) == 0 {
		return genVar{}, false
	}
	return vs[g.r.Intn(len(vs))], true
}

func (g *Gen) declare(ty, prefix string, mut bool) string {
	v := g.name(prefix)
	g.vars = append(g.vars, genVar{v, ty, mut})
	return v
}

// smallInt is an Int expression in [0, 8] used as a loop trip count
// (`lo..(lo + n)` is then empty or has at most 8 iterations, even when
// `lo + n` wraps around).
func (g *Gen) smallInt(idx int) string {
	if g.r.Intn(2) == 0 {
		return fmt.Sprint(g.r.Intn(9))
	}
	return fmt.Sprintf("(%s %% 9).abs()", g.intExpr(idx, 1))
}

func (g *Gen) collStmt(idx int, ind string, k int) {
	switch k {
	case 0: // new Vec, optionally prefilled
		v := g.name("v")
		fmt.Fprintf(&g.b, "%slet %s: Vec<Int> = Vec::new();\n", ind, v)
		for i := g.r.Intn(4); i > 0; i-- {
			fmt.Fprintf(&g.b, "%s%s.push(%s);\n", ind, v, g.intExpr(idx, 2))
		}
		g.vars = append(g.vars, genVar{v, "Vec", false})
	case 1: // Vec mutation
		v, ok := g.pickVar("Vec")
		if !ok {
			g.collStmt(idx, ind, 0)
			return
		}
		switch g.r.Intn(4) {
		case 0:
			if !g.noPush {
				fmt.Fprintf(&g.b, "%s%s.push(%s);\n", ind, v.name, g.intExpr(idx, 2))
				return
			}
			fallthrough
		case 1:
			i, x := g.vecIndex(idx), g.intExpr(idx, 2)
			p := g.declare("Bool", "p", false)
			fmt.Fprintf(&g.b, "%slet %s = %s.set(%s, %s);\n", ind, p, v.name, i, x)
		case 2:
			d := g.intLeaf()
			x := g.declare("Int", "x", false)
			fmt.Fprintf(&g.b, "%slet %s = %s.pop().unwrap_or(%s);\n", ind, x, v.name, d)
		default: // alias
			w := g.declare("Vec", "w", false)
			fmt.Fprintf(&g.b, "%slet %s = %s;\n", ind, w, v.name)
		}
	case 2: // new Map
		if g.r.Intn(2) == 0 {
			m := g.name("m")
			fmt.Fprintf(&g.b, "%slet %s: Map<Int, Int> = Map::new();\n", ind, m)
			g.vars = append(g.vars, genVar{m, "MapI", false})
			for i := g.r.Intn(4); i > 0; i-- {
				fmt.Fprintf(&g.b, "%s%s.insert(%s, %s);\n", ind, m, g.mapKey(idx, "MapI"), g.intExpr(idx, 2))
			}
		} else {
			m := g.name("n")
			fmt.Fprintf(&g.b, "%slet %s: Map<String, Int> = Map::new();\n", ind, m)
			g.vars = append(g.vars, genVar{m, "MapS", false})
			for i := g.r.Intn(4); i > 0; i-- {
				fmt.Fprintf(&g.b, "%s%s.insert(%s, %s);\n", ind, m, g.mapKey(idx, "MapS"), g.intExpr(idx, 2))
			}
		}
	case 3: // Map mutation
		ty := g.pick("MapI", "MapS")
		m, ok := g.pickVar(ty)
		if !ok {
			g.collStmt(idx, ind, 2)
			return
		}
		key := g.mapKey(idx, ty)
		switch g.r.Intn(4) {
		case 0, 1:
			fmt.Fprintf(&g.b, "%s%s.insert(%s, %s);\n", ind, m.name, key, g.intExpr(idx, 2))
		case 2:
			fmt.Fprintf(&g.b, "%s%s.remove(%s);\n", ind, m.name, key)
		default:
			w := g.declare(ty, "alias", false)
			fmt.Fprintf(&g.b, "%slet %s = %s;\n", ind, w, m.name)
		}
	case 4: // struct
		e := g.intExpr(idx, 2)
		a := g.name("s")
		fmt.Fprintf(&g.b, "%slet %s = Acc { total: %s, hits: 0, log: Vec::new() };\n", ind, a, e)
		g.vars = append(g.vars, genVar{a, "Acc", false})
	case 5: // struct mutation through an alias or a call
		a, ok := g.pickVar("Acc")
		if !ok {
			g.collStmt(idx, ind, 4)
			return
		}
		switch g.r.Intn(4) {
		case 0:
			fmt.Fprintf(&g.b, "%s%s.total = %s;\n", ind, a.name, g.intExpr(idx, 2))
		case 1:
			if !g.noPush {
				fmt.Fprintf(&g.b, "%sbump(%s, %s);\n", ind, a.name, g.intExpr(idx, 2))
				return
			}
			fmt.Fprintf(&g.b, "%s%s.hits = %s.hits * 2;\n", ind, a.name, a.name)
		case 2:
			w := g.declare("Acc", "t", false)
			fmt.Fprintf(&g.b, "%slet %s = %s;\n", ind, w, a.name)
		default:
			if !g.noPush {
				fmt.Fprintf(&g.b, "%s%s.log.push(%s);\n", ind, a.name, g.intExpr(idx, 1))
				return
			}
			fmt.Fprintf(&g.b, "%s%s.total = %s.total - %s.log.len();\n", ind, a.name, a.name, a.name)
		}
	case 6, 7: // for loops
		if g.loops >= 2 {
			g.stmt(idx, ind)
			return
		}
		g.forLoop(idx, ind)
	case 8: // string-valued binding folded into an Int
		e := g.strExpr(idx, 3)
		x := g.declare("Int", "h", false)
		fmt.Fprintf(&g.b, "%slet %s = hash(%s);\n", ind, x, e)
	case 9: // log-like Vec<String> join
		e := fmt.Sprintf("hash(%s.split(%s).join(%s))", g.strExpr(idx, 2), g.strLit(), g.strLit())
		x := g.declare("Int", "j", false)
		fmt.Fprintf(&g.b, "%slet %s = %s;\n", ind, x, e)
	default:
		g.stmt(idx, ind)
	}
}

func (g *Gen) vecIndex(idx int) string {
	return g.pick("0", "1", "2", "-1", "3", "100", fmt.Sprintf("(%s %% 4)", g.intExpr(idx, 1)))
}

func (g *Gen) mapKey(idx int, ty string) string {
	if ty == "MapI" {
		return g.pick("0", "1", "2", "-1", fmt.Sprintf("(%s %% 3)", g.intExpr(idx, 1)), g.intLeaf())
	}
	return g.pick(`"a"`, `"b"`, `""`, `"ab"`, fmt.Sprintf("(%s %% 3).to_string()", g.intExpr(idx, 1)))
}

func (g *Gen) forLoop(idx int, ind string) {
	acc := g.name("acc")
	fmt.Fprintf(&g.b, "%slet mut %s = %s;\n", ind, acc, g.intExpr(idx, 1))
	saved, savedNoPush := g.vars, g.noPush
	x := g.name("k")
	if v, ok := g.pickVar("Vec"); ok && g.r.Intn(2) == 0 {
		fmt.Fprintf(&g.b, "%sfor %s in %s {\n", ind, x, v.name)
		g.noPush = true
	} else if a, ok := g.pickVar("Acc"); ok && g.r.Intn(3) == 0 {
		fmt.Fprintf(&g.b, "%sfor %s in %s.log {\n", ind, x, a.name)
		g.noPush = true
	} else {
		lo := g.pick("0", "1", "-2", g.intLeaf())
		fmt.Fprintf(&g.b, "%sfor %s in %s..(%s + %s) {\n", ind, x, lo, lo, g.smallInt(idx))
	}
	g.vars = append(g.vars, genVar{x, "Int", false}, genVar{acc, "Int", true})
	g.loops++
	in := ind + "    "
	for n := 1 + g.r.Intn(3); n > 0; n-- {
		switch g.r.Intn(5) {
		case 0:
			fmt.Fprintf(&g.b, "%sif %s { continue; }\n", in, g.boolExpr(idx, 1))
		case 1:
			fmt.Fprintf(&g.b, "%sif %s { break; }\n", in, g.boolExpr(idx, 1))
		default:
			g.stmt(idx, in)
		}
	}
	fmt.Fprintf(&g.b, "%s%s = %s;\n", in, acc, g.intExpr(idx, 2))
	g.loops--
	fmt.Fprintf(&g.b, "%s}\n", ind)
	// collections declared in the body are dropped; outer ones may have been mutated
	g.vars = append(saved, genVar{acc, "Int", true})
	g.noPush = savedNoPush
}

func (g *Gen) collIntExpr(idx, d, k int) string {
	switch k {
	case 0, 1:
		if v, ok := g.pickVar("Vec"); ok {
			switch g.r.Intn(3) {
			case 0:
				return fmt.Sprintf("%s.len()", v.name)
			case 1:
				return fmt.Sprintf("%s.get(%s).unwrap_or(%s)", v.name, g.vecIndex(idx), g.intLeaf())
			default:
				return fmt.Sprintf("vsum(%s)", v.name)
			}
		}
	case 2:
		ty := g.pick("MapI", "MapS")
		if m, ok := g.pickVar(ty); ok {
			key := g.mapKey(idx, ty)
			switch g.r.Intn(4) {
			case 0:
				return fmt.Sprintf("%s.len()", m.name)
			case 1:
				return fmt.Sprintf("%s.get(%s).unwrap_or(%s)", m.name, key, g.intLeaf())
			case 2:
				return fmt.Sprintf("(if %s.contains(%s) { %s } else { %s })", m.name, key, g.intLeaf(), g.intLeaf())
			default:
				if ty == "MapI" {
					return fmt.Sprintf("mfold(%s)", m.name)
				}
				return fmt.Sprintf("sfold(%s)", m.name)
			}
		}
	case 3:
		if a, ok := g.pickVar("Acc"); ok {
			return g.pick(a.name+".total", a.name+".hits", a.name+".log.len()", "vsum("+a.name+".log)")
		}
	case 4:
		return fmt.Sprintf("hash(%s)", g.strExpr(idx, d-1))
	case 5:
		s := g.strExpr(idx, d-1)
		switch g.r.Intn(5) {
		case 0:
			return fmt.Sprintf("%s.len()", s)
		case 1:
			return fmt.Sprintf("%s.index_of(%s).unwrap_or(-1)", s, g.strExpr(idx, d-1))
		case 2:
			return fmt.Sprintf("%s.char_at(%s).unwrap_or(-1)", s, g.vecIndex(idx))
		case 3:
			return fmt.Sprintf("%s.split(%s).len()", s, g.strLit())
		default:
			return fmt.Sprintf("%s.parse_int().unwrap_or(%s)", s, g.intLeaf())
		}
	case 6:
		return fmt.Sprintf("%s.%s(%s)", g.intExpr(idx, d-1), g.pick("min", "max"), g.intExpr(idx, d-1))
	case 7:
		return fmt.Sprintf("%s.%s(%s)", g.intExpr(idx, d-1), g.pick("bit_and", "bit_or", "bit_xor", "shl", "shr", "ushr"), g.intExpr(idx, d-1))
	}
	return g.intLeaf()
}

var strLits = []string{`""`, `","`, `"a"`, `"ab"`, `"a,b,,c"`, `"  x y  "`, `"12"`, `"-7"`, `"hello"`, `"9223372036854775808"`, `"Hi, There"`}

func (g *Gen) strLit() string { return strLits[g.r.Intn(len(strLits))] }

func (g *Gen) strExpr(idx, d int) string {
	if d <= 0 {
		return g.strLit()
	}
	switch g.r.Intn(10) {
	case 0, 1:
		return g.strLit()
	case 2:
		return fmt.Sprintf("%s.to_string()", g.intExpr(idx, d-1))
	case 3:
		return fmt.Sprintf("(%s + %s)", g.strExpr(idx, d-1), g.strExpr(idx, d-1))
	case 4:
		return fmt.Sprintf("%s.slice(%s, %s)", g.strExpr(idx, d-1), g.vecIndex(idx), g.vecIndex(idx))
	case 5:
		return fmt.Sprintf("%s.replace(%s, %s)", g.strExpr(idx, d-1), g.strLit(), g.strLit())
	case 6:
		return fmt.Sprintf("%s.%s()", g.strExpr(idx, d-1), g.pick("trim", "to_upper", "to_lower"))
	case 7:
		return fmt.Sprintf("String::from_char(32 + (%s %% 90).abs())", g.intExpr(idx, d-1))
	case 8:
		return fmt.Sprintf("String::from_bytes(%s.to_bytes())", g.strExpr(idx, d-1))
	default:
		return fmt.Sprintf("%s.split(%s).join(%s)", g.strExpr(idx, d-1), g.strLit(), g.strLit())
	}
}
