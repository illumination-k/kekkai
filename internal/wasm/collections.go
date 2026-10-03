package wasm

import (
	"fmt"
	"strings"

	"github.com/illumination-k/kekkai/internal/ir"
)

// Collections.
//
// Vec<T> is a struct { len: i32, data: (ref null (array (mut T))) } with
// amortised doubling, implemented entirely in wasm.
//
// Map<K, V> is a JS Map held as an externref and manipulated through host
// imports. Values are boxed into externrefs: numbers become JS numbers or
// BigInts, GC references go through extern.convert_any (and back through
// any.convert_extern + ref.cast).

const (
	gcArrayNewDefault = 0x07
	gcArrayGet        = 0x0b
	gcArraySet        = 0x0e
	gcArrayLen        = 0x0f
	gcArrayCopy       = 0x11
	gcAnyConvExtern   = 0x1a
	gcExternConvAny   = 0x1b

	opI32Add     = 0x6a
	opI32Sub     = 0x6b
	opI32Mul     = 0x6c
	opI32LtS     = 0x48
	opI32GeU     = 0x4f
	opI32WrapI64 = 0xa7
	opI64And     = 0x83
	opI64Or      = 0x84
	opI64Xor     = 0x85
	opI64Shl     = 0x86
	opI64ShrS    = 0x87
	opI64ShrU    = 0x88
	opI64LtU     = 0x54
)

var collectionImports = []struct {
	name string
	sig  sig
}{
	{"arr_new", sig{nil, []ValType{ExternRef}}},
	{"arr_len", sig{[]ValType{ExternRef}, []ValType{I32}}},
	{"arr_get", sig{[]ValType{ExternRef, I32}, []ValType{ExternRef}}},
	{"arr_push", sig{[]ValType{ExternRef, ExternRef}, nil}},
	{"box_i64", sig{[]ValType{I64}, []ValType{ExternRef}}},
	{"box_i32", sig{[]ValType{I32}, []ValType{ExternRef}}},
	{"map_new", sig{nil, []ValType{ExternRef}}},
	{"map_len", sig{[]ValType{ExternRef}, []ValType{I64}}},
	{"map_keys", sig{[]ValType{ExternRef}, []ValType{ExternRef}}},
	{"map_get", sig{[]ValType{ExternRef, ExternRef}, []ValType{ExternRef}}},
	{"map_insert", sig{[]ValType{ExternRef, ExternRef, ExternRef}, nil}},
	{"map_contains", sig{[]ValType{ExternRef, ExternRef}, []ValType{I32}}},
	{"map_remove", sig{[]ValType{ExternRef, ExternRef}, nil}},
}

func isCollectionOp(name string) bool {
	return strings.HasPrefix(name, "vec.") || strings.HasPrefix(name, "map.")
}

// hostParam is the import parameter type for an IR value passed to the JS
// host: Vec values are converted to JS arrays.
func (m *module) hostParam(t ir.Ty) ValType {
	if t.Kind == ir.TAgg && m.prog.Types[t.Agg].Coll == "vec" {
		return ExternRef
	}
	return m.val(t)
}

// toHost converts the wasm value on the stack (of IR type t) into its JS
// representation (used for host call arguments).
func (m *module) toHost(c *code, t ir.Ty) {
	if t.Kind == ir.TAgg && m.prog.Types[t.Agg].Coll == "vec" {
		c.call(m.vecToHostFunc(t.Agg))
	}
}

// box converts the wasm value on the stack into an externref for storage
// in a JS container; unbox is its inverse.
func (m *module) box(c *code, t ir.Ty) {
	switch t.Kind {
	case ir.TInt:
		c.call(m.imp("box_i64"))
	case ir.TBool, ir.TUnit:
		c.call(m.imp("box_i32"))
	case ir.TAgg:
		if m.prog.Types[t.Agg].Coll != "map" {
			c.gc(gcExternConvAny)
		}
	}
}

func (m *module) unbox(c *code, t ir.Ty) {
	switch t.Kind {
	case ir.TInt:
		c.call(m.imp("to_i64"))
	case ir.TBool, ir.TUnit:
		c.call(m.imp("to_i32"))
	case ir.TAgg:
		if m.prog.Types[t.Agg].Coll != "map" {
			c.gc(gcAnyConvExtern)
			c.castNull(m.aggType[t.Agg])
		}
	}
}

func (m *module) helper(name string, s sig, locals []ValType, gen func(c *code)) int {
	if i, ok := m.funcIdx[name]; ok {
		return i
	}
	i := m.reserve(name, s)
	d := m.def(name)
	d.locals = locals
	gen(&d.body)
	d.body.op(opEnd)
	d.built = true
	return i
}

// vecToHostFunc: Vec<T> -> JS array.
func (m *module) vecToHostFunc(agg int) int {
	vt, at := m.aggType[agg], m.vecArr[agg]
	elem := *m.prog.Types[agg].Elem
	// params: 0 vec; locals: 1 arr(ext), 2 i(i32)
	return m.helper(fmt.Sprintf("$vec_to_host%d", agg), sig{[]ValType{RefNull(vt)}, []ValType{ExternRef}}, []ValType{ExternRef, I32}, func(c *code) {
		c.call(m.imp("arr_new"))
		c.set(1)
		c.op(opBlock)
		c.byte(blockVoid)
		c.op(opLoop)
		c.byte(blockVoid)
		c.get(2)
		c.get(0)
		c.structGet(vt, 0)
		c.op(opI32GeU)
		c.byte(opBrIf)
		c.u32(1)
		c.get(1)
		c.get(0)
		c.structGet(vt, 1)
		c.get(2)
		c.gc(gcArrayGet)
		c.u32(uint32(at))
		m.toHost(c, elem)
		m.boxHostElem(c, elem)
		c.call(m.imp("arr_push"))
		c.get(2)
		c.i32(1)
		c.op(opI32Add)
		c.set(2)
		c.br(0)
		c.op(opEnd)
		c.op(opEnd)
		c.get(1)
	})
}

// boxHostElem converts a value to the externref that JS sees in an array.
func (m *module) boxHostElem(c *code, t ir.Ty) {
	switch t.Kind {
	case ir.TInt:
		c.call(m.imp("box_i64"))
	case ir.TBool, ir.TUnit:
		c.call(m.imp("box_i32"))
	case ir.TAgg:
		switch m.prog.Types[t.Agg].Coll {
		case "map", "vec": // already converted to JS values
		default:
			c.gc(gcExternConvAny)
		}
	}
}

// vecFromHostFunc: JS array -> Vec<T> (elements lifted from JS values).
func (m *module) vecFromHostFunc(agg int) int {
	vt, at := m.aggType[agg], m.vecArr[agg]
	elem := *m.prog.Types[agg].Elem
	// params: 0 arr; locals: 1 n, 2 data, 3 i
	return m.helper(fmt.Sprintf("$vec_from_host%d", agg), sig{[]ValType{ExternRef}, []ValType{RefNull(vt)}}, []ValType{I32, RefNull(at), I32}, func(c *code) {
		c.get(0)
		c.call(m.imp("arr_len"))
		c.set(1)
		c.get(1)
		c.gc(gcArrayNewDefault)
		c.u32(uint32(at))
		c.set(2)
		c.op(opBlock)
		c.byte(blockVoid)
		c.op(opLoop)
		c.byte(blockVoid)
		c.get(3)
		c.get(1)
		c.op(opI32GeU)
		c.byte(opBrIf)
		c.u32(1)
		c.get(2)
		c.get(3)
		c.get(0)
		c.get(3)
		c.call(m.imp("arr_get"))
		m.liftTo(c, elem)
		c.gc(gcArraySet)
		c.u32(uint32(at))
		c.get(3)
		c.i32(1)
		c.op(opI32Add)
		c.set(3)
		c.br(0)
		c.op(opEnd)
		c.op(opEnd)
		c.get(1)
		c.get(2)
		c.structNew(vt)
	})
}

// option builds Option values for the aggregate optAgg.
func (m *module) someNone(c *code, optAgg int, some bool, value func()) {
	td := m.prog.Types[optAgg]
	if some {
		c.i32(1)
		value()
	} else {
		c.i32(0)
		c.defaultValue(m.val(td.Variants[1][0]))
	}
	c.structNew(m.aggType[optAgg])
}

// collOp emits a collection operation (the arguments are not yet pushed).
func (g *fnGen) collOp(in ir.Instr) {
	c := g.c
	m := g.m
	arg := func(k int) { c.get(g.local[in.Args[k]]) }
	td := m.prog.Types[in.Type]
	dstTy := g.f.Locals[in.Dst]
	if td.Coll == "map" {
		key := func() { arg(1); m.box(c, *td.Key) }
		switch in.Name {
		case "map.new":
			c.call(m.imp("map_new"))
		case "map.insert":
			arg(0)
			key()
			arg(2)
			m.box(c, *td.Elem)
			c.call(m.imp("map_insert"))
			c.i32(0)
		case "map.get":
			// returns null when absent
			opt := dstTy.Agg
			fn := m.helper(fmt.Sprintf("$map_get%d_%d", in.Type, opt),
				sig{[]ValType{ExternRef, ExternRef}, []ValType{RefNull(m.aggType[opt])}}, []ValType{ExternRef}, func(h *code) {
					h.get(0)
					h.get(1)
					h.call(m.imp("map_get"))
					h.set(2)
					h.get(2)
					h.call(m.imp("is_null"))
					h.op(opIf)
					h.valtype(RefNull(m.aggType[opt]))
					m.someNone(h, opt, false, nil)
					h.op(opElse)
					m.someNone(h, opt, true, func() { h.get(2); m.unbox(h, *td.Elem) })
					h.op(opEnd)
				})
			arg(0)
			key()
			c.call(fn)
		case "map.contains":
			arg(0)
			key()
			c.call(m.imp("map_contains"))
		case "map.remove":
			arg(0)
			key()
			c.call(m.imp("map_remove"))
			c.i32(0)
		case "map.len":
			arg(0)
			c.call(m.imp("map_len"))
		case "map.keys":
			arg(0)
			c.call(m.imp("map_keys"))
			c.call(m.vecFromHostFunc(dstTy.Agg))
		default:
			panic("codegen: unknown map op " + in.Name)
		}
		return
	}
	vt, at := m.aggType[in.Type], m.vecArr[in.Type]
	elem := m.val(*td.Elem)
	vref := RefNull(vt)
	switch in.Name {
	case "vec.new":
		c.i32(0)
		c.i32(4)
		c.gc(gcArrayNewDefault)
		c.u32(uint32(at))
		c.structNew(vt)
	case "vec.len":
		arg(0)
		c.structGet(vt, 0)
		c.op(opI64ExtendU)
	case "vec.at":
		arg(0)
		c.structGet(vt, 1)
		arg(1)
		c.op(opI32WrapI64)
		c.gc(gcArrayGet)
		c.u32(uint32(at))
	case "vec.push":
		// params: 0 vec, 1 x; locals: 2 data, 3 len, 4 new data
		fn := m.helper(fmt.Sprintf("$vec_push%d", in.Type), sig{[]ValType{vref, elem}, nil}, []ValType{RefNull(at), I32, RefNull(at)}, func(h *code) {
			h.get(0)
			h.structGet(vt, 1)
			h.set(2)
			h.get(0)
			h.structGet(vt, 0)
			h.set(3)
			// grow when full
			h.get(3)
			h.get(2)
			h.gc(gcArrayLen)
			h.op(opI32GeU)
			h.op(opIf)
			h.byte(blockVoid)
			h.get(3)
			h.i32(2)
			h.op(opI32Mul)
			h.i32(4)
			h.op(opI32Add)
			h.gc(gcArrayNewDefault)
			h.u32(uint32(at))
			h.set(4)
			h.get(4)
			h.i32(0)
			h.get(2)
			h.i32(0)
			h.get(3)
			h.gc(gcArrayCopy)
			h.u32(uint32(at))
			h.u32(uint32(at))
			h.get(0)
			h.get(4)
			h.structSet(vt, 1)
			h.get(4)
			h.set(2)
			h.op(opEnd)
			h.get(2)
			h.get(3)
			h.get(1)
			h.gc(gcArraySet)
			h.u32(uint32(at))
			h.get(0)
			h.get(3)
			h.i32(1)
			h.op(opI32Add)
			h.structSet(vt, 0)
		})
		arg(0)
		arg(1)
		c.call(fn)
		c.i32(0)
	case "vec.get":
		opt := dstTy.Agg
		// params: 0 vec, 1 i (i64)
		fn := m.helper(fmt.Sprintf("$vec_get%d", in.Type), sig{[]ValType{vref, I64}, []ValType{RefNull(m.aggType[opt])}}, nil, func(h *code) {
			h.get(1)
			h.get(0)
			h.structGet(vt, 0)
			h.op(opI64ExtendU)
			h.op(opI64LtU) // unsigned: negative indices are out of range
			h.op(opIf)
			h.valtype(RefNull(m.aggType[opt]))
			m.someNone(h, opt, true, func() {
				h.get(0)
				h.structGet(vt, 1)
				h.get(1)
				h.op(opI32WrapI64)
				h.gc(gcArrayGet)
				h.u32(uint32(at))
			})
			h.op(opElse)
			m.someNone(h, opt, false, nil)
			h.op(opEnd)
		})
		arg(0)
		arg(1)
		c.call(fn)
	case "vec.set":
		fn := m.helper(fmt.Sprintf("$vec_set%d", in.Type), sig{[]ValType{vref, I64, elem}, []ValType{I32}}, nil, func(h *code) {
			h.get(1)
			h.get(0)
			h.structGet(vt, 0)
			h.op(opI64ExtendU)
			h.op(opI64LtU)
			h.op(opIf)
			h.valtype(I32)
			h.get(0)
			h.structGet(vt, 1)
			h.get(1)
			h.op(opI32WrapI64)
			h.get(2)
			h.gc(gcArraySet)
			h.u32(uint32(at))
			h.i32(1)
			h.op(opElse)
			h.i32(0)
			h.op(opEnd)
		})
		arg(0)
		arg(1)
		arg(2)
		c.call(fn)
	case "vec.pop":
		opt := dstTy.Agg
		fn := m.helper(fmt.Sprintf("$vec_pop%d", in.Type), sig{[]ValType{vref}, []ValType{RefNull(m.aggType[opt])}}, []ValType{I32}, func(h *code) {
			h.get(0)
			h.structGet(vt, 0)
			h.set(1)
			h.get(1)
			h.op(opI32Eqz)
			h.op(opIf)
			h.valtype(RefNull(m.aggType[opt]))
			m.someNone(h, opt, false, nil)
			h.op(opElse)
			h.get(0)
			h.get(1)
			h.i32(1)
			h.op(opI32Sub)
			h.structSet(vt, 0)
			m.someNone(h, opt, true, func() {
				h.get(0)
				h.structGet(vt, 1)
				h.get(1)
				h.i32(1)
				h.op(opI32Sub)
				h.gc(gcArrayGet)
				h.u32(uint32(at))
			})
			h.op(opEnd)
		})
		arg(0)
		c.call(fn)
	case "vec.join":
		// params: 0 vec, 1 sep; locals: 2 acc, 3 i
		fn := m.helper(fmt.Sprintf("$vec_join%d", in.Type), sig{[]ValType{vref, ExternRef}, []ValType{ExternRef}}, []ValType{ExternRef, I32}, func(h *code) {
			h.i32(int32(m.lit("")))
			h.call(m.imp("lit"))
			h.set(2)
			h.op(opBlock)
			h.byte(blockVoid)
			h.op(opLoop)
			h.byte(blockVoid)
			h.get(3)
			h.get(0)
			h.structGet(vt, 0)
			h.op(opI32GeU)
			h.byte(opBrIf)
			h.u32(1)
			h.get(3)
			h.op(opIf)
			h.byte(blockVoid)
			h.get(2)
			h.get(1)
			h.call(m.imp("str_concat"))
			h.set(2)
			h.op(opEnd)
			h.get(2)
			h.get(0)
			h.structGet(vt, 1)
			h.get(3)
			h.gc(gcArrayGet)
			h.u32(uint32(at))
			h.call(m.imp("str_concat"))
			h.set(2)
			h.get(3)
			h.i32(1)
			h.op(opI32Add)
			h.set(3)
			h.br(0)
			h.op(opEnd)
			h.op(opEnd)
			h.get(2)
		})
		arg(0)
		arg(1)
		c.call(fn)
	default:
		panic("codegen: unknown vec op " + in.Name)
	}
}

func isInlineIntOp(name string) bool {
	switch name {
	case "int.bit_and", "int.bit_or", "int.bit_xor", "int.shl", "int.shr", "int.ushr":
		return true
	}
	return false
}

// intOp emits Int bit operations inline; it reports false for other ops.
func (g *fnGen) intOp(in ir.Instr) bool {
	ops := map[string]byte{
		"int.bit_and": opI64And, "int.bit_or": opI64Or, "int.bit_xor": opI64Xor,
		"int.shl": opI64Shl, "int.shr": opI64ShrS, "int.ushr": opI64ShrU,
	}
	o, ok := ops[in.Name]
	if !ok {
		return false
	}
	g.c.get(g.local[in.Args[0]])
	g.c.get(g.local[in.Args[1]])
	g.c.op(o)
	return true
}
