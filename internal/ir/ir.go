// Package ir defines the Kekkai intermediate representation: a small,
// first-order language of functions whose bodies are control-flow graphs
// over mutable locals. Source-level types are erased to representation
// types; capabilities become opaque handles. The IR has a JSON encoding
// that the Lean reference interpreter consumes for differential testing.
//
// Semantics notes (shared with the Lean interpreter):
//   - Int is 64-bit two's complement with wrapping arithmetic.
//   - div truncates toward zero; x div 0 = 0 and x rem 0 = x (total, no traps).
//   - Variant tags: Result Ok=0 Err=1; Option None=0 Some=1; enums in
//     declaration order.
package ir

import (
	"fmt"
	"strings"
)

// TyKind is a representation type kind.
type TyKind string

const (
	TUnit   TyKind = "unit"
	TBool   TyKind = "bool"
	TInt    TyKind = "int"
	TString TyKind = "string"
	TExt    TyKind = "ext" // host value: Request, Response, TxError, capabilities, ...
	TAgg    TyKind = "agg" // aggregate (struct or variant), see Program.Types
)

type Ty struct {
	Kind TyKind `json:"kind"`
	Agg  int    `json:"agg,omitempty"`
	Ext  string `json:"ext,omitempty"`
}

func (t Ty) String() string {
	switch t.Kind {
	case TAgg:
		return fmt.Sprintf("agg%d", t.Agg)
	case TExt:
		return "ext:" + t.Ext
	}
	return string(t.Kind)
}

// TypeDef is an aggregate layout.
type TypeDef struct {
	Name string `json:"name"`
	// Struct types have Fields; variant types have Variants; collections
	// have Coll set ("vec": Vec<Elem>, "map": Map<Key, Elem>).
	IsVariant bool   `json:"variant"`
	Fields    []Ty   `json:"fields,omitempty"`
	Variants  [][]Ty `json:"variants,omitempty"`
	Coll      string `json:"coll,omitempty"`
	Elem      *Ty    `json:"elem,omitempty"`
	Key       *Ty    `json:"key,omitempty"`
}

// Const is a literal.
type Const struct {
	Kind TyKind `json:"kind"`
	Int  int64  `json:"int,omitempty"`
	Bool bool   `json:"bool,omitempty"`
	Str  string `json:"str,omitempty"`
}

// Instr is one instruction. Every instruction writes Dst.
//
//	const    Dst = Const
//	copy     Dst = Args[0]
//	unop     Dst = Name(Args[0])            Name: neg, not
//	binop    Dst = Name(Args[0], Args[1])   Name: add sub mul div rem lt le gt ge eq ne concat
//	call     Dst = Name(Args...)            user function
//	host     Dst = Name(Args...)            builtin operation (sync)
//	await    Dst = Name(Args...)            builtin operation that suspends
//	struct   Dst = Type{Args...}
//	field    Dst = Args[0].Index            (Type)
//	setfield Args[0].Index := Args[1]; Dst = ()   (Type; structs are mutable references)
//	variant  Dst = Type.Tag(Args...)
//	tag      Dst = tag(Args[0])             (Type)
//	vfield   Dst = Args[0].Tag.Index        (Type)
//
// Collection operations are `host` instructions whose Name starts with
// "vec." or "map."; Type is the collection's aggregate. They are
// implemented by the backend (not the JS host):
//
//	vec.new vec.push vec.get vec.set vec.len vec.pop vec.join
//	vec.at (unchecked read used by `for`; index proven in range)
//	map.new map.insert map.get map.contains map.remove map.len map.keys
type Instr struct {
	Op    string `json:"op"`
	Dst   int    `json:"dst"`
	Args  []int  `json:"args,omitempty"`
	Name  string `json:"name,omitempty"`
	Type  int    `json:"type,omitempty"`
	Tag   int    `json:"tag,omitempty"`
	Index int    `json:"index,omitempty"`
	Const *Const `json:"const,omitempty"`
}

// Term ends a block.
//
//	jump        Targets[0]
//	br          if Args[0] then Targets[0] else Targets[1]
//	switch      on Args[0] (i32 tag): Targets[tag], last target is default
//	ret         return Args[0]
//	unreachable
type Term struct {
	Op      string `json:"op"`
	Args    []int  `json:"args,omitempty"`
	Targets []int  `json:"targets,omitempty"`
}

type Block struct {
	Instrs []Instr `json:"instrs"`
	Term   Term    `json:"term"`
}

type Func struct {
	Name    string   `json:"name"`
	NParams int      `json:"nparams"`
	Locals  []Ty     `json:"locals"` // params first
	Names   []string `json:"names"`  // debug names of locals
	Result  Ty       `json:"result"`
	Async   bool     `json:"async"`
	Blocks  []*Block `json:"blocks"` // entry is block 0
}

// HandlerParam describes how the runtime supplies a handler argument.
type HandlerParam struct {
	Kind string `json:"kind"` // "request", "args" (Vec<String>) or a capability name: Log, Net, Db, Clock, Random, Fs
	Name string `json:"name"` // source parameter name (selects the binding)
}

type Program struct {
	Types         []*TypeDef     `json:"types"`
	Funcs         []*Func        `json:"funcs"`
	Handler       string         `json:"handler,omitempty"`
	Entry         string         `json:"entry,omitempty"` // "handler" or "main"
	HandlerParams []HandlerParam `json:"handler_params,omitempty"`
}

func (p *Program) Func(name string) *Func {
	for _, f := range p.Funcs {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// String pretty-prints the program (for `kek ir` and debugging).
func (p *Program) String() string {
	var b strings.Builder
	for i, t := range p.Types {
		if t.IsVariant {
			fmt.Fprintf(&b, "type agg%d %s = variant %v\n", i, t.Name, t.Variants)
		} else {
			fmt.Fprintf(&b, "type agg%d %s = struct %v\n", i, t.Name, t.Fields)
		}
	}
	for _, f := range p.Funcs {
		async := ""
		if f.Async {
			async = "async "
		}
		fmt.Fprintf(&b, "\n%sfn %s(", async, f.Name)
		for i := 0; i < f.NParams; i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%%%d %s: %s", i, f.Names[i], f.Locals[i])
		}
		fmt.Fprintf(&b, ") -> %s {\n", f.Result)
		for bi, blk := range f.Blocks {
			fmt.Fprintf(&b, "  b%d:\n", bi)
			for _, in := range blk.Instrs {
				fmt.Fprintf(&b, "    %%%d = %s\n", in.Dst, fmtInstr(in))
			}
			fmt.Fprintf(&b, "    %s\n", fmtTerm(blk.Term))
		}
		b.WriteString("}\n")
	}
	return b.String()
}

func args(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = fmt.Sprintf("%%%d", x)
	}
	return strings.Join(s, ", ")
}

func fmtInstr(in Instr) string {
	switch in.Op {
	case "const":
		switch in.Const.Kind {
		case TInt:
			return fmt.Sprintf("const %d", in.Const.Int)
		case TBool:
			return fmt.Sprintf("const %t", in.Const.Bool)
		case TString:
			return fmt.Sprintf("const %q", in.Const.Str)
		}
		return "const ()"
	case "copy":
		return args(in.Args)
	case "unop", "binop", "call", "host", "await":
		return fmt.Sprintf("%s %s(%s)", in.Op, in.Name, args(in.Args))
	case "struct":
		return fmt.Sprintf("struct agg%d{%s}", in.Type, args(in.Args))
	case "field":
		return fmt.Sprintf("field agg%d %s.%d", in.Type, args(in.Args), in.Index)
	case "variant":
		return fmt.Sprintf("variant agg%d.%d(%s)", in.Type, in.Tag, args(in.Args))
	case "tag":
		return fmt.Sprintf("tag agg%d %s", in.Type, args(in.Args))
	case "vfield":
		return fmt.Sprintf("vfield agg%d %s.%d.%d", in.Type, args(in.Args), in.Tag, in.Index)
	}
	return in.Op
}

func fmtTerm(t Term) string {
	switch t.Op {
	case "jump":
		return fmt.Sprintf("jump b%d", t.Targets[0])
	case "br":
		return fmt.Sprintf("br %s b%d b%d", args(t.Args), t.Targets[0], t.Targets[1])
	case "switch":
		ts := make([]string, len(t.Targets))
		for i, x := range t.Targets {
			ts[i] = fmt.Sprintf("b%d", x)
		}
		return fmt.Sprintf("switch %s [%s]", args(t.Args), strings.Join(ts, " "))
	case "ret":
		return "ret " + args(t.Args)
	}
	return t.Op
}
