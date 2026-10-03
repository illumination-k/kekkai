package tooling

import (
	"fmt"
	"sort"
	"strings"

	"github.com/illumination-k/kekkai/internal/types"
)

// FuncSignature renders a user function's signature, e.g.
// `fn transfer(db: &Db, amount: Int) -> Result<Int, TxError>`.
func FuncSignature(fn *types.Func) string {
	var b strings.Builder
	b.WriteString("fn ")
	b.WriteString(fn.Name)
	b.WriteByte('(')
	for i, p := range fn.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s: %s", p.Name, p.Type)
	}
	b.WriteByte(')')
	if fn.Result != nil && fn.Result != types.Unit {
		fmt.Fprintf(&b, " -> %s", fn.Result)
	}
	return b.String()
}

// IsStatic reports whether b is a static builtin (`Owner::name`).
func IsStatic(b *types.Builtin) bool { return types.LookupStatic(b.Recv, b.Name) == b }

// IsCapName reports whether name is a capability type.
func IsCapName(name string) bool {
	switch name {
	case "Log", "Net", "Db", "Clock", "Random", "Tx":
		return true
	}
	return false
}

// BuiltinSignature renders a builtin, e.g. `fn String.parse_int(self) -> Option<Int>`
// or `fn Response::text(Int, String) -> Response`.
func BuiltinSignature(b *types.Builtin) string {
	var parts []string
	sep := "."
	if IsStatic(b) {
		sep = "::"
	} else if IsCapName(b.Recv) {
		parts = append(parts, "&self")
	} else {
		parts = append(parts, "self")
	}
	for _, p := range b.Params {
		parts = append(parts, p.String())
	}
	s := fmt.Sprintf("fn %s%s%s(%s)", b.Recv, sep, b.Name, strings.Join(parts, ", "))
	if b.Result != nil && b.Result != types.Unit {
		s += " -> " + b.Result.String()
	}
	return s
}

// builtinNote describes the effect and async-ness of a builtin.
func builtinNote(b *types.Builtin) string {
	var notes []string
	if IsCapName(b.Recv) && !IsStatic(b) {
		notes = append(notes, fmt.Sprintf("effect: performs `%s` (capability `%s.%s`)", b.Recv, strings.ToLower(b.Recv), b.Name))
	} else {
		notes = append(notes, "pure")
	}
	if b.Async {
		notes = append(notes, "async (suspends; callers become resumable state machines)")
	}
	if b.Consumes {
		notes = append(notes, "consumes the owned `Tx` (only inside its `transaction` block)")
	}
	return strings.Join(notes, " · ")
}

// CapReport is the capability summary of one function (the JSON format of
// `kek caps -json`).
type CapReport struct {
	Name      string   `json:"name"`
	Signature string   `json:"signature"`
	Line      int      `json:"line"`
	Col       int      `json:"col"`
	Handler   bool     `json:"handler"`
	Pure      bool     `json:"pure"`
	Async     bool     `json:"async"`
	Caps      []CapArg `json:"caps"`
	// DirectEffects are the capability operations used in the body itself
	// (e.g. "log.info"); effects of callees are covered by the caps passed
	// to them.
	DirectEffects []string `json:"direct_effects"`
	UnusedCaps    []string `json:"unused_caps"`
	Calls         []string `json:"calls"`
}

// CapArg is one capability parameter.
type CapArg struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Used bool   `json:"used"`
}

// CapReports summarizes every function in declaration order.
func CapReports(a *Analysis) []CapReport {
	out := []CapReport{}
	if a.Info == nil {
		return out
	}
	for _, fn := range a.Info.FuncList {
		r := CapReport{Name: fn.Name, Signature: FuncSignature(fn), Handler: fn.Handler, Pure: fn.Pure(), Async: fn.Async,
			Caps: []CapArg{}, DirectEffects: sortedKeys(fn.Effects), UnusedCaps: []string{}, Calls: sortedKeys(fn.Calls)}
		p := fn.Decl.Pos
		if a.ix != nil {
			if np, ok := a.ix.funcPos[fn]; ok {
				p = np
			}
		}
		r.Line, r.Col = p.Line, p.Col
		for _, c := range fn.Caps() {
			r.Caps = append(r.Caps, CapArg{Name: c.Name, Type: c.Type.String(), Used: c.Uses > 0})
			if c.Uses == 0 {
				r.UnusedCaps = append(r.UnusedCaps, c.Name)
			}
		}
		out = append(out, r)
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// funcHover renders the hover text of a user function.
func (a *Analysis) funcHover(fn *types.Func) string {
	var b strings.Builder
	b.WriteString("```kek\n")
	if fn.Handler {
		b.WriteString("#[handler]\n")
	}
	b.WriteString(FuncSignature(fn))
	b.WriteString("\n```\n")
	var caps []string
	for _, c := range fn.Caps() {
		s := "`" + c.Name + ": " + c.Type.String() + "`"
		if c.Uses == 0 {
			s += " (unused)"
		}
		caps = append(caps, s)
	}
	if len(caps) == 0 {
		b.WriteString("\n**pure** — receives no capabilities, so it performs no effects")
	} else {
		b.WriteString("\n**capabilities:** " + strings.Join(caps, ", "))
	}
	if fn.Async {
		b.WriteString("\n\n**async** — compiled to a resumable state machine")
	}
	if eff := sortedKeys(fn.Effects); len(eff) > 0 {
		b.WriteString("\n\n**direct effects:** `" + strings.Join(eff, "`, `") + "`")
	}
	return b.String()
}

func structText(st *types.Struct) string {
	var b strings.Builder
	fmt.Fprintf(&b, "struct %s {", st.Name)
	for i, f := range st.Fields {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "\n    %s: %s", f.Name, f.Type)
	}
	if len(st.Fields) > 0 {
		b.WriteString(",\n")
	}
	b.WriteByte('}')
	return b.String()
}

func variantText(en *types.Enum, v *types.VariantInfo) string {
	s := en.Name + "::" + v.Name
	if len(v.Fields) > 0 {
		var fs []string
		for _, f := range v.Fields {
			fs = append(fs, f.String())
		}
		s += "(" + strings.Join(fs, ", ") + ")"
	}
	return s
}

func enumText(en *types.Enum) string {
	var b strings.Builder
	fmt.Fprintf(&b, "enum %s {", en.Name)
	for _, v := range en.Variants {
		s := v.Name
		if len(v.Fields) > 0 {
			var fs []string
			for _, f := range v.Fields {
				fs = append(fs, f.String())
			}
			s += "(" + strings.Join(fs, ", ") + ")"
		}
		fmt.Fprintf(&b, "\n    %s,", s)
	}
	if len(en.Variants) > 0 {
		b.WriteByte('\n')
	}
	b.WriteByte('}')
	return b.String()
}

func code(s string) string { return "```kek\n" + s + "\n```" }

// typeString renders a type without defaulting inference variables.
func typeString(t types.Type) string {
	if t == nil {
		return "?"
	}
	return types.Prune(t).String()
}
