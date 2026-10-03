package ir

import (
	"fmt"

	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/types"
)

// Host operations introduced by lowering (in addition to builtin Ops).
const (
	// OpTxBegin starts a transaction on a Db: await db.begin(db) -> Tx.
	OpTxBegin = "db.begin"
	// OpTxFinish ends a transaction block: if the Tx is still open (the
	// body left through `?`), it is rolled back. await tx.finish(tx) -> ().
	OpTxFinish = "tx.finish"
)

// Lower translates a type-checked program into IR.
func Lower(info *types.Info) *Program {
	l := &lowerer{info: info, prog: &Program{}, aggs: map[string]int{}}
	for _, fn := range info.FuncList {
		l.prog.Funcs = append(l.prog.Funcs, l.lowerFunc(fn))
	}
	if h := info.Handler; h != nil {
		l.prog.Handler = h.Name
		for _, p := range h.Params {
			kind := "request"
			if cp, ok := p.Type.(*types.Cap); ok {
				kind = cp.Kind.String()
			}
			l.prog.HandlerParams = append(l.prog.HandlerParams, HandlerParam{Kind: kind, Name: p.Name})
		}
	}
	return l.prog
}

type lowerer struct {
	info *types.Info
	prog *Program
	aggs map[string]int

	fn    *Func
	cur   int // current block index
	vars  map[*types.Binding]int
	exits []*closureExit
}

type closureExit struct {
	info   *types.ClosureInfo
	result int // local receiving the body's Result
	block  int // exit block
}

// ---- types ----

func (l *lowerer) ty(t types.Type) Ty {
	t = types.Resolve(t)
	switch t := t.(type) {
	case *types.Prim:
		switch t {
		case types.Bool:
			return Ty{Kind: TBool}
		case types.Int:
			return Ty{Kind: TInt}
		case types.String:
			return Ty{Kind: TString}
		}
		return Ty{Kind: TUnit}
	case *types.Opaque:
		return Ty{Kind: TExt, Ext: t.Name}
	case *types.Cap:
		return Ty{Kind: TExt, Ext: t.Kind.String()}
	}
	return Ty{Kind: TAgg, Agg: l.agg(t)}
}

func (l *lowerer) agg(t types.Type) int {
	key := types.Key(t)
	if i, ok := l.aggs[key]; ok {
		return i
	}
	i := len(l.prog.Types)
	td := &TypeDef{Name: key}
	l.prog.Types = append(l.prog.Types, td)
	l.aggs[key] = i
	switch t := t.(type) {
	case *types.Struct:
		for _, f := range t.Fields {
			td.Fields = append(td.Fields, l.ty(f.Type))
		}
	case *types.Enum:
		td.IsVariant = true
		for _, v := range t.Variants {
			var fs []Ty
			for _, f := range v.Fields {
				fs = append(fs, l.ty(f))
			}
			td.Variants = append(td.Variants, fs)
		}
	case *types.ResultT:
		td.IsVariant = true
		td.Variants = [][]Ty{{l.ty(t.Ok)}, {l.ty(t.Err)}}
	case *types.OptionT:
		td.IsVariant = true
		td.Variants = [][]Ty{{}, {l.ty(t.Elem)}}
	default:
		panic(fmt.Sprintf("no aggregate layout for %s", t))
	}
	return i
}

// ---- builder ----

func (l *lowerer) newLocal(t Ty, name string) int {
	l.fn.Locals = append(l.fn.Locals, t)
	l.fn.Names = append(l.fn.Names, name)
	return len(l.fn.Locals) - 1
}

func (l *lowerer) newBlock() int {
	l.fn.Blocks = append(l.fn.Blocks, &Block{})
	return len(l.fn.Blocks) - 1
}

func (l *lowerer) emit(in Instr) int {
	b := l.fn.Blocks[l.cur]
	b.Instrs = append(b.Instrs, in)
	return in.Dst
}

func (l *lowerer) emitTo(t Ty, in Instr) int {
	in.Dst = l.newLocal(t, "")
	return l.emit(in)
}

func (l *lowerer) terminate(t Term) {
	l.fn.Blocks[l.cur].Term = t
	// Code after a terminator is unreachable; keep emitting into a fresh
	// block so lowering stays simple.
	l.cur = l.newBlock()
	l.fn.Blocks[l.cur].Term = Term{Op: "unreachable"}
}

func (l *lowerer) jump(target int) { l.terminate(Term{Op: "jump", Targets: []int{target}}) }

func (l *lowerer) setBlock(b int) {
	l.cur = b
	if l.fn.Blocks[b].Term.Op == "" {
		l.fn.Blocks[b].Term = Term{Op: "unreachable"}
	}
}

func (l *lowerer) constant(c Const) int {
	return l.emitTo(Ty{Kind: c.Kind}, Instr{Op: "const", Const: &c})
}

func (l *lowerer) unit() int        { return l.constant(Const{Kind: TUnit}) }
func (l *lowerer) intc(v int64) int { return l.constant(Const{Kind: TInt, Int: v}) }

func (l *lowerer) copyTo(dst, src int) {
	l.emit(Instr{Op: "copy", Dst: dst, Args: []int{src}})
}

// ---- functions ----

func (l *lowerer) lowerFunc(fn *types.Func) *Func {
	l.fn = &Func{Name: fn.Name, NParams: len(fn.Params), Result: l.ty(fn.Result), Async: fn.Async}
	l.vars = map[*types.Binding]int{}
	l.exits = nil
	for _, p := range fn.Params {
		l.vars[p] = l.newLocal(l.ty(p.Type), p.Name)
	}
	l.cur = l.newBlock()
	v := l.block(fn.Decl.Body)
	l.terminate(Term{Op: "ret", Args: []int{v}})
	prune(l.fn)
	return l.fn
}

func (l *lowerer) block(b *syntax.Block) int {
	for _, s := range b.Stmts {
		l.stmt(s)
	}
	if b.Tail != nil {
		return l.expr(b.Tail)
	}
	return l.unit()
}

func (l *lowerer) stmt(s syntax.Stmt) {
	switch s := s.(type) {
	case *syntax.LetStmt:
		v := l.expr(s.Init)
		b := l.info.Lets[s]
		dst := l.newLocal(l.ty(b.Type), b.Name)
		l.copyTo(dst, v)
		l.vars[b] = dst
	case *syntax.AssignStmt:
		v := l.expr(s.Value)
		l.copyTo(l.vars[l.info.Assigns[s]], v)
	case *syntax.ExprStmt:
		l.expr(s.X)
	case *syntax.WhileStmt:
		head, body, exit := l.newBlock(), l.newBlock(), l.newBlock()
		l.jump(head)
		l.setBlock(head)
		c := l.expr(s.Cond)
		l.terminate(Term{Op: "br", Args: []int{c}, Targets: []int{body, exit}})
		l.setBlock(body)
		l.block(s.Body)
		l.jump(head)
		l.setBlock(exit)
	case *syntax.ReturnStmt:
		var v int
		if s.Value != nil {
			v = l.expr(s.Value)
		} else {
			v = l.unit()
		}
		l.ret(l.info.Returns[s].Closure, v)
	}
}

// ret leaves the enclosing function, or the transaction body ci.
func (l *lowerer) ret(ci *types.ClosureInfo, v int) {
	if ci == nil {
		l.terminate(Term{Op: "ret", Args: []int{v}})
		return
	}
	for i := len(l.exits) - 1; i >= 0; i-- {
		if ex := l.exits[i]; ex.info == ci {
			l.copyTo(ex.result, v)
			l.jump(ex.block)
			return
		}
	}
	panic("return target not found")
}

// ---- expressions ----

func (l *lowerer) typeOf(e syntax.Expr) Ty { return l.ty(l.info.TypeOf(e)) }

func (l *lowerer) exprs(es []syntax.Expr) []int {
	out := make([]int, len(es))
	for i, e := range es {
		out[i] = l.expr(e)
	}
	return out
}

func (l *lowerer) expr(e syntax.Expr) int {
	switch e := e.(type) {
	case *syntax.IntLit:
		return l.intc(e.Value)
	case *syntax.BoolLit:
		return l.constant(Const{Kind: TBool, Bool: e.Value})
	case *syntax.StringLit:
		return l.constant(Const{Kind: TString, Str: e.Value})
	case *syntax.UnitLit:
		return l.unit()
	case *syntax.Ident:
		if ci := l.info.Calls[e]; ci != nil && ci.Kind == types.CallNone {
			return l.emitTo(l.typeOf(e), Instr{Op: "variant", Type: l.agg(types.Resolve(ci.Type)), Tag: 0})
		}
		return l.vars[l.info.Uses[e]]
	case *syntax.UnaryExpr:
		x := l.expr(e.X)
		op := "neg"
		if e.Op == syntax.Bang {
			op = "not"
		}
		return l.emitTo(l.typeOf(e), Instr{Op: "unop", Name: op, Args: []int{x}})
	case *syntax.BinaryExpr:
		return l.binary(e)
	case *syntax.Block:
		return l.block(e)
	case *syntax.IfExpr:
		return l.ifExpr(e)
	case *syntax.MatchExpr:
		return l.match(e)
	case *syntax.CallExpr:
		return l.call(e, l.info.Calls[e], e.Args)
	case *syntax.PathExpr:
		return l.call(e, l.info.Calls[e], nil)
	case *syntax.MethodCall:
		return l.method(e)
	case *syntax.FieldExpr:
		x := l.expr(e.X)
		return l.emitTo(l.typeOf(e), Instr{Op: "field", Type: l.agg(l.info.TypeOf(e.X)), Index: l.info.Fields[e], Args: []int{x}})
	case *syntax.StructLit:
		st := l.info.StructLits[e]
		vals := map[string]int{}
		for _, f := range e.Fields {
			vals[f.Name] = l.expr(f.Value)
		}
		args := make([]int, len(st.Fields))
		for i, f := range st.Fields {
			args[i] = vals[f.Name]
		}
		return l.emitTo(l.typeOf(e), Instr{Op: "struct", Type: l.agg(st), Args: args})
	case *syntax.TryExpr:
		return l.try(e)
	}
	panic(fmt.Sprintf("lower: unhandled %T", e))
}

var binops = map[syntax.TokenKind]string{
	syntax.Plus: "add", syntax.Minus: "sub", syntax.Star: "mul", syntax.Slash: "div", syntax.Percent: "rem",
	syntax.Lt: "lt", syntax.Le: "le", syntax.Gt: "gt", syntax.Ge: "ge", syntax.Eq: "eq", syntax.Ne: "ne",
}

func (l *lowerer) binary(e *syntax.BinaryExpr) int {
	t := l.typeOf(e)
	if e.Op == syntax.AmpAmp || e.Op == syntax.PipePipe {
		res := l.newLocal(t, "")
		x := l.expr(e.X)
		rhs, short, join := l.newBlock(), l.newBlock(), l.newBlock()
		if e.Op == syntax.AmpAmp {
			l.terminate(Term{Op: "br", Args: []int{x}, Targets: []int{rhs, short}})
		} else {
			l.terminate(Term{Op: "br", Args: []int{x}, Targets: []int{short, rhs}})
		}
		l.setBlock(short)
		l.copyTo(res, x)
		l.jump(join)
		l.setBlock(rhs)
		l.copyTo(res, l.expr(e.Y))
		l.jump(join)
		l.setBlock(join)
		return res
	}
	x := l.expr(e.X)
	y := l.expr(e.Y)
	op := binops[e.Op]
	if e.Op == syntax.Plus && t.Kind == TString {
		op = "concat"
	}
	return l.emitTo(t, Instr{Op: "binop", Name: op, Args: []int{x, y}})
}

func (l *lowerer) ifExpr(e *syntax.IfExpr) int {
	t := l.typeOf(e)
	res := l.newLocal(t, "")
	c := l.expr(e.Cond)
	then, els, join := l.newBlock(), l.newBlock(), l.newBlock()
	l.terminate(Term{Op: "br", Args: []int{c}, Targets: []int{then, els}})
	l.setBlock(then)
	v := l.block(e.Then)
	l.assignResult(res, v, e.Then)
	l.jump(join)
	l.setBlock(els)
	if e.Else != nil {
		v = l.expr(e.Else)
		l.assignResult(res, v, e.Else)
	} else {
		l.copyTo(res, l.unit())
	}
	l.jump(join)
	l.setBlock(join)
	return res
}

// assignResult copies a branch value into the join local unless the branch
// diverges (its value then has no meaningful type).
func (l *lowerer) assignResult(res, v int, branch syntax.Expr) {
	if types.Prune(l.info.Types[branch]) == types.Never {
		return
	}
	l.copyTo(res, v)
}

func (l *lowerer) call(e syntax.Expr, ci *types.CallInfo, argExprs []syntax.Expr) int {
	t := l.typeOf(e)
	args := l.exprs(argExprs)
	switch ci.Kind {
	case types.CallFunc:
		return l.emitTo(t, Instr{Op: "call", Name: ci.Func.Name, Args: args})
	case types.CallOk, types.CallErr, types.CallSome:
		tag := 0
		if ci.Kind != types.CallOk {
			tag = 1
		}
		return l.emitTo(t, Instr{Op: "variant", Type: l.agg(types.Resolve(ci.Type)), Tag: tag, Args: args})
	case types.CallNone:
		return l.emitTo(t, Instr{Op: "variant", Type: l.agg(types.Resolve(ci.Type)), Tag: 0})
	case types.CallVariant:
		return l.emitTo(t, Instr{Op: "variant", Type: l.agg(ci.Enum), Tag: ci.Tag, Args: args})
	case types.CallStatic:
		return l.host(t, ci.Builtin, args)
	}
	panic("lower: bad call")
}

func (l *lowerer) host(t Ty, b *types.Builtin, args []int) int {
	op := "host"
	if b.Async {
		op = "await"
	}
	return l.emitTo(t, Instr{Op: op, Name: b.Op, Args: args})
}

func (l *lowerer) method(e *syntax.MethodCall) int {
	mi := l.info.Methods[e]
	t := l.typeOf(e)
	switch mi.Kind {
	case types.MethodTransaction:
		return l.transaction(e, mi.Closure)
	case types.MethodBuiltin:
		args := append([]int{l.expr(e.Recv)}, l.exprs(e.Args)...)
		return l.host(t, mi.Builtin, args)
	}
	// compiler-implemented Result/Option methods
	recv := l.expr(e.Recv)
	rt := types.Resolve(mi.RecvTy)
	agg := l.agg(rt)
	tag := l.emitTo(Ty{Kind: TInt}, Instr{Op: "tag", Type: agg, Args: []int{recv}})
	_, isOpt := rt.(*types.OptionT)
	switch mi.Op {
	case "is_ok", "is_none":
		return l.emitTo(t, Instr{Op: "binop", Name: "eq", Args: []int{tag, l.intc(0)}})
	case "is_err", "is_some":
		return l.emitTo(t, Instr{Op: "binop", Name: "eq", Args: []int{tag, l.intc(1)}})
	case "unwrap_or":
		def := l.expr(e.Args[0])
		res := l.newLocal(t, "")
		okTag := int64(0)
		if isOpt {
			okTag = 1
		}
		isOk := l.emitTo(Ty{Kind: TBool}, Instr{Op: "binop", Name: "eq", Args: []int{tag, l.intc(okTag)}})
		yes, no, join := l.newBlock(), l.newBlock(), l.newBlock()
		l.terminate(Term{Op: "br", Args: []int{isOk}, Targets: []int{yes, no}})
		l.setBlock(yes)
		l.copyTo(res, l.emitTo(t, Instr{Op: "vfield", Type: agg, Tag: int(okTag), Index: 0, Args: []int{recv}}))
		l.jump(join)
		l.setBlock(no)
		l.copyTo(res, def)
		l.jump(join)
		l.setBlock(join)
		return res
	}
	panic("lower: bad generic method " + mi.Op)
}

// transaction lowers `db.transaction(|tx| body)`:
//
//	tx = await db.begin(db)
//	r  = body            // `?` and `return` inside jump to exit with r set
//	exit: await tx.finish(tx)   // rolls back if still open
//	value = r
func (l *lowerer) transaction(e *syntax.MethodCall, ci *types.ClosureInfo) int {
	db := l.expr(e.Recv)
	txTy := Ty{Kind: TExt, Ext: "Tx"}
	tx := l.emitTo(txTy, Instr{Op: "await", Name: OpTxBegin, Args: []int{db}})
	l.vars[ci.Tx] = tx
	rt := l.ty(ci.Result)
	ex := &closureExit{info: ci, result: l.newLocal(rt, "txresult"), block: l.newBlock()}
	l.exits = append(l.exits, ex)
	cl := e.Args[0].(*syntax.Closure)
	v := l.expr(cl.Body)
	if types.Prune(l.info.Types[cl.Body]) != types.Never {
		l.copyTo(ex.result, v)
	}
	l.jump(ex.block)
	l.exits = l.exits[:len(l.exits)-1]
	l.setBlock(ex.block)
	l.emitTo(Ty{Kind: TUnit}, Instr{Op: "await", Name: OpTxFinish, Args: []int{tx}})
	return ex.result
}

func (l *lowerer) try(e *syntax.TryExpr) int {
	x := l.expr(e.X)
	xt := types.Resolve(l.info.TypeOf(e.X))
	agg := l.agg(xt)
	ti := l.info.Tries[e]
	target := types.Resolve(ti.Target)
	tag := l.emitTo(Ty{Kind: TInt}, Instr{Op: "tag", Type: agg, Args: []int{x}})
	cont, fail := l.newBlock(), l.newBlock()
	var okTag int64
	if _, isOpt := xt.(*types.OptionT); isOpt {
		okTag = 1
	}
	isOk := l.emitTo(Ty{Kind: TBool}, Instr{Op: "binop", Name: "eq", Args: []int{tag, l.intc(okTag)}})
	l.terminate(Term{Op: "br", Args: []int{isOk}, Targets: []int{cont, fail}})

	l.setBlock(fail)
	var out int
	if _, isOpt := xt.(*types.OptionT); isOpt {
		out = l.emitTo(l.ty(target), Instr{Op: "variant", Type: l.agg(target), Tag: 0})
	} else {
		err := l.emitTo(l.ty(xt.(*types.ResultT).Err), Instr{Op: "vfield", Type: agg, Tag: 1, Index: 0, Args: []int{x}})
		out = l.emitTo(l.ty(target), Instr{Op: "variant", Type: l.agg(target), Tag: 1, Args: []int{err}})
	}
	l.ret(ti.Closure, out)

	l.setBlock(cont)
	return l.emitTo(l.typeOf(e), Instr{Op: "vfield", Type: agg, Tag: int(okTag), Index: 0, Args: []int{x}})
}

func (l *lowerer) match(e *syntax.MatchExpr) int {
	t := l.typeOf(e)
	res := l.newLocal(t, "")
	x := l.expr(e.X)
	join := l.newBlock()
	for _, arm := range e.Arms {
		next := l.newBlock()
		l.pattern(x, arm.Pat, next)
		v := l.expr(arm.Body)
		l.assignResult(res, v, arm.Body)
		l.jump(join)
		l.setBlock(next)
	}
	// Falling off the last arm is impossible for exhaustive matches.
	l.terminate(Term{Op: "unreachable"})
	l.setBlock(join)
	return res
}

// pattern emits code testing local x against p; on mismatch control goes
// to fail, otherwise it continues in the current block with p's variables
// bound.
func (l *lowerer) pattern(x int, p *syntax.Pattern, fail int) {
	pi := l.info.Patterns[p]
	test := func(cond int) {
		ok := l.newBlock()
		l.terminate(Term{Op: "br", Args: []int{cond}, Targets: []int{ok, fail}})
		l.setBlock(ok)
	}
	boolTy := Ty{Kind: TBool}
	switch pi.Kind {
	case types.PatWild:
	case types.PatBind:
		dst := l.newLocal(l.ty(pi.Bind.Type), pi.Bind.Name)
		l.copyTo(dst, x)
		l.vars[pi.Bind] = dst
	case types.PatInt:
		test(l.emitTo(boolTy, Instr{Op: "binop", Name: "eq", Args: []int{x, l.intc(*p.IntValue)}}))
	case types.PatString:
		s := l.constant(Const{Kind: TString, Str: *p.StrValue})
		test(l.emitTo(boolTy, Instr{Op: "binop", Name: "eq", Args: []int{x, s}}))
	case types.PatBool:
		if *p.BoolLit {
			test(x)
		} else {
			test(l.emitTo(boolTy, Instr{Op: "unop", Name: "not", Args: []int{x}}))
		}
	case types.PatCtor:
		st := types.Resolve(pi.Type)
		agg := l.agg(st)
		tag := l.emitTo(Ty{Kind: TInt}, Instr{Op: "tag", Type: agg, Args: []int{x}})
		test(l.emitTo(boolTy, Instr{Op: "binop", Name: "eq", Args: []int{tag, l.intc(int64(pi.Tag))}}))
		for i, sub := range pi.Args {
			spi := l.info.Patterns[sub]
			if spi.Kind == types.PatWild {
				continue
			}
			v := l.emitTo(l.ty(spi.Type), Instr{Op: "vfield", Type: agg, Tag: pi.Tag, Index: i, Args: []int{x}})
			l.pattern(v, sub, fail)
		}
	}
}

// prune removes unreachable blocks and renumbers the rest.
func prune(f *Func) {
	order := []int{}
	index := map[int]int{}
	var visit func(b int)
	visit = func(b int) {
		if _, ok := index[b]; ok {
			return
		}
		index[b] = len(order)
		order = append(order, b)
		for _, t := range f.Blocks[b].Term.Targets {
			visit(t)
		}
	}
	visit(0)
	blocks := make([]*Block, len(order))
	for i, b := range order {
		blk := f.Blocks[b]
		for j, t := range blk.Term.Targets {
			blk.Term.Targets[j] = index[t]
		}
		blocks[i] = blk
	}
	f.Blocks = blocks
}
