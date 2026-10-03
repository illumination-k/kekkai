package wasm

import (
	"fmt"
	"sort"
	"strings"

	"github.com/illumination-k/kekkai/internal/ir"
)

// Compilation scheme
//
// Every IR function body is compiled as a dispatcher: a `loop` around a
// `br_table` over "segments" (basic blocks, further split at suspension
// points). Control transfers set a state local and branch to the loop.
//
// Functions that can reach an asynchronous host operation (I/O) are
// compiled into resumable state machines instead of relying on JSPI: such
// a function f becomes
//
//	f$new(params) -> frame      allocate a frame struct holding all locals
//	f$step(frame) -> i32        run until done (0) or suspended (1)
//
// On suspension every local is saved into the frame and control returns to
// the JS glue, which awaits the pending promise and calls the top-level
// step again. Calls between asynchronous functions keep the callee frame in
// the caller's `child` slot, so resuming re-enters the whole chain.
// Synchronous (pure or sync-capability) functions compile to plain wasm
// functions.

// Result of compilation.
type Output struct {
	Wasm    []byte
	Strings []string // string literal table, indexed by kek.lit(i)
	Imports []Import // host imports required from the "kek" module
}

type Import struct {
	Name   string
	Params []string // "i32", "i64", "externref"
	Result string   // "" when none
	Async  bool     // starts an asynchronous operation; result via kek.take()
}

type fieldType struct {
	t   ValType
	mut bool
}

type sig struct {
	params, results []ValType
}

func (s sig) key() string {
	var b buf
	b.u32(uint32(len(s.params)))
	for _, p := range s.params {
		b.valtype(p)
	}
	b.u32(uint32(len(s.results)))
	for _, r := range s.results {
		b.valtype(r)
	}
	return string(b)
}

type importFn struct {
	name  string
	sig   sig
	async bool
}

type defFunc struct {
	name   string
	sig    sig
	locals []ValType // excluding params
	body   code
	export string
	built  bool
}

type module struct {
	prog *ir.Program

	structs  [][]fieldType
	aggType  []int     // IR aggregate index -> wasm type index
	aggSlots [][][]int // variant aggregates: [tag][field] -> struct field index
	frame    map[string]int

	imports   []importFn
	importIdx map[string]int

	funcs   []*defFunc
	funcIdx map[string]int

	strings   []string
	stringIdx map[string]int

	lifts map[int]int // aggregate index -> lift function index
}

// Compile translates an IR program into a WasmGC module.
func Compile(p *ir.Program) (*Output, error) {
	m := &module{
		prog: p, frame: map[string]int{}, importIdx: map[string]int{},
		funcIdx: map[string]int{}, stringIdx: map[string]int{}, lifts: map[int]int{},
	}
	m.layoutTypes()
	m.collectImports()
	for _, f := range p.Funcs {
		if f.Async {
			m.reserve(f.Name+"$new", m.newSig(f))
			m.reserve(f.Name+"$step", sig{[]ValType{RefNull(m.frame[f.Name])}, []ValType{I32}})
		} else {
			m.reserve(f.Name, m.funcSig(f))
		}
	}
	for _, f := range p.Funcs {
		if f.Async {
			m.genNew(f)
			m.genStep(f)
		} else {
			m.genSync(f)
		}
	}
	if p.Handler != "" {
		if err := m.genHandlerExports(); err != nil {
			return nil, err
		}
	}
	m.genPureExports()
	out := &Output{Wasm: m.encode(), Strings: m.strings}
	for _, im := range m.imports {
		x := Import{Name: im.name, Async: im.async}
		for _, p := range im.sig.params {
			x.Params = append(x.Params, valName(p))
		}
		if len(im.sig.results) > 0 {
			x.Result = valName(im.sig.results[0])
		}
		out.Imports = append(out.Imports, x)
	}
	return out, nil
}

func valName(t ValType) string {
	switch t.code {
	case 0x7f:
		return "i32"
	case 0x7e:
		return "i64"
	case 0x6f:
		return "externref"
	}
	return "ref"
}

// ---- types ----

func (m *module) val(t ir.Ty) ValType {
	switch t.Kind {
	case ir.TUnit, ir.TBool:
		return I32
	case ir.TInt:
		return I64
	case ir.TString, ir.TExt:
		return ExternRef
	case ir.TAgg:
		return RefNull(m.aggType[t.Agg])
	}
	panic("bad type")
}

func (m *module) layoutTypes() {
	p := m.prog
	m.aggType = make([]int, len(p.Types))
	m.aggSlots = make([][][]int, len(p.Types))
	for i := range p.Types {
		m.aggType[i] = i
	}
	for i, td := range p.Types {
		var fields []fieldType
		if !td.IsVariant {
			for _, f := range td.Fields {
				fields = append(fields, fieldType{t: m.val(f)})
			}
		} else {
			fields = append(fields, fieldType{t: I32}) // tag
			m.aggSlots[i] = make([][]int, len(td.Variants))
			for tag, vs := range td.Variants {
				for _, f := range vs {
					m.aggSlots[i][tag] = append(m.aggSlots[i][tag], len(fields))
					fields = append(fields, fieldType{t: m.val(f)})
				}
			}
		}
		m.structs = append(m.structs, fields)
	}
	for _, f := range p.Funcs {
		if !f.Async {
			continue
		}
		fields := []fieldType{{I32, true}, {StructRef, true}, {m.val(f.Result), true}}
		for _, l := range f.Locals {
			fields = append(fields, fieldType{m.val(l), true})
		}
		m.frame[f.Name] = len(m.structs)
		m.structs = append(m.structs, fields)
	}
}

const (
	frameState = 0
	frameChild = 1
	frameRet   = 2
	frameLocal = 3
)

func (m *module) funcSig(f *ir.Func) sig {
	s := sig{results: []ValType{m.val(f.Result)}}
	for i := 0; i < f.NParams; i++ {
		s.params = append(s.params, m.val(f.Locals[i]))
	}
	return s
}

func (m *module) newSig(f *ir.Func) sig {
	s := m.funcSig(f)
	s.results = []ValType{RefNull(m.frame[f.Name])}
	return s
}

// ---- imports and functions ----

var helperImports = []struct {
	name string
	sig  sig
}{
	{"lit", sig{[]ValType{I32}, []ValType{ExternRef}}},
	{"take", sig{nil, []ValType{ExternRef}}},
	{"is_null", sig{[]ValType{ExternRef}, []ValType{I32}}},
	{"to_i64", sig{[]ValType{ExternRef}, []ValType{I64}}},
	{"to_i32", sig{[]ValType{ExternRef}, []ValType{I32}}},
	{"res_tag", sig{[]ValType{ExternRef}, []ValType{I32}}},
	{"res_val", sig{[]ValType{ExternRef}, []ValType{ExternRef}}},
	{"res_err", sig{[]ValType{ExternRef}, []ValType{ExternRef}}},
	{"str_eq", sig{[]ValType{ExternRef, ExternRef}, []ValType{I32}}},
	{"str_concat", sig{[]ValType{ExternRef, ExternRef}, []ValType{ExternRef}}},
}

// hostResult maps an IR result type to the import's wasm result type:
// aggregates (Option/Result) cross the boundary as JS values and are
// lifted in wasm; unit has no result.
func (m *module) hostResult(t ir.Ty) []ValType {
	switch t.Kind {
	case ir.TUnit:
		return nil
	case ir.TAgg:
		return []ValType{ExternRef}
	}
	return []ValType{m.val(t)}
}

func (m *module) addImport(name string, s sig, async bool) {
	if i, ok := m.importIdx[name]; ok {
		if m.imports[i].sig.key() != s.key() {
			panic(fmt.Sprintf("inconsistent signature for host operation %s", name))
		}
		return
	}
	m.importIdx[name] = len(m.imports)
	m.imports = append(m.imports, importFn{name: name, sig: s, async: async})
}

func (m *module) collectImports() {
	for _, h := range helperImports {
		m.addImport(h.name, h.sig, false)
	}
	for _, f := range m.prog.Funcs {
		for _, b := range f.Blocks {
			for _, in := range b.Instrs {
				if in.Op != "host" && in.Op != "await" {
					continue
				}
				s := sig{}
				for _, a := range in.Args {
					s.params = append(s.params, m.val(f.Locals[a]))
				}
				if in.Op == "host" {
					s.results = m.hostResult(f.Locals[in.Dst])
				}
				m.addImport(in.Name, s, in.Op == "await")
			}
		}
	}
}

func (m *module) reserve(name string, s sig) int {
	if i, ok := m.funcIdx[name]; ok {
		return i
	}
	i := len(m.imports) + len(m.funcs)
	m.funcIdx[name] = i
	m.funcs = append(m.funcs, &defFunc{name: name, sig: s})
	return i
}

func (m *module) def(name string) *defFunc {
	return m.funcs[m.funcIdx[name]-len(m.imports)]
}

func (m *module) imp(name string) int { return m.importIdx[name] }

func (m *module) lit(s string) int {
	if i, ok := m.stringIdx[s]; ok {
		return i
	}
	m.stringIdx[s] = len(m.strings)
	m.strings = append(m.strings, s)
	return len(m.strings) - 1
}

// ---- helpers: total division ----

// div implements truncating division with x/0 = 0 and MIN/-1 = MIN.
func (m *module) divFunc() int {
	name := "$div"
	if i, ok := m.funcIdx[name]; ok {
		return i
	}
	i := m.reserve(name, sig{[]ValType{I64, I64}, []ValType{I64}})
	f := m.def(name)
	c := &f.body
	// if y == 0 { return 0 }
	c.get(1)
	c.op(opI64Eqz)
	c.op(opIf)
	c.byte(blockVoid)
	c.i64(0)
	c.op(opReturn)
	c.op(opEnd)
	// if y == -1 { return 0 - x }  (wrapping; avoids the MIN/-1 trap)
	c.get(1)
	c.i64(-1)
	c.op(opI64Eq)
	c.op(opIf)
	c.byte(blockVoid)
	c.i64(0)
	c.get(0)
	c.op(opI64Sub)
	c.op(opReturn)
	c.op(opEnd)
	c.get(0)
	c.get(1)
	c.op(opI64DivS)
	c.op(opEnd)
	f.built = true
	return i
}

// rem implements x rem 0 = x (i64.rem_s does not trap on MIN rem -1).
func (m *module) remFunc() int {
	name := "$rem"
	if i, ok := m.funcIdx[name]; ok {
		return i
	}
	i := m.reserve(name, sig{[]ValType{I64, I64}, []ValType{I64}})
	f := m.def(name)
	c := &f.body
	c.get(1)
	c.op(opI64Eqz)
	c.op(opIf)
	c.byte(blockVoid)
	c.get(0)
	c.op(opReturn)
	c.op(opEnd)
	c.get(0)
	c.get(1)
	c.op(opI64RemS)
	c.op(opEnd)
	f.built = true
	return i
}

// ---- lifting host values into wasm values ----

// liftTo emits code converting the externref on the stack to type t.
func (m *module) liftTo(c *code, t ir.Ty) {
	switch t.Kind {
	case ir.TUnit:
		c.op(opDrop)
		c.i32(0)
	case ir.TInt:
		c.call(m.imp("to_i64"))
	case ir.TBool:
		c.call(m.imp("to_i32"))
	case ir.TString, ir.TExt:
	case ir.TAgg:
		c.call(m.liftFunc(t.Agg))
	}
}

func (m *module) liftFunc(agg int) int {
	if i, ok := m.lifts[agg]; ok {
		return i
	}
	name := fmt.Sprintf("$lift%d", agg)
	result := RefNull(m.aggType[agg])
	i := m.reserve(name, sig{[]ValType{ExternRef}, []ValType{result}})
	m.lifts[agg] = i
	f := m.def(name)
	td := m.prog.Types[agg]
	c := &code{}
	// struct.new with only the given tag's fields filled
	build := func(tag int, field func()) {
		c.i32(int32(tag))
		for t2, vs := range td.Variants {
			for k := range vs {
				if t2 == tag {
					field()
				} else {
					c.defaultValue(m.val(vs[k]))
				}
			}
		}
		c.structNew(m.aggType[agg])
	}
	if len(td.Variants) == 2 && len(td.Variants[0]) == 0 && len(td.Variants[1]) == 1 {
		// Option: null => None
		c.get(0)
		c.call(m.imp("is_null"))
		c.op(opIf)
		c.valtype(result)
		build(0, nil)
		c.op(opElse)
		build(1, func() { c.get(0); m.liftTo(c, td.Variants[1][0]) })
		c.op(opEnd)
	} else if len(td.Variants) == 2 && len(td.Variants[0]) == 1 && len(td.Variants[1]) == 1 {
		// Result: {ok, value} / {ok: false, error}
		c.get(0)
		c.call(m.imp("res_tag"))
		c.op(opI32Eqz)
		c.op(opIf)
		c.valtype(result)
		build(0, func() { c.get(0); c.call(m.imp("res_val")); m.liftTo(c, td.Variants[0][0]) })
		c.op(opElse)
		build(1, func() { c.get(0); c.call(m.imp("res_err")); m.liftTo(c, td.Variants[1][0]) })
		c.op(opEnd)
	} else {
		panic(fmt.Sprintf("cannot lift host value into %s", td.Name))
	}
	c.op(opEnd)
	f.body = *c
	f.built = true
	return i
}

// ---- function bodies ----

type segInfo struct {
	block int
	start int // instruction index where the segment starts
	// resume marks a segment that begins by completing a suspension of the
	// instruction at index start (await: take the result; async call: step
	// the child).
	resume bool
}

type fnGen struct {
	m     *module
	f     *ir.Func
	async bool
	c     *code
	// wasm local indices
	local    []int
	state    int
	child    int
	frameL   int // the frame parameter (async)
	frameT   int
	segs     []segInfo
	blockSeg []int // first segment of each block
	nseg     int
	cur      int // current segment
}

func (g *fnGen) isSuspend(in ir.Instr) bool {
	if !g.async {
		return false
	}
	if in.Op == "await" {
		return true
	}
	if in.Op == "call" {
		callee := g.m.prog.Func(in.Name)
		return callee != nil && callee.Async
	}
	return false
}

func (g *fnGen) planSegments() {
	g.blockSeg = make([]int, len(g.f.Blocks))
	for bi, b := range g.f.Blocks {
		g.blockSeg[bi] = len(g.segs)
		g.segs = append(g.segs, segInfo{block: bi, start: 0})
		for ii, in := range b.Instrs {
			if g.isSuspend(in) {
				g.segs = append(g.segs, segInfo{block: bi, start: ii, resume: true})
			}
		}
	}
	g.nseg = len(g.segs)
}

// loopDepth is the br depth of the dispatcher loop from the current segment.
func (g *fnGen) loopDepth() int { return g.nseg - g.cur }

func (g *fnGen) gotoSeg(s int) {
	g.c.i32(int32(s))
	g.c.set(g.state)
	g.c.br(g.loopDepth())
}

func (g *fnGen) saveAll() {
	c := g.c
	for i, l := range g.local {
		c.get(g.frameL)
		c.get(l)
		c.structSet(g.frameT, frameLocal+i)
	}
	c.get(g.frameL)
	c.get(g.child)
	c.structSet(g.frameT, frameChild)
}

func (g *fnGen) suspend(resumeSeg int) {
	c := g.c
	g.saveAll()
	c.get(g.frameL)
	c.i32(int32(resumeSeg))
	c.structSet(g.frameT, frameState)
	c.i32(1)
	c.op(opReturn)
}

func (g *fnGen) body() {
	c := g.c
	g.planSegments()
	c.op(opLoop)
	c.byte(blockVoid)
	for i := 0; i <= g.nseg; i++ {
		c.op(opBlock)
		c.byte(blockVoid)
	}
	c.get(g.state)
	c.op(opBrTable)
	c.u32(uint32(g.nseg))
	for i := 0; i < g.nseg; i++ {
		c.u32(uint32(i))
	}
	c.u32(uint32(g.nseg))
	c.op(opEnd) // B_0
	for s := 0; s < g.nseg; s++ {
		g.cur = s
		g.segment(s)
		c.op(opEnd) // B_{s+1} (or the default block after the last segment)
	}
	c.op(opUnreachable) // default: invalid state
	c.op(opEnd)         // loop
	c.op(opUnreachable)
	c.op(opEnd) // function
}

func (g *fnGen) segment(s int) {
	seg := g.segs[s]
	b := g.f.Blocks[seg.block]
	i := seg.start
	if seg.resume {
		g.resume(b.Instrs[i], s)
		i++
	}
	for ; i < len(b.Instrs); i++ {
		in := b.Instrs[i]
		if g.isSuspend(in) {
			g.startSuspend(in, s+1)
			return
		}
		g.instr(in)
	}
	g.term(b.Term)
}

func (g *fnGen) startSuspend(in ir.Instr, resumeSeg int) {
	c := g.c
	for _, a := range in.Args {
		c.get(g.local[a])
	}
	if in.Op == "await" {
		c.call(g.m.imp(in.Name))
		g.suspend(resumeSeg)
		return
	}
	// async call: allocate the callee frame, then step it in the resume segment
	c.call(g.m.funcIdx[in.Name+"$new"])
	c.set(g.child)
	g.gotoSeg(resumeSeg)
}

func (g *fnGen) resume(in ir.Instr, s int) {
	c := g.c
	dstTy := g.f.Locals[in.Dst]
	if in.Op == "await" {
		c.call(g.m.imp("take"))
		g.m.liftTo(c, dstTy)
		c.set(g.local[in.Dst])
		return
	}
	ft := g.m.frame[in.Name]
	c.get(g.child)
	c.castNull(ft)
	c.call(g.m.funcIdx[in.Name+"$step"])
	c.op(opIf)
	c.byte(blockVoid)
	// still suspended: propagate (depth +1 inside the if is irrelevant: we return)
	g.suspend(s)
	c.op(opEnd)
	c.get(g.child)
	c.castNull(ft)
	c.structGet(ft, frameRet)
	c.set(g.local[in.Dst])
}

func (g *fnGen) instr(in ir.Instr) {
	c := g.c
	m := g.m
	dst := g.local[in.Dst]
	dstTy := g.f.Locals[in.Dst]
	arg := func(k int) { c.get(g.local[in.Args[k]]) }
	switch in.Op {
	case "const":
		switch in.Const.Kind {
		case ir.TInt:
			c.i64(in.Const.Int)
		case ir.TBool:
			if in.Const.Bool {
				c.i32(1)
			} else {
				c.i32(0)
			}
		case ir.TString:
			c.i32(int32(m.lit(in.Const.Str)))
			c.call(m.imp("lit"))
		default:
			c.i32(0)
		}
	case "copy":
		arg(0)
	case "unop":
		if in.Name == "neg" {
			c.i64(0)
			arg(0)
			c.op(opI64Sub)
		} else {
			arg(0)
			c.op(opI32Eqz)
		}
	case "binop":
		g.binop(in)
	case "call":
		for k := range in.Args {
			arg(k)
		}
		c.call(m.funcIdx[in.Name])
	case "host":
		for k := range in.Args {
			arg(k)
		}
		c.call(m.imp(in.Name))
		switch dstTy.Kind {
		case ir.TUnit:
			c.i32(0)
		case ir.TAgg:
			c.call(m.liftFunc(dstTy.Agg))
		}
	case "struct":
		for k := range in.Args {
			arg(k)
		}
		c.structNew(m.aggType[in.Type])
	case "field":
		arg(0)
		c.structGet(m.aggType[in.Type], in.Index)
	case "variant":
		td := m.prog.Types[in.Type]
		c.i32(int32(in.Tag))
		for tag, vs := range td.Variants {
			for k, ft := range vs {
				if tag == in.Tag {
					arg(k)
				} else {
					c.defaultValue(m.val(ft))
				}
			}
		}
		c.structNew(m.aggType[in.Type])
	case "tag":
		arg(0)
		c.structGet(m.aggType[in.Type], 0)
		c.op(opI64ExtendU)
	case "vfield":
		arg(0)
		c.structGet(m.aggType[in.Type], m.aggSlots[in.Type][in.Tag][in.Index])
	default:
		panic("codegen: unknown instruction " + in.Op)
	}
	c.set(dst)
}

func (g *fnGen) binop(in ir.Instr) {
	c := g.c
	m := g.m
	x, y := g.local[in.Args[0]], g.local[in.Args[1]]
	kind := g.f.Locals[in.Args[0]].Kind
	c.get(x)
	c.get(y)
	switch kind {
	case ir.TInt:
		switch in.Name {
		case "add":
			c.op(opI64Add)
		case "sub":
			c.op(opI64Sub)
		case "mul":
			c.op(opI64Mul)
		case "div":
			c.call(m.divFunc())
		case "rem":
			c.call(m.remFunc())
		case "lt":
			c.op(opI64LtS)
		case "le":
			c.op(opI64LeS)
		case "gt":
			c.op(opI64GtS)
		case "ge":
			c.op(opI64GeS)
		case "eq":
			c.op(opI64Eq)
		case "ne":
			c.op(opI64Ne)
		default:
			panic("codegen: bad int binop " + in.Name)
		}
	case ir.TBool, ir.TUnit:
		switch in.Name {
		case "eq":
			c.op(opI32Eq)
		case "ne":
			c.op(opI32Ne)
		default:
			panic("codegen: bad bool binop " + in.Name)
		}
	case ir.TString:
		switch in.Name {
		case "eq":
			c.call(m.imp("str_eq"))
		case "ne":
			c.call(m.imp("str_eq"))
			c.op(opI32Eqz)
		case "concat":
			c.call(m.imp("str_concat"))
		default:
			panic("codegen: bad string binop " + in.Name)
		}
	default:
		panic("codegen: binop on " + string(kind))
	}
}

func (g *fnGen) term(t ir.Term) {
	c := g.c
	switch t.Op {
	case "jump":
		g.gotoSeg(g.blockSeg[t.Targets[0]])
	case "br":
		c.i32(int32(g.blockSeg[t.Targets[0]]))
		c.i32(int32(g.blockSeg[t.Targets[1]]))
		c.get(g.local[t.Args[0]])
		c.op(opSelect)
		c.set(g.state)
		c.br(g.loopDepth())
	case "switch":
		// state = targets[tag] (default: last)
		n := len(t.Targets) - 1
		g.gotoSegDyn(t)
		_ = n
	case "ret":
		if g.async {
			c.get(g.frameL)
			c.get(g.local[t.Args[0]])
			c.structSet(g.frameT, frameRet)
			c.i32(0)
			c.op(opReturn)
		} else {
			c.get(g.local[t.Args[0]])
			c.op(opReturn)
		}
	default:
		c.op(opUnreachable)
	}
}

// gotoSegDyn compiles a switch terminator as a chain of comparisons.
func (g *fnGen) gotoSegDyn(t ir.Term) {
	c := g.c
	n := len(t.Targets) - 1
	c.i32(int32(g.blockSeg[t.Targets[n]]))
	c.set(g.state)
	for k := 0; k < n; k++ {
		c.i32(int32(g.blockSeg[t.Targets[k]]))
		c.get(g.state)
		c.get(g.local[t.Args[0]])
		c.i64(int64(k))
		c.op(opI64Eq)
		c.op(opSelect)
		c.set(g.state)
	}
	c.br(g.loopDepth())
}

func (m *module) newGen(f *ir.Func, async bool, nparams int) (*fnGen, *defFunc) {
	g := &fnGen{m: m, f: f, async: async, c: &code{}}
	var locals []ValType
	next := nparams
	alloc := func(t ValType) int {
		locals = append(locals, t)
		next++
		return next - 1
	}
	g.local = make([]int, len(f.Locals))
	for i, t := range f.Locals {
		if !async && i < f.NParams {
			g.local[i] = i
			continue
		}
		g.local[i] = alloc(m.val(t))
	}
	g.state = alloc(I32)
	g.child = alloc(StructRef)
	return g, &defFunc{locals: locals}
}

func (m *module) genSync(f *ir.Func) {
	g, tmp := m.newGen(f, false, f.NParams)
	g.body()
	d := m.def(f.Name)
	d.locals = tmp.locals
	d.body = *g.c
	d.built = true
}

func (m *module) genNew(f *ir.Func) {
	d := m.def(f.Name + "$new")
	c := &d.body
	c.i32(0) // state
	c.refNull(heapStruct)
	c.defaultValue(m.val(f.Result))
	for i, t := range f.Locals {
		if i < f.NParams {
			c.get(i)
		} else {
			c.defaultValue(m.val(t))
		}
	}
	c.structNew(m.frame[f.Name])
	c.op(opEnd)
	d.built = true
}

func (m *module) genStep(f *ir.Func) {
	g, tmp := m.newGen(f, true, 1)
	g.frameL = 0
	g.frameT = m.frame[f.Name]
	c := g.c
	// load the frame into locals
	for i, l := range g.local {
		c.get(0)
		c.structGet(g.frameT, frameLocal+i)
		c.set(l)
	}
	c.get(0)
	c.structGet(g.frameT, frameChild)
	c.set(g.child)
	c.get(0)
	c.structGet(g.frameT, frameState)
	c.set(g.state)
	g.body()
	d := m.def(f.Name + "$step")
	d.locals = tmp.locals
	d.body = *c
	d.built = true
}

// genHandlerExports exports handler_new / handler_step / handler_result
// using anyref so that frames are opaque to JS.
func (m *module) genHandlerExports() error {
	f := m.prog.Func(m.prog.Handler)
	if f == nil || !f.Async {
		return fmt.Errorf("handler %q must be compiled as async", m.prog.Handler)
	}
	ft := m.frame[f.Name]
	s := m.funcSig(f)
	s.results = []ValType{AnyRef}
	m.reserve("$handler_new", s)
	d := m.def("$handler_new")
	for i := 0; i < f.NParams; i++ {
		d.body.get(i)
	}
	d.body.call(m.funcIdx[f.Name+"$new"])
	d.body.op(opEnd)
	d.export, d.built = "handler_new", true

	m.reserve("$handler_step", sig{[]ValType{AnyRef}, []ValType{I32}})
	d = m.def("$handler_step")
	d.body.get(0)
	d.body.castNull(ft)
	d.body.call(m.funcIdx[f.Name+"$step"])
	d.body.op(opEnd)
	d.export, d.built = "handler_step", true

	m.reserve("$handler_result", sig{[]ValType{AnyRef}, []ValType{m.val(f.Result)}})
	d = m.def("$handler_result")
	d.body.get(0)
	d.body.castNull(ft)
	d.body.structGet(ft, frameRet)
	d.body.op(opEnd)
	d.export, d.built = "handler_result", true
	return nil
}

// genPureExports exports synchronous functions whose signature only uses
// Int/Bool/String (useful for tests and for calling pure code from JS).
func (m *module) genPureExports() {
	for _, f := range m.prog.Funcs {
		if f.Async || f.Name == m.prog.Handler {
			continue
		}
		ok := true
		for _, t := range append(append([]ir.Ty{}, f.Locals[:f.NParams]...), f.Result) {
			if t.Kind == ir.TAgg || t.Kind == ir.TExt {
				ok = false
			}
		}
		if ok {
			m.def(f.Name).export = "fn_" + f.Name
		}
	}
}

// ---- encoding ----

func (m *module) encode() []byte {
	// function types (after the rec group of struct types)
	var sigs []sig
	sigIdx := map[string]int{}
	typeOf := func(s sig) int {
		k := s.key()
		if i, ok := sigIdx[k]; ok {
			return i
		}
		i := len(m.structs) + len(sigs)
		sigIdx[k] = i
		sigs = append(sigs, s)
		return i
	}
	importTypes := make([]int, len(m.imports))
	for i, im := range m.imports {
		importTypes[i] = typeOf(im.sig)
	}
	funcTypes := make([]int, len(m.funcs))
	for i, f := range m.funcs {
		if !f.built {
			panic("function body not generated: " + f.name)
		}
		funcTypes[i] = typeOf(f.sig)
	}

	var out buf
	out.bytes([]byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00})

	var ts buf
	n := len(sigs)
	if len(m.structs) > 0 {
		n++
	}
	ts.u32(uint32(n))
	if len(m.structs) > 0 {
		ts.byte(0x4e)
		ts.u32(uint32(len(m.structs)))
		for _, st := range m.structs {
			ts.byte(0x5f)
			ts.u32(uint32(len(st)))
			for _, f := range st {
				ts.valtype(f.t)
				if f.mut {
					ts.byte(1)
				} else {
					ts.byte(0)
				}
			}
		}
	}
	for _, s := range sigs {
		ts.byte(0x60)
		ts.u32(uint32(len(s.params)))
		for _, p := range s.params {
			ts.valtype(p)
		}
		ts.u32(uint32(len(s.results)))
		for _, r := range s.results {
			ts.valtype(r)
		}
	}
	out.section(1, ts)

	var is buf
	is.u32(uint32(len(m.imports)))
	for i, im := range m.imports {
		is.name("kek")
		is.name(im.name)
		is.byte(0x00)
		is.u32(uint32(importTypes[i]))
	}
	out.section(2, is)

	var fs buf
	fs.u32(uint32(len(m.funcs)))
	for i := range m.funcs {
		fs.u32(uint32(funcTypes[i]))
	}
	out.section(3, fs)

	var es buf
	var exports []int
	for i, f := range m.funcs {
		if f.export != "" {
			exports = append(exports, i)
		}
	}
	sort.Slice(exports, func(a, b int) bool { return m.funcs[exports[a]].export < m.funcs[exports[b]].export })
	es.u32(uint32(len(exports)))
	for _, i := range exports {
		es.name(m.funcs[i].export)
		es.byte(0x00)
		es.u32(uint32(len(m.imports) + i))
	}
	out.section(7, es)

	var cs buf
	cs.u32(uint32(len(m.funcs)))
	for _, f := range m.funcs {
		var body buf
		body.u32(uint32(len(f.locals)))
		for _, l := range f.locals {
			body.u32(1)
			body.valtype(l)
		}
		body.bytes(f.body.buf)
		cs.u32(uint32(len(body)))
		cs.bytes(body)
	}
	out.section(10, cs)

	// name section (function names) for readable stack traces
	var names buf
	names.name("name")
	var sub buf
	sub.u32(uint32(len(m.imports) + len(m.funcs)))
	for i, im := range m.imports {
		sub.u32(uint32(i))
		sub.name("kek." + im.name)
	}
	for i, f := range m.funcs {
		sub.u32(uint32(len(m.imports) + i))
		sub.name(strings.TrimPrefix(f.name, "$"))
	}
	names.byte(1)
	names.u32(uint32(len(sub)))
	names.bytes(sub)
	out.section(0, names)
	return out
}
