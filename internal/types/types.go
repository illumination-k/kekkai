// Package types implements the Kekkai type checker: local type inference,
// second-class capability checking, effect (capability) tracking and the
// linearity check for transactions.
package types

import (
	"fmt"
	"strings"
)

// Type is a Kekkai type.
type Type interface{ String() string }

// Prim is one of the primitive value types.
type Prim struct{ Name string }

var (
	Unit   = &Prim{"()"}
	Bool   = &Prim{"Bool"}
	Int    = &Prim{"Int"}
	String = &Prim{"String"}
)

func (p *Prim) String() string { return p.Name }

// Opaque is a host-provided immutable data type (Request, Response,
// TxError, NetError). Opaque values are ordinary (first-class) data,
// not capabilities.
type Opaque struct{ Name string }

var (
	Request  = &Opaque{"Request"}
	Response = &Opaque{"Response"}
	TxError  = &Opaque{"TxError"}
	NetError = &Opaque{"NetError"}
	IoError  = &Opaque{"IoError"}
)

func (o *Opaque) String() string { return o.Name }

// Struct is a user-defined record type.
type Struct struct {
	Name   string
	Fields []*FieldInfo
}

type FieldInfo struct {
	Name string
	Type Type
}

func (s *Struct) String() string { return s.Name }

func (s *Struct) Field(name string) (int, *FieldInfo) {
	for i, f := range s.Fields {
		if f.Name == name {
			return i, f
		}
	}
	return -1, nil
}

// Enum is a user-defined sum type.
type Enum struct {
	Name     string
	Variants []*VariantInfo
}

type VariantInfo struct {
	Name   string
	Fields []Type
}

func (e *Enum) String() string { return e.Name }

func (e *Enum) Variant(name string) (int, *VariantInfo) {
	for i, v := range e.Variants {
		if v.Name == name {
			return i, v
		}
	}
	return -1, nil
}

// ResultT is Result<Ok, Err>.
type ResultT struct{ Ok, Err Type }

func (r *ResultT) String() string { return fmt.Sprintf("Result<%s, %s>", r.Ok, r.Err) }

// VecT is Vec<Elem>: a growable array with reference semantics.
type VecT struct{ Elem Type }

func (v *VecT) String() string { return fmt.Sprintf("Vec<%s>", v.Elem) }

// MapT is Map<Key, Val> (Key is Int or String), insertion-ordered, with
// reference semantics.
type MapT struct{ Key, Val Type }

func (m *MapT) String() string { return fmt.Sprintf("Map<%s, %s>", m.Key, m.Val) }

// OptionT is Option<Elem>.
type OptionT struct{ Elem Type }

func (o *OptionT) String() string { return fmt.Sprintf("Option<%s>", o.Elem) }

// CapKind identifies a capability.
type CapKind int

const (
	CapLog CapKind = iota
	CapNet
	CapDb
	CapClock
	CapRandom
	CapTx
	CapFs
)

// CapInfo describes the static properties of a capability kind.
type CapInfo struct {
	Name string
	// Revocable effects can be performed inside a transaction. Irrevocable
	// ones (network, starting another transaction) cannot be rolled back
	// and are masked inside `transaction` bodies.
	Revocable bool
}

var capInfos = map[CapKind]CapInfo{
	CapLog:    {"Log", true},
	CapNet:    {"Net", false},
	CapDb:     {"Db", false},
	CapClock:  {"Clock", true},
	CapRandom: {"Random", true},
	CapTx:     {"Tx", true},
	CapFs:     {"Fs", false},
}

var capByName = map[string]CapKind{
	"Log": CapLog, "Net": CapNet, "Db": CapDb, "Clock": CapClock, "Random": CapRandom, "Tx": CapTx, "Fs": CapFs,
}

func (k CapKind) Info() CapInfo  { return capInfos[k] }
func (k CapKind) String() string { return capInfos[k].Name }

// Cap is a capability type. `&C` is Borrowed; the only owned capability is
// the linear `Tx` bound by a transaction block.
type Cap struct {
	Kind     CapKind
	Borrowed bool
}

func (c *Cap) String() string {
	if c.Borrowed {
		return "&" + c.Kind.String()
	}
	return c.Kind.String()
}

// Var is an inference variable.
type Var struct {
	ID  int
	Ref Type
}

func (v *Var) String() string {
	if v.Ref != nil {
		return v.Ref.String()
	}
	return fmt.Sprintf("?%d", v.ID)
}

// Prune follows bound inference variables.
func Prune(t Type) Type {
	for {
		v, ok := t.(*Var)
		if !ok || v.Ref == nil {
			return t
		}
		t = v.Ref
	}
}

// Resolve returns t with all bound variables substituted. Unbound
// variables are defaulted to ().
func Resolve(t Type) Type {
	t = Prune(t)
	switch t := t.(type) {
	case *Var:
		t.Ref = Unit
		return Unit
	case *ResultT:
		return &ResultT{Resolve(t.Ok), Resolve(t.Err)}
	case *OptionT:
		return &OptionT{Resolve(t.Elem)}
	case *VecT:
		return &VecT{Resolve(t.Elem)}
	case *MapT:
		return &MapT{Resolve(t.Key), Resolve(t.Val)}
	}
	return t
}

// IsCap reports whether t is a capability type.
func IsCap(t Type) bool {
	_, ok := Prune(t).(*Cap)
	return ok
}

// Identical reports structural equality of resolved types.
func Identical(a, b Type) bool {
	a, b = Prune(a), Prune(b)
	switch a := a.(type) {
	case *ResultT:
		b, ok := b.(*ResultT)
		return ok && Identical(a.Ok, b.Ok) && Identical(a.Err, b.Err)
	case *OptionT:
		b, ok := b.(*OptionT)
		return ok && Identical(a.Elem, b.Elem)
	case *VecT:
		b, ok := b.(*VecT)
		return ok && Identical(a.Elem, b.Elem)
	case *MapT:
		b, ok := b.(*MapT)
		return ok && Identical(a.Key, b.Key) && Identical(a.Val, b.Val)
	case *Cap:
		b, ok := b.(*Cap)
		return ok && a.Kind == b.Kind && a.Borrowed == b.Borrowed
	}
	return a == b
}

// Key returns a canonical string for a resolved type (used for layouts).
func Key(t Type) string {
	t = Resolve(t)
	var b strings.Builder
	writeKey(&b, t)
	return b.String()
}

func writeKey(b *strings.Builder, t Type) {
	switch t := t.(type) {
	case *ResultT:
		b.WriteString("Result<")
		writeKey(b, t.Ok)
		b.WriteString(",")
		writeKey(b, t.Err)
		b.WriteString(">")
	case *OptionT:
		b.WriteString("Option<")
		writeKey(b, t.Elem)
		b.WriteString(">")
	case *VecT:
		b.WriteString("Vec<")
		writeKey(b, t.Elem)
		b.WriteString(">")
	case *MapT:
		b.WriteString("Map<")
		writeKey(b, t.Key)
		b.WriteString(",")
		writeKey(b, t.Val)
		b.WriteString(">")
	default:
		b.WriteString(t.String())
	}
}
