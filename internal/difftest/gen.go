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
// loops and string host operations. Function i may only call functions
// j < i, and loops are bounded, so every program terminates.
type Gen struct {
	r      *rand.Rand
	b      strings.Builder
	nfuncs int
	vars   []genVar
	depth  int
	fresh  int
}

type genVar struct {
	name string
	ty   string // "Int" or "Bool"
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
	n := 1 + g.r.Intn(4)
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
	switch g.r.Intn(5) {
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
