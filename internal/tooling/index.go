package tooling

import (
	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/types"
)

type refKind int

const (
	refBinding  refKind = iota // local variable, parameter, capability, pattern binding
	refFunc                    // user function
	refStruct                  // user struct type
	refEnum                    // user enum type
	refField                   // struct field
	refVariant                 // enum variant
	refMethod                  // builtin or generic method
	refStatic                  // builtin static function Owner::name
	refCtor                    // Ok / Err / Some / None
	refTypeName                // builtin type name (Int, Request, Log, ...)
)

// ref is one occurrence of a name in the source: a declaration or a use.
type ref struct {
	kind    refKind
	pos     syntax.Pos
	name    string
	decl    bool
	binding *types.Binding
	fn      *types.Func
	st      *types.Struct
	en      *types.Enum
	idx     int // field or variant index
	builtin *types.Builtin
	method  *types.MethodInfo
	expr    syntax.Expr // expression whose type to show, if any
	fnScope *syntax.FuncDecl
}

func (r *ref) end() syntax.Pos { return syntax.Pos{Line: r.pos.Line, Col: r.pos.Col + len(r.name)} }

// indexer walks the AST and records every named occurrence.
type indexer struct {
	a  *Analysis
	in *types.Info
	fd *syntax.FuncDecl
	// declPos maps bindings to the position of their name.
	declPos map[*types.Binding]syntax.Pos
	// bindScope maps bindings to the function declaring them.
	bindScope map[*types.Binding]*syntax.FuncDecl
	// declFunc etc. map type-checker objects to declaration name positions.
	funcPos    map[*types.Func]syntax.Pos
	structPos  map[*types.Struct]syntax.Pos
	enumPos    map[*types.Enum]syntax.Pos
	fieldPos   map[*types.Struct][]syntax.Pos
	variantPos map[*types.Enum][]syntax.Pos
}

func (a *Analysis) index() {
	ix := &indexer{
		a: a, in: a.Info,
		declPos:    map[*types.Binding]syntax.Pos{},
		bindScope:  map[*types.Binding]*syntax.FuncDecl{},
		funcPos:    map[*types.Func]syntax.Pos{},
		structPos:  map[*types.Struct]syntax.Pos{},
		enumPos:    map[*types.Enum]syntax.Pos{},
		fieldPos:   map[*types.Struct][]syntax.Pos{},
		variantPos: map[*types.Enum][]syntax.Pos{},
	}
	a.ix = ix
	f := a.File
	for _, sd := range f.Structs {
		st := ix.in.Structs[sd.Name]
		np := a.nameAfter(sd.Pos, sd.Name)
		if st != nil {
			if _, dup := ix.structPos[st]; !dup {
				ix.structPos[st] = np
				for _, fl := range sd.Fields {
					ix.fieldPos[st] = append(ix.fieldPos[st], fl.Pos)
				}
			}
		}
		ix.add(&ref{kind: refStruct, pos: np, name: sd.Name, decl: true, st: st})
		for i, fl := range sd.Fields {
			ix.add(&ref{kind: refField, pos: fl.Pos, name: fl.Name, decl: true, st: st, idx: i})
			ix.typeExpr(fl.Type)
		}
	}
	for _, ed := range f.Enums {
		en := ix.in.Enums[ed.Name]
		np := a.nameAfter(ed.Pos, ed.Name)
		if en != nil {
			if _, dup := ix.enumPos[en]; !dup {
				ix.enumPos[en] = np
				for _, v := range ed.Variants {
					ix.variantPos[en] = append(ix.variantPos[en], v.Pos)
				}
			}
		}
		ix.add(&ref{kind: refEnum, pos: np, name: ed.Name, decl: true, en: en})
		for i, v := range ed.Variants {
			ix.add(&ref{kind: refVariant, pos: v.Pos, name: v.Name, decl: true, en: en, idx: i})
			for _, t := range v.Fields {
				ix.typeExpr(t)
			}
		}
	}
	for _, fd := range f.Funcs {
		fn := ix.in.Funcs[fd.Name]
		if fn != nil && fn.Decl != fd {
			fn = nil
		}
		np := a.nameAfter(fd.Pos, fd.Name)
		if fn != nil {
			ix.funcPos[fn] = np
		}
		ix.fd = fd
		ix.add(&ref{kind: refFunc, pos: np, name: fd.Name, decl: true, fn: fn})
		for i, p := range fd.Params {
			var b *types.Binding
			if fn != nil && i < len(fn.Params) {
				b = fn.Params[i]
			}
			ix.bindDecl(b, p.Pos, p.Name, nil)
			ix.typeExpr(p.Type)
		}
		if fd.Result != nil {
			ix.typeExpr(fd.Result)
		}
		if fd.Body != nil {
			ix.expr(fd.Body)
		}
		ix.fd = nil
	}
}

func (ix *indexer) add(r *ref) {
	if r.fnScope == nil {
		r.fnScope = ix.fd
	}
	ix.a.refs = append(ix.a.refs, r)
}

func (ix *indexer) bindDecl(b *types.Binding, pos syntax.Pos, name string, e syntax.Expr) {
	if b != nil {
		ix.declPos[b] = pos
		ix.bindScope[b] = ix.fd
	}
	if name == "_" {
		return
	}
	ix.add(&ref{kind: refBinding, pos: pos, name: name, decl: true, binding: b, expr: e})
}

func (ix *indexer) typeExpr(t syntax.TypeExpr) {
	switch t := t.(type) {
	case *syntax.RefType:
		ix.typeExpr(t.Elem)
	case *syntax.NamedType:
		r := &ref{kind: refTypeName, pos: t.Pos, name: t.Name}
		if st := ix.in.Structs[t.Name]; st != nil {
			r.kind, r.st = refStruct, st
		} else if en := ix.in.Enums[t.Name]; en != nil {
			r.kind, r.en = refEnum, en
		}
		ix.add(r)
		for _, a := range t.Args {
			ix.typeExpr(a)
		}
	}
}

func (ix *indexer) block(b *syntax.Block) {
	for _, s := range b.Stmts {
		ix.stmt(s)
	}
	if b.Tail != nil {
		ix.expr(b.Tail)
	}
}

func (ix *indexer) stmt(s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.LetStmt:
		ix.expr(s.Init)
		if s.Type != nil {
			ix.typeExpr(s.Type)
		}
		ix.bindDecl(ix.in.Lets[s], ix.a.nameAfter(s.Pos, s.Name), s.Name, nil)
	case *syntax.AssignStmt:
		ix.add(&ref{kind: refBinding, pos: s.Pos, name: s.Name, binding: ix.in.Assigns[s]})
		ix.expr(s.Value)
	case *syntax.WhileStmt:
		ix.expr(s.Cond)
		ix.block(s.Body)
	case *syntax.ReturnStmt:
		if s.Value != nil {
			ix.expr(s.Value)
		}
	case *syntax.ExprStmt:
		ix.expr(s.X)
	}
}

func (ix *indexer) expr(e syntax.Expr) {
	switch e := e.(type) {
	case nil:
	case *syntax.Ident:
		r := &ref{kind: refBinding, pos: e.Pos, name: e.Name, expr: e, binding: ix.in.Uses[e]}
		if ci := ix.in.Calls[e]; ci != nil && ci.Kind == types.CallNone {
			r.kind = refCtor
		}
		ix.add(r)
	case *syntax.CallExpr:
		ci := ix.in.Calls[e]
		switch f := e.Func.(type) {
		case *syntax.Ident:
			r := &ref{kind: refFunc, pos: f.Pos, name: f.Name, expr: e}
			if ci != nil {
				switch ci.Kind {
				case types.CallFunc:
					r.fn = ci.Func
				case types.CallOk, types.CallErr, types.CallSome:
					r.kind = refCtor
				}
			} else if fn := ix.in.Funcs[f.Name]; fn != nil {
				r.fn = fn
			}
			ix.add(r)
		case *syntax.PathExpr:
			ix.path(f, ci, e)
		default:
			ix.expr(e.Func)
		}
		for _, a := range e.Args {
			ix.expr(a)
		}
	case *syntax.PathExpr:
		ix.path(e, ix.in.Calls[e], e)
	case *syntax.MethodCall:
		ix.expr(e.Recv)
		ix.add(&ref{kind: refMethod, pos: e.Pos, name: e.Name, method: ix.in.Methods[e], expr: e})
		if mi := ix.in.Methods[e]; mi != nil && mi.Kind == types.MethodTransaction && len(e.Args) == 1 {
			if cl, ok := e.Args[0].(*syntax.Closure); ok {
				ix.closure(cl)
				return
			}
		}
		for _, a := range e.Args {
			ix.expr(a)
		}
	case *syntax.FieldExpr:
		ix.expr(e.X)
		r := &ref{kind: refField, pos: e.Pos, name: e.Name, expr: e, idx: -1}
		if st, ok := types.Prune(ix.in.Types[e.X]).(*types.Struct); ok {
			r.st = st
			r.idx, _ = st.Field(e.Name)
		}
		ix.add(r)
	case *syntax.StructLit:
		st := ix.in.StructLits[e]
		if st == nil {
			st = ix.in.Structs[e.Name]
		}
		ix.add(&ref{kind: refStruct, pos: e.Pos, name: e.Name, st: st, expr: e})
		for _, fi := range e.Fields {
			r := &ref{kind: refField, pos: fi.Pos, name: fi.Name, st: st, idx: -1}
			if st != nil {
				r.idx, _ = st.Field(fi.Name)
			}
			// Shorthand `P { x }`: the value is an identifier at the same
			// position; index the binding instead of the field.
			if id, ok := fi.Value.(*syntax.Ident); ok && id.Pos == fi.Pos {
				ix.expr(id)
				continue
			}
			ix.add(r)
			ix.expr(fi.Value)
		}
	case *syntax.IfExpr:
		ix.expr(e.Cond)
		ix.block(e.Then)
		if e.Else != nil {
			ix.expr(e.Else)
		}
	case *syntax.MatchExpr:
		ix.expr(e.X)
		for _, arm := range e.Arms {
			ix.pattern(arm.Pat)
			ix.expr(arm.Body)
		}
	case *syntax.Block:
		ix.block(e)
	case *syntax.BinaryExpr:
		ix.expr(e.X)
		ix.expr(e.Y)
	case *syntax.UnaryExpr:
		ix.expr(e.X)
	case *syntax.TryExpr:
		ix.expr(e.X)
	case *syntax.Closure:
		ix.closure(e)
	}
}

func (ix *indexer) closure(cl *syntax.Closure) {
	ci := ix.in.Closures[cl]
	for i, p := range cl.Params {
		var b *types.Binding
		if ci != nil && i == 0 {
			b = ci.Tx
		}
		ix.bindDecl(b, p.Pos, p.Name, nil)
		if p.Type != nil {
			ix.typeExpr(p.Type)
		}
	}
	ix.expr(cl.Body)
}

// path indexes `Type::Name` (key is the expression holding the CallInfo).
func (ix *indexer) path(p *syntax.PathExpr, ci *types.CallInfo, key syntax.Expr) {
	owner := &ref{kind: refTypeName, pos: p.Pos, name: p.Type}
	if en := ix.in.Enums[p.Type]; en != nil {
		owner.kind, owner.en = refEnum, en
	}
	ix.add(owner)
	np, ok := ix.a.tokenAfter(p.Pos, 2)
	if !ok {
		return
	}
	r := &ref{kind: refStatic, pos: np, name: p.Name, expr: key, idx: -1}
	if ci != nil {
		switch ci.Kind {
		case types.CallVariant:
			r.kind, r.en, r.idx = refVariant, ci.Enum, ci.Tag
		case types.CallStatic:
			r.builtin = ci.Builtin
		}
	} else if owner.en != nil {
		r.kind, r.en = refVariant, owner.en
		r.idx, _ = owner.en.Variant(p.Name)
	} else {
		r.builtin = types.LookupStatic(p.Type, p.Name)
	}
	ix.add(r)
}

func (ix *indexer) pattern(p *syntax.Pattern) {
	if p == nil {
		return
	}
	pi := ix.in.Patterns[p]
	if pi != nil && pi.Kind == types.PatBind {
		ix.bindDecl(pi.Bind, p.Pos, p.Bind, nil)
		return
	}
	ctorPos := p.Pos
	if p.Type != "" {
		owner := &ref{kind: refTypeName, pos: p.Pos, name: p.Type}
		if en := ix.in.Enums[p.Type]; en != nil {
			owner.kind, owner.en = refEnum, en
		}
		ix.add(owner)
		ctorPos, _ = ix.a.tokenAfter(p.Pos, 2)
	}
	name := p.Ctor
	if name == "" {
		name = p.Bind
	}
	if name != "" {
		r := &ref{kind: refCtor, pos: ctorPos, name: name, idx: -1}
		if pi != nil && pi.Kind == types.PatCtor {
			if en, ok := types.Prune(pi.Type).(*types.Enum); ok {
				r.kind, r.en, r.idx = refVariant, en, pi.Tag
			}
		}
		ix.add(r)
	}
	for _, sub := range p.Args {
		ix.pattern(sub)
	}
}
