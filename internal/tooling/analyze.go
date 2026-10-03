package tooling

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/types"
)

// Diagnostic is a compiler message with a source range (End is exclusive
// and on the same line as Pos).
type Diagnostic struct {
	Pos      syntax.Pos
	End      syntax.Pos
	Severity string // "error" or "warning"
	Phase    string // "parse", "type" or "lint"
	Message  string
}

// Analysis is the result of parsing and type-checking one source text.
// It is usable even when the text has errors: File holds every item that
// parsed completely and Info holds whatever the checker could infer.
type Analysis struct {
	Doc  *Doc
	File *syntax.File
	Info *types.Info // nil only if the checker crashed
	Toks []syntax.Token
	// ParseOK is false when the text has syntax errors (then type errors
	// are not reported, since the file is incomplete).
	ParseOK bool
	Diags   []Diagnostic

	tokAt map[syntax.Pos]int
	refs  []*ref
	ix    *indexer
}

// Analyze parses and type-checks src.
func Analyze(src string) *Analysis {
	a := &Analysis{Doc: NewDoc(src), tokAt: map[syntax.Pos]int{}}
	a.Toks, _ = syntax.Lex(src)
	for i, t := range a.Toks {
		a.tokAt[t.Pos] = i
	}
	f, perr := syntax.Parse(src)
	if f == nil {
		f = &syntax.File{}
	}
	a.File = f
	a.ParseOK = perr == nil
	a.addErrors(perr, "parse")
	info, terr := safeCheck(f)
	a.Info = info
	if a.ParseOK {
		a.addErrors(terr, "type")
		a.addLints()
	}
	if a.Info != nil {
		a.index()
	}
	return a
}

func safeCheck(f *syntax.File) (info *types.Info, err error) {
	defer func() {
		if r := recover(); r != nil {
			info, err = nil, fmt.Errorf("internal error in type checker: %v", r)
		}
	}()
	return types.Check(f)
}

func (a *Analysis) addErrors(err error, phase string) {
	if err == nil {
		return
	}
	list, ok := err.(syntax.ErrorList)
	if !ok {
		a.Diags = append(a.Diags, Diagnostic{Pos: syntax.Pos{Line: 1, Col: 1}, End: syntax.Pos{Line: 1, Col: 1},
			Severity: "error", Phase: phase, Message: err.Error()})
		return
	}
	for _, e := range list {
		d := Diagnostic{Pos: e.Pos, End: a.tokenEnd(e.Pos), Severity: "error", Phase: phase, Message: e.Msg}
		if strings.HasPrefix(e.Msg, "unexpected character") {
			// The lexer reports every byte of a non-ASCII character;
			// report the character once, with its full extent.
			off := a.Doc.Offset(e.Pos)
			if off < len(a.Doc.Src) && !utf8.RuneStart(a.Doc.Src[off]) {
				continue
			}
			r, size := utf8.DecodeRuneInString(a.Doc.Src[off:])
			d.Message = fmt.Sprintf("unexpected character %q", r)
			d.End = syntax.Pos{Line: e.Pos.Line, Col: e.Pos.Col + size}
		}
		a.Diags = append(a.Diags, d)
	}
}

// addLints reports capabilities that are granted but never used: the
// function could be given less authority.
func (a *Analysis) addLints() {
	if a.Info == nil {
		return
	}
	for _, fn := range a.Info.FuncList {
		for _, c := range fn.Caps() {
			if c.Uses == 0 && c.Name != "_" {
				a.Diags = append(a.Diags, Diagnostic{Pos: c.Pos, End: a.tokenEnd(c.Pos), Severity: "warning", Phase: "lint",
					Message: fmt.Sprintf("capability `%s: %s` is granted to `%s` but never used; remove it to narrow the function's authority", c.Name, c.Type, fn.Name)})
			}
		}
	}
}

// Errors returns only the error diagnostics.
func (a *Analysis) Errors() []Diagnostic {
	var out []Diagnostic
	for _, d := range a.Diags {
		if d.Severity == "error" {
			out = append(out, d)
		}
	}
	return out
}

// tokenEnd returns the end of the token starting at p, or p+1 if there is
// none.
func (a *Analysis) tokenEnd(p syntax.Pos) syntax.Pos {
	if i, ok := a.tokAt[p]; ok {
		if n := a.tokenLen(i); n > 0 {
			return syntax.Pos{Line: p.Line, Col: p.Col + n}
		}
	}
	return syntax.Pos{Line: p.Line, Col: p.Col + 1}
}

// tokenLen returns the length in bytes of token i (0 for EOF).
func (a *Analysis) tokenLen(i int) int {
	t := a.Toks[i]
	src := a.Doc.Src
	off := a.Doc.Offset(t.Pos)
	switch t.Kind {
	case syntax.EOF:
		return 0
	case syntax.TIdent:
		return len(t.Text)
	case syntax.TInt:
		j := off
		for j < len(src) && (src[j] == '_' || ('0' <= src[j] && src[j] <= '9')) {
			j++
		}
		return j - off
	case syntax.TString:
		j := off + 1
		for j < len(src) && src[j] != '"' && src[j] != '\n' {
			if src[j] == '\\' {
				j++
			}
			j++
		}
		if j < len(src) && src[j] == '"' {
			j++
		}
		if j > len(src) {
			j = len(src)
		}
		return j - off
	}
	if t.Text != "" { // keywords
		return len(t.Text)
	}
	return len(strings.Trim(t.Kind.String(), "`"))
}

// tokenIndexAt returns the index of the token containing p (or ending
// exactly at p when it is an identifier, so that a cursor placed right
// after a name still resolves), or -1.
func (a *Analysis) tokenIndexAt(p syntax.Pos) int {
	inside, after := -1, -1
	for i, t := range a.Toks {
		if t.Pos.Line != p.Line || t.Kind == syntax.EOF {
			continue
		}
		n := a.tokenLen(i)
		if t.Pos.Col <= p.Col && p.Col < t.Pos.Col+n {
			inside = i
		}
		if t.Kind == syntax.TIdent && p.Col == t.Pos.Col+n {
			after = i
		}
	}
	if inside >= 0 && (a.Toks[inside].Kind == syntax.TIdent || after < 0) {
		return inside
	}
	return after
}

// nameAfter returns the position of the first identifier token at or
// after p named name (used to find the name of `fn f`, `let x`, ...).
func (a *Analysis) nameAfter(p syntax.Pos, name string) syntax.Pos {
	i, ok := a.tokAt[p]
	if !ok {
		return p
	}
	for j := i; j < len(a.Toks) && j < i+4; j++ {
		t := a.Toks[j]
		if (t.Kind == syntax.TIdent || t.Kind == syntax.Underscore) && t.Text == name {
			return t.Pos
		}
	}
	return p
}

// tokenAfter returns the position of the token n tokens after p.
func (a *Analysis) tokenAfter(p syntax.Pos, n int) (syntax.Pos, bool) {
	i, ok := a.tokAt[p]
	if !ok || i+n >= len(a.Toks) {
		return p, false
	}
	return a.Toks[i+n].Pos, true
}
