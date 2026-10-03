// Package selfhost holds the canonical text dumps used to compare the Go
// compiler (stage0) with the self-hosted compiler in compiler/ (stage1).
// The stage1 printers (compiler/frontend.kek) must produce byte-identical
// output.
package selfhost

import (
	"fmt"
	"strings"

	"github.com/illumination-k/kekkai/internal/syntax"
)

// Quote is the canonical string quoting of the dumps.
func Quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == '"':
			b.WriteString(`\"`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\r':
			b.WriteString(`\r`)
		case c < 0x20 || c == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func pos(p syntax.Pos) string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

// Tokens dumps the tokens of src, one per line, followed by the lexer
// errors. ok is false when there were errors.
func Tokens(src string) (out string, ok bool) {
	toks, errs := syntax.Lex(src)
	var b strings.Builder
	for _, t := range toks {
		fmt.Fprintf(&b, "%s %s %s\n", pos(t.Pos), t.Kind, Quote(t.Text))
	}
	for _, e := range errs {
		fmt.Fprintf(&b, "error %s\n", e)
	}
	return b.String(), len(errs) == 0
}

// AST dumps the syntax tree of src, or the sorted parse errors.
func AST(src string) (out string, ok bool) {
	f, err := syntax.Parse(src)
	if err != nil {
		return err.Error() + "\n", false
	}
	d := &dumper{}
	for _, s := range f.Structs {
		d.line(0, "struct %s @%s", s.Name, pos(s.Pos))
		for _, fl := range s.Fields {
			d.line(1, "field %s @%s", fl.Name, pos(fl.Pos))
			d.typ(2, fl.Type)
		}
	}
	for _, e := range f.Enums {
		d.line(0, "enum %s @%s", e.Name, pos(e.Pos))
		for _, v := range e.Variants {
			d.line(1, "variant %s @%s", v.Name, pos(v.Pos))
			for _, t := range v.Fields {
				d.typ(2, t)
			}
		}
	}
	for _, fn := range f.Funcs {
		d.line(0, "fn %s @%s", fn.Name, pos(fn.Pos))
		for _, a := range fn.Attrs {
			d.line(1, "attr %s @%s", a.Name, pos(a.Pos))
		}
		for _, p := range fn.Params {
			d.param(1, p)
		}
		if fn.Result != nil {
			d.line(1, "result")
			d.typ(2, fn.Result)
		}
		d.block(1, fn.Body)
	}
	return d.b.String(), true
}

type dumper struct{ b strings.Builder }

func (d *dumper) line(depth int, format string, args ...any) {
	d.b.WriteString(strings.Repeat("  ", depth))
	fmt.Fprintf(&d.b, format, args...)
	d.b.WriteByte('\n')
}

func (d *dumper) typ(depth int, t syntax.TypeExpr) {
	switch t := t.(type) {
	case *syntax.NamedType:
		d.line(depth, "type %s @%s", t.Name, pos(t.Pos))
		for _, a := range t.Args {
			d.typ(depth+1, a)
		}
	case *syntax.RefType:
		d.line(depth, "ref @%s", pos(t.Pos))
		d.typ(depth+1, t.Elem)
	case *syntax.UnitType:
		d.line(depth, "unit-type @%s", pos(t.Pos))
	}
}

func (d *dumper) param(depth int, p *syntax.Param) {
	d.line(depth, "param %s @%s", p.Name, pos(p.Pos))
	if p.Type != nil {
		d.typ(depth+1, p.Type)
	}
}

func (d *dumper) block(depth int, b *syntax.Block) {
	d.line(depth, "block @%s end=%s", pos(b.Pos), pos(b.End))
	for _, s := range b.Stmts {
		d.stmt(depth+1, s)
	}
	if b.Tail != nil {
		d.line(depth+1, "tail")
		d.expr(depth+2, b.Tail)
	}
}

func (d *dumper) stmt(depth int, s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.LetStmt:
		d.line(depth, "let %s mut=%t @%s", s.Name, s.Mut, pos(s.Pos))
		if s.Type != nil {
			d.typ(depth+1, s.Type)
		}
		d.expr(depth+1, s.Init)
	case *syntax.AssignStmt:
		d.line(depth, "assign %s @%s", s.Name, pos(s.Pos))
		d.expr(depth+1, s.Value)
	case *syntax.FieldAssignStmt:
		d.line(depth, "field-assign @%s", pos(s.Pos))
		d.expr(depth+1, s.Target)
		d.expr(depth+1, s.Value)
	case *syntax.BreakStmt:
		d.line(depth, "break @%s", pos(s.Pos))
	case *syntax.ContinueStmt:
		d.line(depth, "continue @%s", pos(s.Pos))
	case *syntax.WhileStmt:
		d.line(depth, "while @%s", pos(s.Pos))
		d.expr(depth+1, s.Cond)
		d.block(depth+1, s.Body)
	case *syntax.ForStmt:
		d.line(depth, "for %s @%s", s.Var, pos(s.Pos))
		d.expr(depth+1, s.Iter)
		if s.End != nil {
			d.line(depth+1, "to")
			d.expr(depth+2, s.End)
		}
		d.block(depth+1, s.Body)
	case *syntax.ReturnStmt:
		d.line(depth, "return @%s", pos(s.Pos))
		if s.Value != nil {
			d.expr(depth+1, s.Value)
		}
	case *syntax.ExprStmt:
		d.line(depth, "expr-stmt semi=%t @%s", s.Semi, pos(s.Pos))
		d.expr(depth+1, s.X)
	}
}

func op(k syntax.TokenKind) string { return strings.Trim(k.String(), "`") }

func (d *dumper) expr(depth int, e syntax.Expr) {
	switch e := e.(type) {
	case *syntax.IntLit:
		d.line(depth, "int %d @%s", e.Value, pos(e.Pos))
	case *syntax.BoolLit:
		d.line(depth, "bool %t @%s", e.Value, pos(e.Pos))
	case *syntax.StringLit:
		d.line(depth, "string %s @%s", Quote(e.Value), pos(e.Pos))
	case *syntax.UnitLit:
		d.line(depth, "unit @%s", pos(e.Pos))
	case *syntax.Ident:
		d.line(depth, "ident %s @%s", e.Name, pos(e.Pos))
	case *syntax.BinaryExpr:
		d.line(depth, "binary %s @%s", op(e.Op), pos(e.Pos))
		d.expr(depth+1, e.X)
		d.expr(depth+1, e.Y)
	case *syntax.UnaryExpr:
		d.line(depth, "unary %s @%s", op(e.Op), pos(e.Pos))
		d.expr(depth+1, e.X)
	case *syntax.CallExpr:
		d.line(depth, "call @%s", pos(e.Pos))
		d.expr(depth+1, e.Func)
		for _, a := range e.Args {
			d.expr(depth+1, a)
		}
	case *syntax.MethodCall:
		d.line(depth, "method %s @%s", e.Name, pos(e.Pos))
		d.expr(depth+1, e.Recv)
		for _, a := range e.Args {
			d.expr(depth+1, a)
		}
	case *syntax.FieldExpr:
		d.line(depth, "field %s @%s", e.Name, pos(e.Pos))
		d.expr(depth+1, e.X)
	case *syntax.PathExpr:
		d.line(depth, "path %s::%s @%s", e.Type, e.Name, pos(e.Pos))
	case *syntax.StructLit:
		d.line(depth, "struct-lit %s @%s", e.Name, pos(e.Pos))
		for _, f := range e.Fields {
			d.line(depth+1, "init %s @%s", f.Name, pos(f.Pos))
			d.expr(depth+2, f.Value)
		}
	case *syntax.IfExpr:
		d.line(depth, "if @%s", pos(e.Pos))
		d.expr(depth+1, e.Cond)
		d.block(depth+1, e.Then)
		if e.Else != nil {
			d.line(depth+1, "else")
			d.expr(depth+2, e.Else)
		}
	case *syntax.MatchExpr:
		d.line(depth, "match @%s", pos(e.Pos))
		d.expr(depth+1, e.X)
		for _, a := range e.Arms {
			d.line(depth+1, "arm @%s", pos(a.Pos))
			d.pattern(depth+2, a.Pat)
			d.expr(depth+2, a.Body)
		}
	case *syntax.Block:
		d.block(depth, e)
	case *syntax.TryExpr:
		d.line(depth, "try @%s", pos(e.Pos))
		d.expr(depth+1, e.X)
	case *syntax.Closure:
		d.line(depth, "closure @%s", pos(e.Pos))
		for _, p := range e.Params {
			d.param(depth+1, p)
		}
		d.expr(depth+1, e.Body)
	}
}

func (d *dumper) pattern(depth int, p *syntax.Pattern) {
	switch {
	case p.Wildcard:
		d.line(depth, "pat _ @%s", pos(p.Pos))
	case p.IntValue != nil:
		d.line(depth, "pat int %d @%s", *p.IntValue, pos(p.Pos))
	case p.BoolLit != nil:
		d.line(depth, "pat bool %t @%s", *p.BoolLit, pos(p.Pos))
	case p.StrValue != nil:
		d.line(depth, "pat string %s @%s", Quote(*p.StrValue), pos(p.Pos))
	default:
		d.line(depth, "pat type=%s ctor=%s bind=%s @%s", p.Type, p.Ctor, p.Bind, pos(p.Pos))
		for _, a := range p.Args {
			d.pattern(depth+1, a)
		}
	}
}
