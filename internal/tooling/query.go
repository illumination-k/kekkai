package tooling

import (
	"fmt"
	"sort"
	"strings"

	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/types"
)

// refAt returns the name occurrence under p.
func (a *Analysis) refAt(p syntax.Pos) *ref {
	ti := a.tokenIndexAt(p)
	if ti < 0 {
		return nil
	}
	tp := a.Toks[ti].Pos
	var found *ref
	for _, r := range a.refs {
		if r.pos == tp {
			// Prefer the most specific occurrence (e.g. the method name
			// over an enclosing expression at the same position).
			if found == nil || (found.binding == nil && r.binding != nil) {
				found = r
			}
		}
	}
	return found
}

// Hover returns Markdown describing the name under p and its range.
func (a *Analysis) Hover(p syntax.Pos) (text string, start, end syntax.Pos, ok bool) {
	r := a.refAt(p)
	if r == nil {
		return "", p, p, false
	}
	text = a.hoverText(r)
	if text == "" {
		return "", p, p, false
	}
	return text, r.pos, r.end(), true
}

func (a *Analysis) hoverText(r *ref) string {
	in := a.Info
	switch r.kind {
	case refBinding:
		b := r.binding
		if b == nil {
			if r.expr != nil {
				if t := in.Types[r.expr]; t != nil {
					return code(r.name + ": " + typeString(t))
				}
			}
			return ""
		}
		kind := "let"
		switch {
		case b.Param && types.IsCap(b.Type):
			kind = "capability"
		case b.Param:
			kind = "param"
		case types.IsCap(b.Type):
			kind = "capability"
		case b.Mut:
			kind = "let mut"
		}
		s := code(fmt.Sprintf("%s %s: %s", kind, b.Name, typeString(b.Type)))
		if c, ok := types.Prune(b.Type).(*types.Cap); ok {
			s += "\n\n" + capDoc(c)
		}
		return s
	case refFunc:
		if r.fn != nil {
			return a.funcHover(r.fn)
		}
	case refStruct:
		if r.st != nil {
			return code(structText(r.st))
		}
	case refEnum:
		if r.en != nil {
			return code(enumText(r.en))
		}
	case refField:
		if r.st != nil && r.idx >= 0 && r.idx < len(r.st.Fields) {
			f := r.st.Fields[r.idx]
			return code(fmt.Sprintf("field %s.%s: %s", r.st.Name, f.Name, f.Type))
		}
	case refVariant:
		if r.en != nil && r.idx >= 0 && r.idx < len(r.en.Variants) {
			return code(variantText(r.en, r.en.Variants[r.idx])) + "\n\nvariant of `" + r.en.Name + "`"
		}
	case refMethod:
		mi := r.method
		if mi == nil {
			return ""
		}
		switch mi.Kind {
		case types.MethodBuiltin:
			return code(BuiltinSignature(mi.Builtin)) + "\n\n" + builtinNote(mi.Builtin)
		case types.MethodTransaction:
			res := "Result<T, E>"
			if mi.Closure != nil {
				res = typeString(mi.Closure.Result)
			}
			return code(fmt.Sprintf("fn Db.transaction(&self, body: |tx: Tx| -> %s) -> Result<..., TxError>", res)) +
				"\n\neffect: runs `body` as an atomic transaction. `tx` is linear: it must be committed or rolled back exactly once. Irrevocable capabilities (`Net`, `Db`) are not visible inside; use `tx.outbox(...)`." +
				"\n\nasync"
		case types.MethodGeneric:
			return code(genericSig(mi.Op, typeString(mi.RecvTy), typeString(in.Types[r.expr]))) + "\n\npure"
		}
	case refStatic:
		if r.builtin != nil {
			return code(BuiltinSignature(r.builtin)) + "\n\n" + builtinNote(r.builtin)
		}
	case refCtor:
		if r.expr != nil {
			if t := in.Types[r.expr]; t != nil {
				return code(r.name + " : " + typeString(t))
			}
		}
		return code(ctorDoc(r.name))
	case refTypeName:
		if IsCapName(r.name) {
			c := &types.Cap{Kind: capKind(r.name)}
			return code("capability "+r.name) + "\n\n" + capDoc(c)
		}
		if ms := MethodsOf(r.name); len(ms) > 0 {
			var names []string
			for _, m := range ms {
				names = append(names, m.Name)
			}
			return code("type "+r.name) + "\n\nmethods: `" + strings.Join(names, "`, `") + "`"
		}
		return code("type " + r.name)
	}
	return ""
}

func ctorDoc(name string) string {
	switch name {
	case "Ok":
		return "Ok : T -> Result<T, E>"
	case "Err":
		return "Err : E -> Result<T, E>"
	case "Some":
		return "Some : T -> Option<T>"
	case "None":
		return "None : Option<T>"
	}
	return name
}

func genericSig(op, recv, result string) string {
	switch op {
	case "unwrap_or":
		return fmt.Sprintf("fn %s.unwrap_or(self, default: %s) -> %s", recv, result, result)
	default:
		return fmt.Sprintf("fn %s.%s(self) -> Bool", recv, op)
	}
}

func capKind(name string) types.CapKind {
	for k := types.CapLog; k <= types.CapTx; k++ {
		if k.String() == name {
			return k
		}
	}
	return types.CapLog
}

func capDoc(c *types.Cap) string {
	name := c.Kind.String()
	var ms []string
	for _, b := range MethodsOf(name) {
		ms = append(ms, b.Name)
	}
	if c.Kind == types.CapDb {
		ms = append(ms, "transaction")
	}
	sort.Strings(ms)
	rev := "revocable: usable inside a transaction"
	if !c.Kind.Info().Revocable {
		rev = "irrevocable: not usable inside a transaction"
	}
	return fmt.Sprintf("Capability `%s` (%s). Operations: `%s`.", name, rev, strings.Join(ms, "`, `"))
}

// MethodsOf returns the builtin (non-static) methods of a receiver type
// name, sorted by name.
func MethodsOf(recv string) []*types.Builtin {
	var out []*types.Builtin
	for _, b := range types.AllBuiltins() {
		if b.Recv == recv && !IsStatic(b) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// StaticsOf returns the builtin static functions `Owner::name`.
func StaticsOf(owner string) []*types.Builtin {
	var out []*types.Builtin
	for _, b := range types.AllBuiltins() {
		if b.Recv == owner && IsStatic(b) {
			out = append(out, b)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Definition returns the declaration position of the name under p.
func (a *Analysis) Definition(p syntax.Pos) (start, end syntax.Pos, ok bool) {
	r := a.refAt(p)
	if r == nil || a.ix == nil {
		return p, p, false
	}
	ix := a.ix
	var dp syntax.Pos
	name := r.name
	switch r.kind {
	case refBinding:
		if r.binding == nil {
			return p, p, false
		}
		dp, ok = ix.declPos[r.binding]
		name = r.binding.Name
	case refFunc:
		if r.fn != nil {
			dp, ok = ix.funcPos[r.fn]
		}
	case refStruct:
		if r.st != nil {
			dp, ok = ix.structPos[r.st]
		}
	case refEnum:
		if r.en != nil {
			dp, ok = ix.enumPos[r.en]
		}
	case refField:
		if r.st != nil && r.idx >= 0 && r.idx < len(ix.fieldPos[r.st]) {
			dp, ok = ix.fieldPos[r.st][r.idx], true
		}
	case refVariant:
		if r.en != nil && r.idx >= 0 && r.idx < len(ix.variantPos[r.en]) {
			dp, ok = ix.variantPos[r.en][r.idx], true
		}
	}
	if !ok {
		return p, p, false
	}
	return dp, syntax.Pos{Line: dp.Line, Col: dp.Col + len(name)}, true
}

// Symbol is a document outline entry.
type Symbol struct {
	Name     string
	Detail   string
	Kind     string // "function", "struct", "enum", "field", "variant"
	Start    syntax.Pos
	End      syntax.Pos
	SelStart syntax.Pos
	SelEnd   syntax.Pos
	Children []Symbol
}

// Symbols returns the outline of the file in source order.
func (a *Analysis) Symbols() []Symbol {
	var out []Symbol
	f := a.File
	sel := func(p syntax.Pos, name string) (syntax.Pos, syntax.Pos) {
		return p, syntax.Pos{Line: p.Line, Col: p.Col + len(name)}
	}
	for _, sd := range f.Structs {
		np := a.nameAfter(sd.Pos, sd.Name)
		s := Symbol{Name: sd.Name, Kind: "struct", Start: sd.Pos, End: a.braceEnd(np)}
		s.SelStart, s.SelEnd = sel(np, sd.Name)
		for _, fl := range sd.Fields {
			c := Symbol{Name: fl.Name, Kind: "field", Detail: typeExprString(fl.Type), Start: fl.Pos}
			c.SelStart, c.SelEnd = sel(fl.Pos, fl.Name)
			c.End = c.SelEnd
			s.Children = append(s.Children, c)
		}
		out = append(out, s)
	}
	for _, ed := range f.Enums {
		np := a.nameAfter(ed.Pos, ed.Name)
		s := Symbol{Name: ed.Name, Kind: "enum", Start: ed.Pos, End: a.braceEnd(np)}
		s.SelStart, s.SelEnd = sel(np, ed.Name)
		for _, v := range ed.Variants {
			var fs []string
			for _, t := range v.Fields {
				fs = append(fs, typeExprString(t))
			}
			c := Symbol{Name: v.Name, Kind: "variant", Start: v.Pos}
			if len(fs) > 0 {
				c.Detail = "(" + strings.Join(fs, ", ") + ")"
			}
			c.SelStart, c.SelEnd = sel(v.Pos, v.Name)
			c.End = c.SelEnd
			s.Children = append(s.Children, c)
		}
		out = append(out, s)
	}
	for _, fd := range f.Funcs {
		np := a.nameAfter(fd.Pos, fd.Name)
		start := fd.Pos
		if len(fd.Attrs) > 0 {
			start = fd.Attrs[0].Pos
		}
		s := Symbol{Name: fd.Name, Kind: "function", Start: start, End: fd.Pos}
		if fd.Body != nil {
			s.End = syntax.Pos{Line: fd.Body.End.Line, Col: fd.Body.End.Col + 1}
		}
		s.SelStart, s.SelEnd = sel(np, fd.Name)
		if a.Info != nil {
			if fn := a.Info.Funcs[fd.Name]; fn != nil && fn.Decl == fd {
				s.Detail = strings.TrimPrefix(FuncSignature(fn), "fn "+fn.Name)
				if fn.Pure() {
					s.Detail += "  [pure]"
				}
			}
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return Less(out[i].Start, out[j].Start) })
	return out
}

// braceEnd returns the position just after the `}` matching the first `{`
// at or after p.
func (a *Analysis) braceEnd(p syntax.Pos) syntax.Pos {
	i, ok := a.tokAt[p]
	if !ok {
		return p
	}
	depth := 0
	for ; i < len(a.Toks); i++ {
		switch a.Toks[i].Kind {
		case syntax.LBrace:
			depth++
		case syntax.RBrace:
			depth--
			if depth == 0 {
				t := a.Toks[i].Pos
				return syntax.Pos{Line: t.Line, Col: t.Col + 1}
			}
		}
	}
	return p
}

func typeExprString(t syntax.TypeExpr) string {
	switch t := t.(type) {
	case *syntax.UnitType:
		return "()"
	case *syntax.RefType:
		return "&" + typeExprString(t.Elem)
	case *syntax.NamedType:
		if len(t.Args) == 0 {
			return t.Name
		}
		var as []string
		for _, x := range t.Args {
			as = append(as, typeExprString(x))
		}
		return t.Name + "<" + strings.Join(as, ", ") + ">"
	}
	return "?"
}

// funcAt returns the function declaration enclosing p.
func (a *Analysis) funcAt(p syntax.Pos) *syntax.FuncDecl {
	for _, fd := range a.File.Funcs {
		if fd.Body == nil {
			continue
		}
		if !Less(p, fd.Pos) && !Less(fd.Body.End, p) {
			return fd
		}
	}
	return nil
}
