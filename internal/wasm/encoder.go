// Package wasm generates WasmGC modules from Kekkai IR.
package wasm

// Low-level binary encoding helpers for the WebAssembly (GC) binary format.

type buf []byte

func (b *buf) byte(x byte)    { *b = append(*b, x) }
func (b *buf) bytes(x []byte) { *b = append(*b, x...) }

func (b *buf) u32(x uint32) {
	for {
		c := byte(x & 0x7f)
		x >>= 7
		if x != 0 {
			c |= 0x80
		}
		b.byte(c)
		if x == 0 {
			return
		}
	}
}

func (b *buf) s64(x int64) {
	for {
		c := byte(x & 0x7f)
		x >>= 7
		done := (x == 0 && c&0x40 == 0) || (x == -1 && c&0x40 != 0)
		if !done {
			c |= 0x80
		}
		b.byte(c)
		if done {
			return
		}
	}
}

func (b *buf) s32(x int32) { b.s64(int64(x)) }

func (b *buf) name(s string) {
	b.u32(uint32(len(s)))
	b.bytes([]byte(s))
}

// section appends a section with the given id and contents.
func (b *buf) section(id byte, contents buf) {
	b.byte(id)
	b.u32(uint32(len(contents)))
	b.bytes(contents)
}

// ---- value types ----

// ValType is an encoded value type.
type ValType struct {
	code byte  // 0x7f i32, 0x7e i64, 0x6f externref, 0x6e anyref, 0x6b structref, 0x63 ref null, 0x64 ref
	idx  int32 // type index for 0x63/0x64
}

var (
	I32       = ValType{code: 0x7f}
	I64       = ValType{code: 0x7e}
	ExternRef = ValType{code: 0x6f}
	AnyRef    = ValType{code: 0x6e}
	StructRef = ValType{code: 0x6b}
)

func RefNull(idx int) ValType { return ValType{code: 0x63, idx: int32(idx)} }

func (b *buf) valtype(t ValType) {
	b.byte(t.code)
	if t.code == 0x63 || t.code == 0x64 {
		b.s32(t.idx)
	}
}

// ---- opcodes ----

const (
	opUnreachable = 0x00
	opNop         = 0x01
	opBlock       = 0x02
	opLoop        = 0x03
	opIf          = 0x04
	opElse        = 0x05
	opEnd         = 0x0b
	opBr          = 0x0c
	opBrIf        = 0x0d
	opBrTable     = 0x0e
	opReturn      = 0x0f
	opCall        = 0x10
	opDrop        = 0x1a
	opSelect      = 0x1b
	opLocalGet    = 0x20
	opLocalSet    = 0x21
	opLocalTee    = 0x22
	opI32Const    = 0x41
	opI64Const    = 0x42
	opI32Eqz      = 0x45
	opI32Eq       = 0x46
	opI32Ne       = 0x47
	opI64Eqz      = 0x50
	opI64Eq       = 0x51
	opI64Ne       = 0x52
	opI64LtS      = 0x53
	opI64GtS      = 0x55
	opI64LeS      = 0x57
	opI64GeS      = 0x59
	opI32And      = 0x71
	opI64Add      = 0x7c
	opI64Sub      = 0x7d
	opI64Mul      = 0x7e
	opI64DivS     = 0x7f
	opI64RemS     = 0x81
	opI64ExtendU  = 0xad
	opRefNull     = 0xd0
	opRefIsNull   = 0xd1
	opGC          = 0xfb

	gcStructNew = 0x00
	gcStructGet = 0x02
	gcStructSet = 0x05
	gcRefCast   = 0x16 // ref.cast (ref ht)
	gcRefCastN  = 0x17 // ref.cast (ref null ht)

	blockVoid = 0x40

	heapExtern = 0x6f
	heapAny    = 0x6e
	heapStruct = 0x6b
	heapNone   = 0x71
)

// code is a function body under construction.
type code struct {
	buf
}

func (c *code) op(o byte)          { c.byte(o) }
func (c *code) i32(x int32)        { c.byte(opI32Const); c.s32(x) }
func (c *code) i64(x int64)        { c.byte(opI64Const); c.s64(x) }
func (c *code) get(l int)          { c.byte(opLocalGet); c.u32(uint32(l)) }
func (c *code) set(l int)          { c.byte(opLocalSet); c.u32(uint32(l)) }
func (c *code) call(f int)         { c.byte(opCall); c.u32(uint32(f)) }
func (c *code) br(depth int)       { c.byte(opBr); c.u32(uint32(depth)) }
func (c *code) gc(sub byte)        { c.byte(opGC); c.u32(uint32(sub)) }
func (c *code) structNew(t int)    { c.gc(gcStructNew); c.u32(uint32(t)) }
func (c *code) structGet(t, f int) { c.gc(gcStructGet); c.u32(uint32(t)); c.u32(uint32(f)) }
func (c *code) structSet(t, f int) { c.gc(gcStructSet); c.u32(uint32(t)); c.u32(uint32(f)) }
func (c *code) castNull(t int)     { c.gc(gcRefCastN); c.s32(int32(t)) }
func (c *code) refNull(heap byte)  { c.byte(opRefNull); c.byte(heap) }
func (c *code) refNullIdx(t int)   { c.byte(opRefNull); c.s32(int32(t)) }

// defaultValue pushes the default value of a value type.
func (c *code) defaultValue(t ValType) {
	switch t.code {
	case 0x7f:
		c.i32(0)
	case 0x7e:
		c.i64(0)
	case 0x6f:
		c.refNull(heapExtern)
	case 0x6e:
		c.refNull(heapAny)
	case 0x6b:
		c.refNull(heapStruct)
	case 0x63:
		c.refNullIdx(int(t.idx))
	}
}
