package types

import (
	"fmt"
	"sort"

	"github.com/illumination-k/kekkai/internal/syntax"
)

// Never is the type of diverging expressions (a block ending in `return`).
var Never = &Prim{"!"}

// Invalid is the type of erroneous expressions; it unifies with everything
// so that one mistake is reported only once.
var Invalid = &Prim{"<invalid>"}

// Binding is a local variable, parameter or capability.
type Binding struct {
	ID   int
	Name string
	Type Type
	Mut  bool
	Pos  syntax.Pos
	// Param is true for function parameters (including capabilities).
	Param bool
	// Uses counts references (used to report unused capabilities).
	Uses int
}

// Func is a checked top-level function.
type Func struct {
	Name    string
	Decl    *syntax.FuncDecl
	Params  []*Binding
	Result  Type
	Handler bool
	// Async is true when the function can reach an asynchronous builtin
	// (directly or through calls) and is therefore compiled as a resumable
	// state machine.
	Async bool
	// Calls lists the user functions called from the body.
	Calls map[string]bool
	// Effects lists the capability operations used directly in the body.
	Effects map[string]bool
}

// Caps returns the capability parameters of f: the complete set of
// effects f (and anything it calls) can perform.
func (f *Func) Caps() []*Binding {
	var out []*Binding
	for _, p := range f.Params {
		if IsCap(p.Type) {
			out = append(out, p)
		}
	}
	return out
}

// Pure reports whether f receives no capabilities.
func (f *Func) Pure() bool { return len(f.Caps()) == 0 }

// CallKind classifies call-like expressions.
type CallKind int

const (
	CallFunc    CallKind = iota // user function
	CallOk                      // Ok(x)
	CallErr                     // Err(x)
	CallSome                    // Some(x)
	CallNone                    // None
	CallVariant                 // Enum::Variant(args) or Enum::Variant
	CallStatic                  // builtin static, e.g. Response::text
)

type CallInfo struct {
	Kind    CallKind
	Func    *Func
	Enum    *Enum
	Tag     int
	Builtin *Builtin
	Type    Type // result type
}

// MethodKind classifies method calls.
type MethodKind int

const (
	MethodBuiltin     MethodKind = iota
	MethodTransaction            // db.transaction(|tx| ...)
	MethodGeneric                // compiler-implemented Result/Option methods
)

type MethodInfo struct {
	Kind    MethodKind
	Builtin *Builtin
	Op      string // for MethodGeneric: "is_ok", "is_err", "is_some", "is_none", "unwrap_or"
	RecvTy  Type
	Closure *ClosureInfo
}

type ClosureInfo struct {
	Tx     *Binding
	Result Type // Result<T, E> returned by the body
}

// TryInfo records the early-exit target of `?`.
type TryInfo struct {
	Closure *ClosureInfo // nil: return from the enclosing function
	Target  Type         // the return type of the target
}

// ReturnInfo records the target of a `return` statement.
type ReturnInfo struct {
	Closure *ClosureInfo
}

// PatKind classifies match patterns.
type PatKind int

const (
	PatWild PatKind = iota
	PatBind
	PatInt
	PatBool
	PatString
	PatCtor // Ok/Err/Some/None/Enum variant
)

type PatInfo struct {
	Kind PatKind
	Type Type       // type of the matched value
	Tag  int        // constructor tag (Result: Ok=0, Err=1; Option: None=0, Some=1)
	Bind *Binding   // for PatBind
	Args []*syntax.Pattern // for PatCtor: field sub-patterns
}

// Info holds the results of type checking.
type Info struct {
	Funcs    map[string]*Func
	FuncList []*Func
	Structs  map[string]*Struct
	Enums    map[string]*Enum
	Handler  *Func

	Types      map[syntax.Expr]Type
	Uses       map[*syntax.Ident]*Binding
	Lets       map[*syntax.LetStmt]*Binding
	Assigns    map[*syntax.AssignStmt]*Binding
	Calls      map[syntax.Expr]*CallInfo // *CallExpr, *PathExpr, *Ident (None)
	Methods    map[*syntax.MethodCall]*MethodInfo
	Fields     map[*syntax.FieldExpr]int
	StructLits map[*syntax.StructLit]*Struct
	Tries      map[*syntax.TryExpr]*TryInfo
	Returns    map[*syntax.ReturnStmt]*ReturnInfo
	Patterns   map[*syntax.Pattern]*PatInfo
	Closures   map[*syntax.Closure]*ClosureInfo
}

// TypeOf returns the resolved type of an expression.
func (in *Info) TypeOf(e syntax.Expr) Type { return Resolve(in.Types[e]) }

type scope struct {
	parent *scope
	vars   map[string]*Binding
	// txBoundary marks the body of a transaction block: irrevocable
	// capabilities from outside are not visible past it.
	txBoundary bool
}

type retCtx struct {
	typ     Type
	closure *ClosureInfo
}

type checker struct {
	info   *Info
	errs   syntax.ErrorList
	nextID int
	scope  *scope
	rets   []retCtx
	fn     *Func
}

// Check type-checks a parsed file.
func Check(f *syntax.File) (*Info, error) {
	c := &checker{info: &Info{
		Funcs:      map[string]*Func{},
		Structs:    map[string]*Struct{},
		Enums:      map[string]*Enum{},
		Types:      map[syntax.Expr]Type{},
		Uses:       map[*syntax.Ident]*Binding{},
		Lets:       map[*syntax.LetStmt]*Binding{},
		Assigns:    map[*syntax.AssignStmt]*Binding{},
		Calls:      map[syntax.Expr]*CallInfo{},
		Methods:    map[*syntax.MethodCall]*MethodInfo{},
		Fields:     map[*syntax.FieldExpr]int{},
		StructLits: map[*syntax.StructLit]*Struct{},
		Tries:      map[*syntax.TryExpr]*TryInfo{},
		Returns:    map[*syntax.ReturnStmt]*ReturnInfo{},
		Patterns:   map[*syntax.Pattern]*PatInfo{},
		Closures:   map[*syntax.Closure]*ClosureInfo{},
	}}
	c.declare(f)
	for _, fd := range f.Funcs {
		if fn := c.info.Funcs[fd.Name]; fn != nil && fn.Decl == fd {
			c.checkFunc(fn)
		}
	}
	c.computeAsync()
	for _, fn := range c.info.FuncList {
		checkLinearity(c.info, fn, &c.errs)
	}
	if err := c.errs.Err(); err != nil {
		return c.info, err
	}
	return c.info, nil
}

func (c *checker) errorf(pos syntax.Pos, format string, args ...any) {
	c.errs.Add(pos, format, args...)
}

func (c *checker) newVar() *Var {
	c.nextID++
	return &Var{ID: c.nextID}
}

func (c *checker) newBinding(name string, t Type, pos syntax.Pos) *Binding {
	c.nextID++
	return &Binding{ID: c.nextID, Name: name, Type: t, Pos: pos}
}

// ---- declarations ----

var reserved = map[string]bool{
	"Int": true, "Bool": true, "String": true, "Result": true, "Option": true,
	"Request": true, "Response": true, "TxError": true, "NetError": true,
	"Log": true, "Net": true, "Db": true, "Clock": true, "Random": true, "Tx": true,
	"Ok": true, "Err": true, "Some": true, "None": true,
}

func (c *checker) declare(f *syntax.File) {
	for _, sd := range f.Structs {
		if reserved[sd.Name] || c.info.Structs[sd.Name] != nil || c.info.Enums[sd.Name] != nil {
			c.errorf(sd.Pos, "type `%s` is already defined", sd.Name)
			continue
		}
		c.info.Structs[sd.Name] = &Struct{Name: sd.Name}
	}
	for _, ed := range f.Enums {
		if reserved[ed.Name] || c.info.Structs[ed.Name] != nil || c.info.Enums[ed.Name] != nil {
			c.errorf(ed.Pos, "type `%s` is already defined", ed.Name)
			continue
		}
		c.info.Enums[ed.Name] = &Enum{Name: ed.Name}
	}
	for _, sd := range f.Structs {
		st := c.info.Structs[sd.Name]
		if st == nil || len(st.Fields) > 0 {
			continue
		}
		seen := map[string]bool{}
		for _, fl := range sd.Fields {
			if seen[fl.Name] {
				c.errorf(fl.Pos, "duplicate field `%s`", fl.Name)
			}
			seen[fl.Name] = true
			t := c.resolveType(fl.Type, false)
			st.Fields = append(st.Fields, &FieldInfo{Name: fl.Name, Type: t})
		}
	}
	for _, ed := range f.Enums {
		en := c.info.Enums[ed.Name]
		if en == nil || len(en.Variants) > 0 {
			continue
		}
		seen := map[string]bool{}
		for _, v := range ed.Variants {
			if seen[v.Name] {
				c.errorf(v.Pos, "duplicate variant `%s`", v.Name)
			}
			seen[v.Name] = true
			vi := &VariantInfo{Name: v.Name}
			for _, ft := range v.Fields {
				t := c.resolveType(ft, false)
				vi.Fields = append(vi.Fields, t)
			}
			en.Variants = append(en.Variants, vi)
		}
		if len(en.Variants) == 0 {
			c.errorf(ed.Pos, "enum `%s` must have at least one variant", ed.Name)
		}
	}
	for _, fd := range f.Funcs {
		if c.info.Funcs[fd.Name] != nil {
			c.errorf(fd.Pos, "function `%s` is already defined", fd.Name)
			continue
		}
		fn := &Func{Name: fd.Name, Decl: fd, Calls: map[string]bool{}, Effects: map[string]bool{}}
		fn.Handler = fd.HasAttr("handler")
		for _, a := range fd.Attrs {
			if a.Name != "handler" {
				c.errorf(a.Pos, "unknown attribute `#[%s]`", a.Name)
			}
		}
		seen := map[string]bool{}
		for _, p := range fd.Params {
			if p.Name != "_" && seen[p.Name] {
				c.errorf(p.Pos, "duplicate parameter `%s`", p.Name)
			}
			seen[p.Name] = true
			t := c.resolveType(p.Type, true)
			if cp, ok := t.(*Cap); ok && cp.Kind == CapTx && !cp.Borrowed {
				c.errorf(p.Pos, "a function cannot take ownership of a transaction; borrow it with `&Tx` (only a `transaction` block can commit or roll back)")
			}
			b := c.newBinding(p.Name, t, p.Pos)
			b.Param = true
			fn.Params = append(fn.Params, b)
		}
		if fd.Result == nil {
			fn.Result = Unit
		} else {
			fn.Result = c.resolveType(fd.Result, false)
		}
		if fn.Handler {
			c.checkHandlerSig(fn)
		}
		c.info.Funcs[fd.Name] = fn
		c.info.FuncList = append(c.info.FuncList, fn)
	}
}

func (c *checker) checkHandlerSig(fn *Func) {
	if c.info.Handler != nil {
		c.errorf(fn.Decl.Pos, "only one #[handler] is allowed per program (also defined: `%s`)", c.info.Handler.Name)
		return
	}
	c.info.Handler = fn
	nreq := 0
	for _, p := range fn.Params {
		switch t := p.Type.(type) {
		case *Opaque:
			if t == Request {
				nreq++
				if nreq > 1 {
					c.errorf(p.Pos, "a handler takes at most one Request")
				}
				continue
			}
		case *Cap:
			if t.Kind != CapTx {
				continue
			}
		}
		c.errorf(p.Pos, "handler parameters must be `Request` or capabilities (&Log, &Net, &Db, &Clock, &Random), found `%s`", p.Type)
	}
	if fn.Result != Response {
		c.errorf(fn.Decl.Pos, "a handler must return `Response`, found `%s`", fn.Result)
	}
}

// resolveType converts a syntactic type. Capability types are accepted
// only when allowCap is set (function parameters).
func (c *checker) resolveType(te syntax.TypeExpr, allowCap bool) Type {
	switch te := te.(type) {
	case *syntax.UnitType:
		return Unit
	case *syntax.RefType:
		inner, ok := te.Elem.(*syntax.NamedType)
		if ok && len(inner.Args) == 0 {
			if k, ok := capByName[inner.Name]; ok {
				if !allowCap {
					c.errorf(te.Pos, "capability type `&%s` is only allowed as a function parameter", inner.Name)
				}
				return &Cap{Kind: k, Borrowed: true}
			}
		}
		c.errorf(te.Pos, "`&` can only be applied to capability types (Log, Net, Db, Clock, Random, Tx)")
		return c.resolveType(te.Elem, false)
	case *syntax.NamedType:
		args := func(n int) []Type {
			if len(te.Args) != n {
				c.errorf(te.Pos, "`%s` expects %d type argument(s), found %d", te.Name, n, len(te.Args))
			}
			out := make([]Type, n)
			for i := range out {
				if i < len(te.Args) {
					out[i] = c.resolveType(te.Args[i], false)
				} else {
					out[i] = Unit
				}
			}
			return out
		}
		switch te.Name {
		case "Int":
			args(0)
			return Int
		case "Bool":
			args(0)
			return Bool
		case "String":
			args(0)
			return String
		case "Result":
			a := args(2)
			return &ResultT{a[0], a[1]}
		case "Option":
			a := args(1)
			return &OptionT{a[0]}
		}
		for _, o := range []*Opaque{Request, Response, TxError, NetError} {
			if te.Name == o.Name {
				args(0)
				return o
			}
		}
		if k, ok := capByName[te.Name]; ok {
			args(0)
			if !allowCap {
				c.errorf(te.Pos, "capability type `%s` is only allowed as a function parameter", te.Name)
				return &Cap{Kind: k}
			}
			if k != CapTx {
				c.errorf(te.Pos, "capabilities are passed by borrowing: write `&%s`", te.Name)
				return &Cap{Kind: k, Borrowed: true}
			}
			return &Cap{Kind: k}
		}
		if st := c.info.Structs[te.Name]; st != nil {
			args(0)
			return st
		}
		if en := c.info.Enums[te.Name]; en != nil {
			args(0)
			return en
		}
		c.errorf(te.Pos, "unknown type `%s`", te.Name)
		return Unit
	}
	return Unit
}

// ---- unification ----

func occurs(v *Var, t Type) bool {
	t = Prune(t)
	switch t := t.(type) {
	case *Var:
		return t == v
	case *ResultT:
		return occurs(v, t.Ok) || occurs(v, t.Err)
	case *OptionT:
		return occurs(v, t.Elem)
	}
	return false
}

func unify(a, b Type) bool {
	a, b = Prune(a), Prune(b)
	if a == Never || b == Never || a == Invalid || b == Invalid {
		return true
	}
	if va, ok := a.(*Var); ok {
		if vb, ok := b.(*Var); ok && va == vb {
			return true
		}
		if occurs(va, b) {
			return false
		}
		va.Ref = b
		return true
	}
	if _, ok := b.(*Var); ok {
		return unify(b, a)
	}
	switch a := a.(type) {
	case *ResultT:
		b, ok := b.(*ResultT)
		return ok && unify(a.Ok, b.Ok) && unify(a.Err, b.Err)
	case *OptionT:
		b, ok := b.(*OptionT)
		return ok && unify(a.Elem, b.Elem)
	case *Cap:
		b, ok := b.(*Cap)
		return ok && a.Kind == b.Kind && a.Borrowed == b.Borrowed
	}
	return a == b
}

// join returns the type of two branches (Never is absorbed).
func join(a, b Type) Type {
	if Prune(a) == Never {
		return b
	}
	return a
}

func (c *checker) expect(pos syntax.Pos, got, want Type, what string) {
	if !unify(got, want) {
		c.errorf(pos, "mismatched types in %s: expected `%s`, found `%s`", what, want, got)
	}
}

// ---- functions ----

func (c *checker) push(txBoundary bool) {
	c.scope = &scope{parent: c.scope, vars: map[string]*Binding{}, txBoundary: txBoundary}
}

func (c *checker) pop() { c.scope = c.scope.parent }

func (c *checker) bind(b *Binding) {
	if b.Name == "_" {
		return
	}
	c.scope.vars[b.Name] = b
}

func (c *checker) checkFunc(fn *Func) {
	c.fn = fn
	c.scope = nil
	c.push(false)
	for _, p := range fn.Params {
		c.bind(p)
	}
	c.rets = []retCtx{{typ: fn.Result}}
	t := c.checkBlock(fn.Decl.Body)
	c.expect(tailPos(fn.Decl.Body), t, fn.Result, "function result")
	c.pop()
	c.fn = nil
}

// tailPos is the position to blame for a block's value.
func tailPos(b *syntax.Block) syntax.Pos {
	if b.Tail != nil {
		return syntax.ExprPos(b.Tail)
	}
	return b.End
}

// lookup resolves a name; it reports masked irrevocable capabilities used
// inside a transaction.
func (c *checker) lookup(id *syntax.Ident) *Binding {
	crossed := false
	for s := c.scope; s != nil; s = s.parent {
		if b, ok := s.vars[id.Name]; ok {
			if cp, ok := b.Type.(*Cap); ok && crossed && !cp.Kind.Info().Revocable {
				c.errorf(id.Pos, "capability `%s: %s` cannot be used inside a transaction: its effects cannot be rolled back (use `tx.outbox(...)` to run an effect after commit)", b.Name, b.Type)
			}
			return b
		}
		if s.txBoundary {
			crossed = true
		}
	}
	return nil
}

func (c *checker) record(e syntax.Expr, t Type) Type {
	c.info.Types[e] = t
	return t
}

func (c *checker) checkBlock(b *syntax.Block) Type {
	c.push(false)
	defer c.pop()
	diverges := false
	for _, s := range b.Stmts {
		if c.checkStmt(s) {
			diverges = true
		}
	}
	var t Type = Unit
	if b.Tail != nil {
		t = c.checkExpr(b.Tail)
	} else if diverges {
		t = Never
	}
	return c.record(b, t)
}

// checkStmt checks a statement and reports whether it diverges.
func (c *checker) checkStmt(s syntax.Stmt) bool {
	switch s := s.(type) {
	case *syntax.LetStmt:
		t := c.checkExpr(s.Init)
		if s.Type != nil {
			want := c.resolveType(s.Type, false)
			c.expect(syntax.ExprPos(s.Init), t, want, "let binding")
			t = want
		}
		if Prune(t) == Never {
			t = c.newVar()
		}
		b := c.newBinding(s.Name, t, s.Pos)
		b.Mut = s.Mut
		c.info.Lets[s] = b
		c.bind(b)
		return false
	case *syntax.AssignStmt:
		b := c.lookup(&syntax.Ident{Pos: s.Pos, Name: s.Name})
		t := c.checkExpr(s.Value)
		if b == nil {
			c.errorf(s.Pos, "cannot find value `%s` in this scope", s.Name)
			return false
		}
		if IsCap(b.Type) {
			c.errorf(s.Pos, "cannot assign to capability `%s`", s.Name)
			return false
		}
		if !b.Mut {
			c.errorf(s.Pos, "cannot assign twice to immutable variable `%s` (declare it with `let mut`)", s.Name)
		}
		c.info.Assigns[s] = b
		c.expect(syntax.ExprPos(s.Value), t, b.Type, "assignment")
		return false
	case *syntax.WhileStmt:
		ct := c.checkExpr(s.Cond)
		c.expect(syntax.ExprPos(s.Cond), ct, Bool, "while condition")
		bt := c.checkBlock(s.Body)
		c.expect(s.Body.End, bt, Unit, "while body")
		return false
	case *syntax.ReturnStmt:
		rc := c.rets[len(c.rets)-1]
		c.info.Returns[s] = &ReturnInfo{Closure: rc.closure}
		if s.Value == nil {
			c.expect(s.Pos, Unit, rc.typ, "return")
		} else {
			t := c.checkExpr(s.Value)
			c.expect(syntax.ExprPos(s.Value), t, rc.typ, "return")
		}
		return true
	case *syntax.ExprStmt:
		t := c.checkExpr(s.X)
		if Prune(t) == Never {
			return true
		}
		if !s.Semi {
			c.expect(s.Pos, t, Unit, "statement")
		}
		return false
	}
	return false
}

func (c *checker) checkExpr(e syntax.Expr) Type {
	return c.checkExprCap(e, false)
}

// checkExprCap checks an expression. Capability-typed identifiers are only
// permitted when allowCap is set (method receivers and capability
// arguments).
func (c *checker) checkExprCap(e syntax.Expr, allowCap bool) Type {
	switch e := e.(type) {
	case *syntax.IntLit:
		return c.record(e, Int)
	case *syntax.BoolLit:
		return c.record(e, Bool)
	case *syntax.StringLit:
		return c.record(e, String)
	case *syntax.UnitLit:
		return c.record(e, Unit)
	case *syntax.Ident:
		if e.Name == "None" {
			if b := c.lookup(e); b == nil {
				t := &OptionT{c.newVar()}
				c.info.Calls[e] = &CallInfo{Kind: CallNone, Type: t}
				return c.record(e, t)
			}
		}
		b := c.lookup(e)
		if b == nil {
			c.errorf(e.Pos, "cannot find value `%s` in this scope", e.Name)
			return c.record(e, Invalid)
		}
		b.Uses++
		c.info.Uses[e] = b
		if IsCap(b.Type) && !allowCap {
			c.errorf(e.Pos, "capability `%s` cannot be used as a value: capabilities are second-class and can only be passed as arguments or used as method receivers", b.Name)
		}
		return c.record(e, b.Type)
	case *syntax.UnaryExpr:
		t := c.checkExpr(e.X)
		if e.Op == syntax.Minus {
			c.expect(e.Pos, t, Int, "negation")
			return c.record(e, Int)
		}
		c.expect(e.Pos, t, Bool, "`!`")
		return c.record(e, Bool)
	case *syntax.BinaryExpr:
		return c.record(e, c.checkBinary(e))
	case *syntax.Block:
		return c.checkBlock(e)
	case *syntax.IfExpr:
		ct := c.checkExpr(e.Cond)
		c.expect(syntax.ExprPos(e.Cond), ct, Bool, "if condition")
		tt := c.checkBlock(e.Then)
		if e.Else == nil {
			c.expect(e.Then.End, tt, Unit, "`if` without `else`")
			return c.record(e, Unit)
		}
		et := c.checkExpr(e.Else)
		if !unify(tt, et) {
			c.errorf(e.Pos, "`if` and `else` have incompatible types: `%s` vs `%s`", tt, et)
		}
		return c.record(e, join(tt, et))
	case *syntax.MatchExpr:
		return c.record(e, c.checkMatch(e))
	case *syntax.CallExpr:
		return c.record(e, c.checkCall(e))
	case *syntax.PathExpr:
		return c.record(e, c.checkPath(e, nil))
	case *syntax.MethodCall:
		return c.record(e, c.checkMethod(e))
	case *syntax.FieldExpr:
		t := Prune(c.checkExpr(e.X))
		st, ok := t.(*Struct)
		if t == Invalid {
			return c.record(e, Invalid)
		}
		if !ok {
			c.errorf(e.Pos, "no field `%s` on type `%s`", e.Name, t)
			return c.record(e, Invalid)
		}
		i, fi := st.Field(e.Name)
		if fi == nil {
			c.errorf(e.Pos, "struct `%s` has no field `%s`", st.Name, e.Name)
			return c.record(e, Invalid)
		}
		c.info.Fields[e] = i
		return c.record(e, fi.Type)
	case *syntax.StructLit:
		st := c.info.Structs[e.Name]
		if st == nil {
			c.errorf(e.Pos, "unknown struct `%s`", e.Name)
			for _, f := range e.Fields {
				c.checkExpr(f.Value)
			}
			return c.record(e, Invalid)
		}
		c.info.StructLits[e] = st
		seen := map[string]bool{}
		for _, f := range e.Fields {
			t := c.checkExpr(f.Value)
			_, fi := st.Field(f.Name)
			if fi == nil {
				c.errorf(f.Pos, "struct `%s` has no field `%s`", st.Name, f.Name)
				continue
			}
			if seen[f.Name] {
				c.errorf(f.Pos, "field `%s` specified more than once", f.Name)
			}
			seen[f.Name] = true
			c.expect(syntax.ExprPos(f.Value), t, fi.Type, "field `"+f.Name+"`")
		}
		for _, fi := range st.Fields {
			if !seen[fi.Name] {
				c.errorf(e.Pos, "missing field `%s` in initializer of `%s`", fi.Name, st.Name)
			}
		}
		return c.record(e, st)
	case *syntax.TryExpr:
		return c.record(e, c.checkTry(e))
	case *syntax.Closure:
		c.errorf(e.Pos, "closures are second-class: they can only appear as the body of `transaction`")
		return c.record(e, Invalid)
	}
	panic(fmt.Sprintf("unhandled expression %T", e))
}

func (c *checker) checkBinary(e *syntax.BinaryExpr) Type {
	xt := c.checkExpr(e.X)
	yt := c.checkExpr(e.Y)
	switch e.Op {
	case syntax.AmpAmp, syntax.PipePipe:
		c.expect(syntax.ExprPos(e.X), xt, Bool, "logical operator")
		c.expect(syntax.ExprPos(e.Y), yt, Bool, "logical operator")
		return Bool
	case syntax.Eq, syntax.Ne:
		if !unify(xt, yt) {
			c.errorf(e.Pos, "cannot compare `%s` with `%s`", xt, yt)
			return Bool
		}
		switch Prune(xt) {
		case Int, Bool, String, Unit:
		default:
			c.errorf(e.Pos, "`%s` cannot be compared with `==` (only Int, Bool, String)", Prune(xt))
		}
		return Bool
	case syntax.Lt, syntax.Le, syntax.Gt, syntax.Ge:
		c.expect(syntax.ExprPos(e.X), xt, Int, "comparison")
		c.expect(syntax.ExprPos(e.Y), yt, Int, "comparison")
		return Bool
	case syntax.Plus:
		if Prune(xt) == String || Prune(yt) == String {
			c.expect(syntax.ExprPos(e.X), xt, String, "string concatenation")
			c.expect(syntax.ExprPos(e.Y), yt, String, "string concatenation")
			return String
		}
		fallthrough
	default:
		c.expect(syntax.ExprPos(e.X), xt, Int, "arithmetic")
		c.expect(syntax.ExprPos(e.Y), yt, Int, "arithmetic")
		return Int
	}
}

func (c *checker) checkArgs(pos syntax.Pos, what string, args []syntax.Expr, params []Type) {
	if len(args) != len(params) {
		c.errorf(pos, "%s takes %d argument(s) but %d were supplied", what, len(params), len(args))
	}
	for i, a := range args {
		if i >= len(params) {
			c.checkExpr(a)
			continue
		}
		c.checkArg(a, params[i], what)
	}
}

// checkArg checks one argument; capability parameters accept only a
// capability variable (borrowing it for the duration of the call).
func (c *checker) checkArg(a syntax.Expr, want Type, what string) {
	wc, isCap := want.(*Cap)
	if !isCap {
		t := c.checkExpr(a)
		c.expect(syntax.ExprPos(a), t, want, "argument of "+what)
		return
	}
	id, ok := a.(*syntax.Ident)
	if !ok {
		c.checkExpr(a)
		c.errorf(syntax.ExprPos(a), "expected capability `%s` as argument of %s", want, what)
		return
	}
	t := Prune(c.checkExprCap(id, true))
	gc, ok := t.(*Cap)
	if !ok || gc.Kind != wc.Kind {
		c.errorf(id.Pos, "mismatched types in argument of %s: expected capability `%s`, found `%s`", what, want, t)
	}
}

func (c *checker) checkCall(e *syntax.CallExpr) Type {
	switch f := e.Func.(type) {
	case *syntax.Ident:
		switch f.Name {
		case "Ok", "Err", "Some":
			if c.lookup(f) == nil && c.info.Funcs[f.Name] == nil {
				var arg Type = c.newVar()
				if len(e.Args) != 1 {
					c.errorf(e.Pos, "`%s` takes exactly one argument", f.Name)
					for _, a := range e.Args {
						c.checkExpr(a)
					}
				} else {
					arg = c.checkExpr(e.Args[0])
				}
				var t Type
				ci := &CallInfo{}
				switch f.Name {
				case "Ok":
					t, ci.Kind = &ResultT{arg, c.newVar()}, CallOk
				case "Err":
					t, ci.Kind = &ResultT{c.newVar(), arg}, CallErr
				default:
					t, ci.Kind = &OptionT{arg}, CallSome
				}
				ci.Type = t
				c.info.Calls[e] = ci
				return t
			}
		}
		fn := c.info.Funcs[f.Name]
		if fn == nil {
			if b := c.lookup(f); b != nil {
				c.errorf(f.Pos, "`%s` is not a function (functions are not first-class in Kekkai)", f.Name)
			} else {
				c.errorf(f.Pos, "cannot find function `%s`", f.Name)
			}
			for _, a := range e.Args {
				c.checkExpr(a)
			}
			return Invalid
		}
		if fn.Handler {
			c.errorf(f.Pos, "the #[handler] `%s` cannot be called directly", fn.Name)
		}
		params := make([]Type, len(fn.Params))
		for i, p := range fn.Params {
			params[i] = p.Type
		}
		c.checkArgs(e.Pos, "`"+fn.Name+"`", e.Args, params)
		c.fn.Calls[fn.Name] = true
		c.info.Calls[e] = &CallInfo{Kind: CallFunc, Func: fn, Type: fn.Result}
		return fn.Result
	case *syntax.PathExpr:
		return c.checkPath(f, e)
	}
	c.errorf(e.Pos, "expression is not callable")
	for _, a := range e.Args {
		c.checkExpr(a)
	}
	return Invalid
}

// checkPath checks `Type::Name` (call is non-nil when applied to args).
func (c *checker) checkPath(p *syntax.PathExpr, call *syntax.CallExpr) Type {
	var key syntax.Expr = p
	var args []syntax.Expr
	pos := p.Pos
	if call != nil {
		key, args, pos = call, call.Args, call.Pos
	}
	if en := c.info.Enums[p.Type]; en != nil {
		tag, v := en.Variant(p.Name)
		if v == nil {
			c.errorf(p.Pos, "no variant `%s` in enum `%s`", p.Name, en.Name)
			for _, a := range args {
				c.checkExpr(a)
			}
			return Invalid
		}
		if call == nil && len(v.Fields) > 0 {
			c.errorf(p.Pos, "variant `%s::%s` takes %d argument(s)", en.Name, v.Name, len(v.Fields))
		}
		c.checkArgs(pos, "`"+en.Name+"::"+v.Name+"`", args, v.Fields)
		c.info.Calls[key] = &CallInfo{Kind: CallVariant, Enum: en, Tag: tag, Type: en}
		return en
	}
	if b := LookupStatic(p.Type, p.Name); b != nil {
		if call == nil {
			c.errorf(p.Pos, "`%s::%s` is a function; call it with `()`", p.Type, p.Name)
		}
		c.checkArgs(pos, "`"+p.Type+"::"+p.Name+"`", args, b.Params)
		c.info.Calls[key] = &CallInfo{Kind: CallStatic, Builtin: b, Type: b.Result}
		return b.Result
	}
	c.errorf(p.Pos, "cannot find `%s::%s`", p.Type, p.Name)
	for _, a := range args {
		c.checkExpr(a)
	}
	return Invalid
}

func recvName(t Type) string {
	switch t := Prune(t).(type) {
	case *Prim:
		return t.Name
	case *Opaque:
		return t.Name
	case *Cap:
		return t.Kind.String()
	case *Struct:
		return t.Name
	case *Enum:
		return t.Name
	case *ResultT:
		return "Result"
	case *OptionT:
		return "Option"
	}
	return "?"
}

func (c *checker) checkMethod(e *syntax.MethodCall) Type {
	rt := Prune(c.checkExprCap(e.Recv, true))
	if rt == Invalid {
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return Invalid
	}
	if cp, ok := rt.(*Cap); ok {
		if _, isIdent := e.Recv.(*syntax.Ident); !isIdent {
			c.errorf(e.Pos, "capability receiver must be a variable")
		}
		if cp.Kind == CapDb && e.Name == "transaction" {
			return c.checkTransaction(e)
		}
	}
	switch rt := rt.(type) {
	case *ResultT, *OptionT:
		return c.checkGenericMethod(e, rt)
	case *Var:
		c.errorf(e.Pos, "type annotations needed: cannot call method `%s` on a value of unknown type", e.Name)
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return Invalid
	}
	name := recvName(rt)
	b := LookupMethod(name, e.Name)
	if b == nil {
		c.errorf(e.Pos, "no method named `%s` found for `%s`", e.Name, rt)
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return Invalid
	}
	if b.Consumes {
		if cp, ok := rt.(*Cap); ok && cp.Borrowed {
			c.errorf(e.Pos, "cannot call `%s` on a borrowed transaction `&Tx`: only the `transaction` block that owns the `Tx` can end it", e.Name)
		}
	}
	what := "`" + name + "." + e.Name + "`"
	c.checkArgs(e.Pos, what, e.Args, b.Params)
	if _, ok := rt.(*Cap); ok {
		c.fn.Effects[b.Op] = true
	}
	c.info.Methods[e] = &MethodInfo{Kind: MethodBuiltin, Builtin: b, RecvTy: rt}
	return b.Result
}

func (c *checker) checkGenericMethod(e *syntax.MethodCall, rt Type) Type {
	mi := &MethodInfo{Kind: MethodGeneric, Op: e.Name, RecvTy: rt}
	var result Type
	switch rt := rt.(type) {
	case *ResultT:
		switch e.Name {
		case "is_ok", "is_err":
			c.checkArgs(e.Pos, "`"+e.Name+"`", e.Args, nil)
			result = Bool
		case "unwrap_or":
			c.checkArgs(e.Pos, "`unwrap_or`", e.Args, []Type{rt.Ok})
			result = rt.Ok
		}
	case *OptionT:
		switch e.Name {
		case "is_some", "is_none":
			c.checkArgs(e.Pos, "`"+e.Name+"`", e.Args, nil)
			result = Bool
		case "unwrap_or":
			c.checkArgs(e.Pos, "`unwrap_or`", e.Args, []Type{rt.Elem})
			result = rt.Elem
		}
	}
	if result == nil {
		c.errorf(e.Pos, "no method named `%s` found for `%s`", e.Name, rt)
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return Invalid
	}
	c.info.Methods[e] = mi
	return result
}

func (c *checker) checkTransaction(e *syntax.MethodCall) Type {
	c.fn.Effects["db.transaction"] = true
	if len(e.Args) != 1 {
		c.errorf(e.Pos, "`transaction` takes exactly one closure argument `|tx| { ... }`")
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return Invalid
	}
	cl, ok := e.Args[0].(*syntax.Closure)
	if !ok || len(cl.Params) != 1 {
		c.errorf(syntax.ExprPos(e.Args[0]), "`transaction` expects a closure with one parameter: `|tx| { ... }`")
		c.checkExpr(e.Args[0])
		return Invalid
	}
	txType := &Cap{Kind: CapTx}
	if pt := cl.Params[0].Type; pt != nil {
		if t := c.resolveType(pt, true); !Identical(t, txType) {
			c.errorf(cl.Params[0].Pos, "the transaction parameter has type `Tx`, found `%s`", t)
		}
	}
	tx := c.newBinding(cl.Params[0].Name, txType, cl.Params[0].Pos)
	result := &ResultT{c.newVar(), c.newVar()}
	ci := &ClosureInfo{Tx: tx, Result: result}
	c.info.Closures[cl] = ci
	c.push(true)
	c.bind(tx)
	c.rets = append(c.rets, retCtx{typ: result, closure: ci})
	bt := c.checkExprCap(cl.Body, false)
	c.rets = c.rets[:len(c.rets)-1]
	c.pop()
	if !unify(bt, result) {
		c.errorf(syntax.ExprPos(cl.Body), "a transaction body must evaluate to a `Result` (usually `tx.commit()`), found `%s`", bt)
	}
	c.info.Types[cl] = result
	c.info.Methods[e] = &MethodInfo{Kind: MethodTransaction, Closure: ci}
	return result
}

func (c *checker) checkTry(e *syntax.TryExpr) Type {
	t := Prune(c.checkExpr(e.X))
	if t == Invalid {
		return Invalid
	}
	rc := c.rets[len(c.rets)-1]
	target := Prune(rc.typ)
	c.info.Tries[e] = &TryInfo{Closure: rc.closure, Target: rc.typ}
	switch t := t.(type) {
	case *ResultT:
		if _, ok := target.(*Var); ok {
			unify(target, &ResultT{c.newVar(), c.newVar()})
			target = Prune(target)
		}
		tr, ok := target.(*ResultT)
		if !ok {
			c.errorf(e.Pos, "the `?` operator can only be used on `Result` in a function that returns `Result` (this returns `%s`)", target)
			return t.Ok
		}
		if !unify(t.Err, tr.Err) {
			c.errorf(e.Pos, "`?` cannot convert the error type `%s` into `%s`", Resolve(t.Err), Resolve(tr.Err))
		}
		return t.Ok
	case *OptionT:
		if _, ok := target.(*OptionT); !ok {
			c.errorf(e.Pos, "the `?` operator can only be used on `Option` in a function that returns `Option` (this returns `%s`)", target)
		}
		return t.Elem
	case *Var:
		c.errorf(e.Pos, "type annotations needed for `?`")
		return Invalid
	}
	c.errorf(e.Pos, "the `?` operator can only be applied to `Result` or `Option`, found `%s`", t)
	return Invalid
}

// ---- match ----

func (c *checker) checkMatch(e *syntax.MatchExpr) Type {
	st := Prune(c.checkExpr(e.X))
	if st == Invalid {
		st = c.newVar()
	}
	if _, ok := st.(*Var); ok {
		c.errorf(e.Pos, "type annotations needed for match scrutinee")
		return Invalid
	}
	var result Type = Never
	var rows [][]*syntax.Pattern
	for _, arm := range e.Arms {
		c.push(false)
		c.checkPattern(arm.Pat, st)
		if len(rows) > 0 && exhaustive(c.info, rows, []Type{st}) {
			c.errorf(arm.Pos, "unreachable pattern")
		}
		rows = append(rows, []*syntax.Pattern{arm.Pat})
		at := c.checkExpr(arm.Body)
		if !unify(result, at) {
			c.errorf(syntax.ExprPos(arm.Body), "match arms have incompatible types: expected `%s`, found `%s`", Resolve(result), Resolve(at))
		}
		result = join(result, at)
		c.pop()
	}
	if !exhaustive(c.info, rows, []Type{st}) {
		c.errorf(e.Pos, "non-exhaustive patterns in match on `%s`%s", Resolve(st), missing(c.info, rows, st))
	}
	return result
}

// ctors returns the constructor names and field types of a finite type,
// or nil for types with infinitely many values (Int, String, ...).
func ctors(t Type) ([]string, [][]Type) {
	switch t := Prune(t).(type) {
	case *ResultT:
		return []string{"Ok(_)", "Err(_)"}, [][]Type{{t.Ok}, {t.Err}}
	case *OptionT:
		return []string{"None", "Some(_)"}, [][]Type{nil, {t.Elem}}
	case *Enum:
		var names []string
		var fields [][]Type
		for _, v := range t.Variants {
			n := t.Name + "::" + v.Name
			if len(v.Fields) > 0 {
				n += "(..)"
			}
			names = append(names, n)
			fields = append(fields, v.Fields)
		}
		return names, fields
	case *Prim:
		if t == Bool {
			return []string{"false", "true"}, [][]Type{nil, nil}
		}
	}
	return nil, nil
}

// patTag returns (tag, true) if p tests a constructor (or bool literal),
// and false for irrefutable patterns. Other literal patterns return tag -1.
func patTag(info *Info, p *syntax.Pattern) (int, bool) {
	pi := info.Patterns[p]
	if pi == nil {
		return 0, false
	}
	switch pi.Kind {
	case PatCtor:
		return pi.Tag, true
	case PatBool:
		if *p.BoolLit {
			return 1, true
		}
		return 0, true
	case PatInt, PatString:
		return -1, true
	}
	return 0, false
}

var wildPat = &syntax.Pattern{Wildcard: true}

// exhaustive reports whether the pattern matrix covers every value of the
// column types (Maranget's usefulness algorithm, specialised to the
// question "is the all-wildcards row useful?").
func exhaustive(info *Info, rows [][]*syntax.Pattern, tys []Type) bool {
	if len(tys) == 0 {
		return len(rows) > 0
	}
	names, fields := ctors(tys[0])
	if names == nil {
		var def [][]*syntax.Pattern
		for _, r := range rows {
			if _, refutable := patTag(info, r[0]); !refutable {
				def = append(def, r[1:])
			}
		}
		return exhaustive(info, def, tys[1:])
	}
	for tag := range names {
		var spec [][]*syntax.Pattern
		for _, r := range rows {
			t, refutable := patTag(info, r[0])
			switch {
			case !refutable:
				row := make([]*syntax.Pattern, 0, len(fields[tag])+len(r)-1)
				for range fields[tag] {
					row = append(row, wildPat)
				}
				spec = append(spec, append(row, r[1:]...))
			case t == tag:
				args := info.Patterns[r[0]].Args
				if len(args) != len(fields[tag]) {
					continue // arity error already reported
				}
				spec = append(spec, append(append([]*syntax.Pattern{}, args...), r[1:]...))
			}
		}
		if !exhaustive(info, spec, append(append([]Type{}, fields[tag]...), tys[1:]...)) {
			return false
		}
	}
	return true
}

// missing names the top-level constructors that are not fully covered.
func missing(info *Info, rows [][]*syntax.Pattern, t Type) string {
	names, fields := ctors(t)
	if names == nil {
		return ": add a `_` arm"
	}
	var miss []string
	for tag, n := range names {
		var spec [][]*syntax.Pattern
		for _, r := range rows {
			tg, refutable := patTag(info, r[0])
			if !refutable {
				row := []*syntax.Pattern{}
				for range fields[tag] {
					row = append(row, wildPat)
				}
				spec = append(spec, row)
			} else if tg == tag && len(info.Patterns[r[0]].Args) == len(fields[tag]) {
				spec = append(spec, info.Patterns[r[0]].Args)
			}
		}
		if !exhaustive(info, spec, fields[tag]) {
			miss = append(miss, "`"+n+"`")
		}
	}
	sort.Strings(miss)
	out := ": missing "
	for i, m := range miss {
		if i > 0 {
			out += ", "
		}
		out += m
	}
	return out
}

func (c *checker) checkPattern(p *syntax.Pattern, st Type) {
	st = Prune(st)
	pi := &PatInfo{Type: st}
	c.info.Patterns[p] = pi
	subs := func(fields []Type) {
		if len(p.Args) != len(fields) {
			c.errorf(p.Pos, "pattern `%s` expects %d field(s), found %d", p.Ctor, len(fields), len(p.Args))
		}
		pi.Args = p.Args
		for i, sub := range p.Args {
			var t Type = Invalid
			if i < len(fields) {
				t = fields[i]
			}
			c.checkPattern(sub, t)
		}
	}
	switch {
	case p.Wildcard:
		pi.Kind = PatWild
		return
	case p.IntValue != nil:
		pi.Kind = PatInt
		if !unify(st, Int) {
			c.errorf(p.Pos, "integer pattern used on `%s`", st)
		}
		return
	case p.BoolLit != nil:
		pi.Kind = PatBool
		if !unify(st, Bool) {
			c.errorf(p.Pos, "boolean pattern used on `%s`", st)
		}
		return
	case p.StrValue != nil:
		pi.Kind = PatString
		if !unify(st, String) {
			c.errorf(p.Pos, "string pattern used on `%s`", st)
		}
		return
	}
	ctor := p.Ctor
	if ctor == "" {
		ctor = p.Bind
	}
	switch t := st.(type) {
	case *ResultT:
		if p.Type == "" && (ctor == "Ok" || ctor == "Err") {
			pi.Kind = PatCtor
			if ctor == "Ok" {
				subs([]Type{t.Ok})
			} else {
				pi.Tag = 1
				subs([]Type{t.Err})
			}
			return
		}
	case *OptionT:
		if p.Type == "" && ctor == "Some" {
			pi.Kind, pi.Tag = PatCtor, 1
			subs([]Type{t.Elem})
			return
		}
		if p.Type == "" && ctor == "None" {
			pi.Kind, pi.Tag = PatCtor, 0
			subs(nil)
			return
		}
	case *Enum:
		if p.Type != "" && p.Type != t.Name {
			c.errorf(p.Pos, "expected a variant of `%s`, found `%s::%s`", t.Name, p.Type, ctor)
			pi.Kind = PatWild
			return
		}
		if tag, v := t.Variant(ctor); v != nil {
			pi.Kind, pi.Tag = PatCtor, tag
			subs(v.Fields)
			return
		}
		if p.Type != "" {
			c.errorf(p.Pos, "no variant `%s` in enum `%s`", ctor, t.Name)
			pi.Kind = PatWild
			return
		}
	}
	if p.Ctor != "" {
		if st != Invalid {
			c.errorf(p.Pos, "pattern `%s` does not match type `%s`", ctor, st)
		}
		pi.Kind = PatWild
		for _, sub := range p.Args {
			c.checkPattern(sub, Invalid)
		}
		return
	}
	pi.Kind = PatBind
	b := c.newBinding(p.Bind, st, p.Pos)
	c.bind(b)
	pi.Bind = b
}

// ---- async analysis ----

func (c *checker) computeAsync() {
	asyncOps := map[string]bool{"db.transaction": true}
	for _, b := range AllBuiltins() {
		if b.Async {
			asyncOps[b.Op] = true
		}
	}
	for _, fn := range c.info.FuncList {
		// The handler is the entry point driven by the JS event loop.
		fn.Async = fn.Handler
		for op := range fn.Effects {
			if asyncOps[op] {
				fn.Async = true
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range c.info.FuncList {
			if fn.Async {
				continue
			}
			for callee := range fn.Calls {
				if c.info.Funcs[callee].Async {
					fn.Async = true
					changed = true
					break
				}
			}
		}
	}
}
