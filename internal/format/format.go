// Package format implements `kek fmt`: the canonical layout of Kekkai
// source (4-space indentation, rustfmt-like).
//
// The printer works from the AST. Comments are not part of the AST: they
// are collected separately by the lexer (syntax.LexWithComments) and
// re-attached by position:
//
//   - every "unit" (top-level item, struct field, enum variant, statement,
//     match arm) owns the comments that precede it on their own lines
//     (leading), and those after its last token on the same line
//     (trailing);
//   - comments inside a unit that no nested unit claims (e.g. between call
//     arguments) are hoisted above the unit, so no comment is ever lost;
//   - comments after the last unit of a block stay before its `}`.
//
// Formatting is idempotent and preserves the AST (tested over testdata and
// randomly generated programs).
package format

import (
	"strings"

	"github.com/illumination-k/kekkai/internal/syntax"
)

const (
	indentUnit = "    "
	maxWidth   = 100
	// maxSingleLineIf is the widest `if c { a } else { b }` kept on one line.
	maxSingleLineIf = 50
)

// Source formats a complete .kek file. It fails if the file does not parse.
func Source(src []byte) ([]byte, error) {
	s := string(src)
	f, err := syntax.Parse(s)
	if err != nil {
		return nil, err
	}
	toks, comments, _ := syntax.LexWithComments(s)
	p := &printer{
		src:      s,
		toks:     toks,
		tokAt:    map[syntax.Pos]int{},
		comments: comments,
		used:     make([]bool, len(comments)),
	}
	p.lineStart = []int{0}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			p.lineStart = append(p.lineStart, i+1)
		}
	}
	for i, t := range toks {
		p.tokAt[t.Pos] = i
	}
	out := p.file(f)
	return []byte(out), nil
}

type printer struct {
	src       string
	lineStart []int
	toks      []syntax.Token
	tokAt     map[syntax.Pos]int
	comments  []syntax.Comment
	used      []bool
	flat      int // > 0 while trying to print a binary expression on one line
}

// ---- positions and comments ----

func before(a, b syntax.Pos) bool {
	return a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col)
}

func (p *printer) offset(pos syntax.Pos) int {
	if pos.Line < 1 || pos.Line > len(p.lineStart) {
		return len(p.src)
	}
	return p.lineStart[pos.Line-1] + pos.Col - 1
}

// blankBefore reports whether the source line before `line` is blank.
func (p *printer) blankBefore(line int) bool {
	if line < 2 || line > len(p.lineStart) {
		return false
	}
	start := p.lineStart[line-2]
	end := p.lineStart[line-1]
	return strings.TrimSpace(p.src[start:end]) == ""
}

// tokIndex returns the index of the token starting at pos. Opening
// parentheses directly before it are included, so that a unit starting
// with a parenthesized expression owns its `(`.
func (p *printer) tokIndex(pos syntax.Pos) int {
	i, ok := p.tokAt[pos]
	if !ok {
		// Should not happen for positions produced by the parser; fall
		// back to the first token at or after pos.
		for i = 0; i < len(p.toks)-1 && before(p.toks[i].Pos, pos); i++ {
		}
	}
	for i > 0 && p.toks[i-1].Kind == syntax.LParen {
		i--
	}
	return i
}

func (p *printer) save() []bool { return append([]bool(nil), p.used...) }

func (p *printer) restore(s []bool) { copy(p.used, s) }

// take marks and returns the unused comments accepted by keep.
func (p *printer) take(keep func(c syntax.Comment) bool) []syntax.Comment {
	var out []syntax.Comment
	for i, c := range p.comments {
		if !p.used[i] && keep(c) {
			p.used[i] = true
			out = append(out, c)
		}
	}
	return out
}

// hasComments reports whether an unused comment lies in [from, to).
func (p *printer) hasComments(from, to syntax.Pos) bool {
	for i, c := range p.comments {
		if !p.used[i] && !before(c.Pos, from) && before(c.Pos, to) {
			return true
		}
	}
	return false
}

// ---- containers ----

// unit is a line-level element of a container: an item, field, variant,
// statement or match arm.
type unit struct {
	start  syntax.Pos
	render func(ind int) string
}

func indent(n int) string { return strings.Repeat(indentUnit, n) }

// container lays out units one per line. For a braced container, open and
// close are the token indices of `{` and `}` and the units are indented one
// level deeper than ind; the top level uses open = -1.
func (p *printer) container(open, close int, units []unit, ind int) string {
	top := open < 0
	contentInd := ind + 1
	if top {
		contentInd = 0
	}
	closePos := p.toks[close].Pos
	firstStart := closePos
	if len(units) > 0 {
		firstStart = units[0].start
	}

	// inside reports whether a comment lies after the opening brace.
	inside := func(c syntax.Comment) bool { return top || before(p.toks[open].Pos, c.Pos) }
	var b strings.Builder
	head := ""
	if !top {
		head = "{"
		ot := p.toks[open].Pos
		for _, c := range p.take(func(c syntax.Comment) bool {
			return c.Pos.Line == ot.Line && before(ot, c.Pos) && before(c.Pos, firstStart)
		}) {
			head += " " + c.Text
		}
	}
	first := true
	piece := func(text string, blank bool) {
		if blank && !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(indent(contentInd))
		b.WriteString(text)
		b.WriteByte('\n')
	}
	ownLine := func(cs []syntax.Comment, forceBlank bool) {
		for i, c := range cs {
			piece(c.Text, (i == 0 && forceBlank) || p.blankBefore(c.Pos.Line))
		}
	}

	for i, u := range units {
		lead := p.take(func(c syntax.Comment) bool { return inside(c) && before(c.Pos, u.start) })
		ownLine(lead, top && i > 0)
		blank := p.blankBefore(u.start.Line) || (top && i > 0 && len(lead) == 0)

		text := u.render(contentInd)

		nextStart, nextIdx := closePos, close
		if i+1 < len(units) {
			nextStart = units[i+1].start
			nextIdx = p.tokIndex(nextStart)
		}
		endIdx := nextIdx - 1
		if endIdx < 0 {
			endIdx = 0
		}
		end := p.toks[endIdx].Pos
		interior := p.take(func(c syntax.Comment) bool {
			return before(u.start, c.Pos) && before(c.Pos, end)
		})
		for j, c := range interior {
			piece(c.Text, j == 0 && blank)
			blank = false
		}
		for _, c := range p.take(func(c syntax.Comment) bool {
			return c.Pos.Line == end.Line && before(end, c.Pos) && before(c.Pos, nextStart)
		}) {
			text += " " + c.Text
		}
		piece(text, blank)
	}
	var rest []syntax.Comment
	if top {
		rest = p.take(func(syntax.Comment) bool { return true })
	} else {
		rest = p.take(func(c syntax.Comment) bool { return inside(c) && before(c.Pos, closePos) })
	}
	ownLine(rest, false)

	if top {
		return b.String()
	}
	if b.Len() == 0 {
		if head == "{" {
			return "{}"
		}
		return head + "\n" + indent(ind) + "}"
	}
	return head + "\n" + b.String() + indent(ind) + "}"
}

// matchClose returns the index of the bracket closing the one at open.
func (p *printer) matchClose(open int) int {
	depth := 0
	for i := open; i < len(p.toks); i++ {
		switch p.toks[i].Kind {
		case syntax.LBrace, syntax.LParen, syntax.LBracket:
			depth++
		case syntax.RBrace, syntax.RParen, syntax.RBracket:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return len(p.toks) - 1
}

// nextBrace returns the index of the first `{` at or after token i.
func (p *printer) nextBrace(i int) int {
	for ; i < len(p.toks)-1; i++ {
		if p.toks[i].Kind == syntax.LBrace {
			return i
		}
	}
	return i
}

// ---- items ----

func (p *printer) file(f *syntax.File) string {
	var items []unit
	for _, d := range f.Structs {
		d := d
		items = append(items, unit{d.Pos, func(ind int) string { return p.structDecl(d, ind) }})
	}
	for _, d := range f.Enums {
		d := d
		items = append(items, unit{d.Pos, func(ind int) string { return p.enumDecl(d, ind) }})
	}
	for _, d := range f.Funcs {
		d := d
		start := d.Pos
		if len(d.Attrs) > 0 {
			start = d.Attrs[0].Pos
		}
		items = append(items, unit{start, func(ind int) string { return p.funcDecl(d, ind) }})
	}
	// restore source order
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && before(items[j].start, items[j-1].start); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	return p.container(-1, len(p.toks)-1, items, 0)
}

func (p *printer) structDecl(d *syntax.StructDecl, ind int) string {
	open := p.nextBrace(p.tokIndex(d.Pos))
	var units []unit
	for _, fd := range d.Fields {
		fd := fd
		units = append(units, unit{fd.Pos, func(int) string {
			return fd.Name + ": " + p.typ(fd.Type) + ","
		}})
	}
	return "struct " + d.Name + " " + p.container(open, p.matchClose(open), units, ind)
}

func (p *printer) enumDecl(d *syntax.EnumDecl, ind int) string {
	open := p.nextBrace(p.tokIndex(d.Pos))
	var units []unit
	for _, v := range d.Variants {
		v := v
		units = append(units, unit{v.Pos, func(int) string {
			s := v.Name
			if len(v.Fields) > 0 {
				ts := make([]string, len(v.Fields))
				for i, t := range v.Fields {
					ts[i] = p.typ(t)
				}
				s += "(" + strings.Join(ts, ", ") + ")"
			}
			return s + ","
		}})
	}
	return "enum " + d.Name + " " + p.container(open, p.matchClose(open), units, ind)
}

func (p *printer) funcDecl(d *syntax.FuncDecl, ind int) string {
	var b strings.Builder
	for _, a := range d.Attrs {
		b.WriteString("#[" + a.Name + "]\n" + indent(ind))
	}
	params := make([]string, len(d.Params))
	for i, prm := range d.Params {
		params[i] = prm.Name + ": " + p.typ(prm.Type)
	}
	ret := ""
	if d.Result != nil {
		ret = " -> " + p.typ(d.Result)
	}
	head := "fn " + d.Name + "(" + strings.Join(params, ", ") + ")" + ret
	if len(indent(ind))+len(head)+2 > maxWidth && len(params) > 0 {
		head = "fn " + d.Name + "(\n"
		for _, s := range params {
			head += indent(ind+1) + s + ",\n"
		}
		head += indent(ind) + ")" + ret
	}
	b.WriteString(head + " " + p.block(d.Body, ind))
	return b.String()
}

func (p *printer) typ(t syntax.TypeExpr) string {
	switch t := t.(type) {
	case *syntax.UnitType:
		return "()"
	case *syntax.RefType:
		return "&" + p.typ(t.Elem)
	case *syntax.NamedType:
		if len(t.Args) == 0 {
			return t.Name
		}
		as := make([]string, len(t.Args))
		for i, a := range t.Args {
			as[i] = p.typ(a)
		}
		return t.Name + "<" + strings.Join(as, ", ") + ">"
	}
	return "?"
}

// ---- statements ----

func (p *printer) block(b *syntax.Block, ind int) string {
	flat := p.flat
	p.flat = 0
	defer func() { p.flat = flat }()
	var units []unit
	for _, s := range b.Stmts {
		s := s
		units = append(units, unit{stmtPos(s), func(ind int) string { return p.stmt(s, ind) }})
	}
	if b.Tail != nil {
		units = append(units, unit{p.startPos(b.Tail), func(ind int) string {
			return p.stmtExpr(b.Tail, ind)
		}})
	}
	open := p.tokIndex(b.Pos)
	// tokIndex may have stepped back over `(`; the block starts at `{`.
	for p.toks[open].Kind != syntax.LBrace {
		open++
	}
	return p.container(open, p.tokAt[b.End], units, ind)
}

func stmtPos(s syntax.Stmt) syntax.Pos {
	switch s := s.(type) {
	case *syntax.LetStmt:
		return s.Pos
	case *syntax.AssignStmt:
		return s.Pos
	case *syntax.WhileStmt:
		return s.Pos
	case *syntax.ForStmt:
		return s.Pos
	case *syntax.FieldAssignStmt:
		return s.Pos
	case *syntax.BreakStmt:
		return s.Pos
	case *syntax.ContinueStmt:
		return s.Pos
	case *syntax.ReturnStmt:
		return s.Pos
	case *syntax.ExprStmt:
		return s.Pos
	}
	return syntax.Pos{}
}

func (p *printer) stmt(s syntax.Stmt, ind int) string {
	col := len(indent(ind))
	switch s := s.(type) {
	case *syntax.LetStmt:
		h := "let "
		if s.Mut {
			h += "mut "
		}
		h += s.Name
		if s.Type != nil {
			h += ": " + p.typ(s.Type)
		}
		h += " = "
		return h + p.expr(s.Init, ind, col+len(h), false) + ";"
	case *syntax.AssignStmt:
		h := s.Name + " = "
		return h + p.expr(s.Value, ind, col+len(h), false) + ";"
	case *syntax.WhileStmt:
		return "while " + p.expr(s.Cond, ind, col+6, true) + " " + p.block(s.Body, ind)
	case *syntax.ForStmt:
		h := "for " + s.Var + " in "
		h += p.expr(s.Iter, ind, col+len(h), true)
		if s.End != nil {
			h += ".."
			h += p.expr(s.End, ind, advance(col, h), true)
		}
		return h + " " + p.block(s.Body, ind)
	case *syntax.FieldAssignStmt:
		h := p.stmtExpr(s.Target, ind) + " = "
		return h + p.expr(s.Value, ind, advance(col, h), false) + ";"
	case *syntax.BreakStmt:
		return "break;"
	case *syntax.ContinueStmt:
		return "continue;"
	case *syntax.ReturnStmt:
		if s.Value == nil {
			return "return;"
		}
		return "return " + p.expr(s.Value, ind, col+7, false) + ";"
	case *syntax.ExprStmt:
		t := p.stmtExpr(s.X, ind)
		if s.Semi {
			t += ";"
		}
		return t
	}
	return ""
}

// stmtExpr prints an expression in statement position, where a leading
// `if`/`match`/block ends the statement unless followed by `.` or `?`.
func (p *printer) stmtExpr(e syntax.Expr, ind int) string {
	col := len(indent(ind))
	if needsStmtParens(e) {
		return "(" + p.expr(e, ind, col+1, false) + ")"
	}
	return p.expr(e, ind, col, false)
}

func isBlockLike(e syntax.Expr) bool {
	switch e.(type) {
	case *syntax.IfExpr, *syntax.MatchExpr, *syntax.Block:
		return true
	}
	return false
}

func needsStmtParens(e syntax.Expr) bool {
	var parent syntax.Expr
	for {
		if isBlockLike(e) {
			_, bin := parent.(*syntax.BinaryExpr)
			return bin
		}
		next := leftChild(e)
		if next == nil {
			return false
		}
		parent, e = e, next
	}
}

// leftChild returns the operand printed first (without parentheses), or nil.
func leftChild(e syntax.Expr) syntax.Expr {
	switch e := e.(type) {
	case *syntax.BinaryExpr:
		if leftParen(e.X, e.Op) {
			return nil // parenthesized
		}
		return e.X
	case *syntax.MethodCall:
		if prec(e.Recv) < precPostfix {
			return nil
		}
		return e.Recv
	case *syntax.FieldExpr:
		if prec(e.X) < precPostfix {
			return nil
		}
		return e.X
	case *syntax.TryExpr:
		if prec(e.X) < precPostfix {
			return nil
		}
		return e.X
	case *syntax.CallExpr:
		return e.Func
	}
	return nil
}

// startPos returns the position of the first token of an expression.
func (p *printer) startPos(e syntax.Expr) syntax.Pos {
	for {
		switch x := e.(type) {
		case *syntax.BinaryExpr:
			e = x.X
		case *syntax.MethodCall:
			e = x.Recv
		case *syntax.FieldExpr:
			e = x.X
		case *syntax.TryExpr:
			e = x.X
		case *syntax.CallExpr:
			e = x.Func
		default:
			return syntax.ExprPos(e)
		}
	}
}

// ---- expressions ----

const (
	precClosure = 0
	precUnary   = 6
	precPostfix = 7
	precPrimary = 8
)

var precedence = map[syntax.TokenKind]int{
	syntax.PipePipe: 1,
	syntax.AmpAmp:   2,
	syntax.Eq:       3, syntax.Ne: 3, syntax.Lt: 3, syntax.Le: 3, syntax.Gt: 3, syntax.Ge: 3,
	syntax.Plus: 4, syntax.Minus: 4,
	syntax.Star: 5, syntax.Slash: 5, syntax.Percent: 5,
}

var opText = map[syntax.TokenKind]string{
	syntax.PipePipe: "||", syntax.AmpAmp: "&&", syntax.Eq: "==", syntax.Ne: "!=",
	syntax.Lt: "<", syntax.Le: "<=", syntax.Gt: ">", syntax.Ge: ">=",
	syntax.Plus: "+", syntax.Minus: "-", syntax.Star: "*", syntax.Slash: "/", syntax.Percent: "%",
	syntax.Bang: "!",
}

func prec(e syntax.Expr) int {
	switch e := e.(type) {
	case *syntax.Closure:
		return precClosure
	case *syntax.BinaryExpr:
		return precedence[e.Op]
	case *syntax.UnaryExpr:
		return precUnary
	case *syntax.CallExpr, *syntax.MethodCall, *syntax.FieldExpr, *syntax.TryExpr:
		return precPostfix
	}
	return precPrimary
}

func lastLineLen(s string) int {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return len(s) - i - 1
	}
	return -1 // single line
}

// advance returns the column after printing s starting at col.
func advance(col int, s string) int {
	if n := lastLineLen(s); n >= 0 {
		return n
	}
	return col + len(s)
}

// sub prints an operand, parenthesized when paren is set.
func (p *printer) sub(e syntax.Expr, ind, col int, ns, paren bool) string {
	if paren {
		return "(" + p.expr(e, ind, col+1, false) + ")"
	}
	return p.expr(e, ind, col, ns)
}

// expr prints e starting at column col; continuation lines carry their
// own indentation. ns is set where struct literals are not allowed (the
// heads of `if`, `while` and `match`).
func (p *printer) expr(e syntax.Expr, ind, col int, ns bool) string {
	switch e := e.(type) {
	case *syntax.IntLit:
		return p.intText(e.Pos)
	case *syntax.StringLit:
		return p.strText(e.Pos)
	case *syntax.BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case *syntax.UnitLit:
		return "()"
	case *syntax.Ident:
		return e.Name
	case *syntax.PathExpr:
		return e.Type + "::" + e.Name
	case *syntax.BinaryExpr:
		pr := precedence[e.Op]
		saved := p.save()
		// Operators are laid out by the outermost binary expression: nested
		// ones stay flat while it tries a single line.
		p.flat++
		l := p.sub(e.X, ind, col, ns, leftParen(e.X, e.Op))
		op := " " + opText[e.Op] + " "
		r := p.sub(e.Y, ind, advance(col, l)+len(op), ns, rightParen(e.Y, e.Op))
		p.flat--
		s := l + op + r
		if p.flat > 0 || strings.Contains(s, "\n") || col+len(s) <= maxWidth {
			return s
		}
		// Too long: break before each operator of this precedence chain.
		p.restore(saved)
		var operands []syntax.Expr
		var ops []syntax.TokenKind
		var x syntax.Expr = e
		for {
			b, ok := x.(*syntax.BinaryExpr)
			if !ok || precedence[b.Op] != pr || (b != e && pr == 3) {
				break
			}
			operands = append([]syntax.Expr{b.Y}, operands...)
			ops = append([]syntax.TokenKind{b.Op}, ops...)
			x = b.X
		}
		s = p.sub(x, ind, col, ns, leftParen(x, e.Op))
		ccol := len(indent(ind + 1))
		for i, y := range operands {
			h := opText[ops[i]] + " "
			s += "\n" + indent(ind+1) + h + p.sub(y, ind+1, ccol+len(h), ns, rightParen(y, ops[i]))
		}
		return s
	case *syntax.UnaryExpr:
		op := opText[e.Op]
		return op + p.sub(e.X, ind, col+1, ns, prec(e.X) < precUnary)
	case *syntax.CallExpr:
		f := p.sub(e.Func, ind, col, ns, prec(e.Func) < precPostfix)
		return f + p.args(e.Args, ind, advance(col, f))
	case *syntax.MethodCall:
		r := p.sub(e.Recv, ind, col, ns, prec(e.Recv) < precPostfix)
		h := r + "." + e.Name
		return h + p.args(e.Args, ind, advance(col, h))
	case *syntax.FieldExpr:
		return p.sub(e.X, ind, col, ns, prec(e.X) < precPostfix) + "." + e.Name
	case *syntax.TryExpr:
		return p.sub(e.X, ind, col, ns, prec(e.X) < precPostfix) + "?"
	case *syntax.StructLit:
		if ns {
			return "(" + p.structLit(e, ind, col+1) + ")"
		}
		return p.structLit(e, ind, col)
	case *syntax.IfExpr:
		return p.ifExpr(e, ind, col)
	case *syntax.MatchExpr:
		return p.matchExpr(e, ind, col)
	case *syntax.Block:
		return p.block(e, ind)
	case *syntax.Closure:
		h := "||"
		if len(e.Params) > 0 {
			ps := make([]string, len(e.Params))
			for i, prm := range e.Params {
				ps[i] = prm.Name
				if prm.Type != nil {
					ps[i] += ": " + p.typ(prm.Type)
				}
			}
			h = "|" + strings.Join(ps, ", ") + "|"
		}
		h += " "
		return h + p.expr(e.Body, ind, col+len(h), ns)
	}
	return "?"
}

// overflows reports whether e may start on the line of a call and
// continue below it (a trailing closure or block-like argument).
func overflows(e syntax.Expr) bool {
	switch e.(type) {
	case *syntax.Closure, *syntax.Block, *syntax.IfExpr, *syntax.MatchExpr, *syntax.StructLit:
		return true
	}
	return false
}

// list prints a delimited, comma-separated list: on one line when it fits,
// with the last element overflowing when only it spans lines, and one
// element per line (with a trailing comma) otherwise.
// pad is put inside the delimiters on one line (`{ a }` vs `(a)`).
func (p *printer) list(open, close, pad string, n int, elem func(i, ind, col int) string, last syntax.Expr, ind, col int) string {
	if n == 0 {
		return open + close
	}
	saved := p.save()
	var b strings.Builder
	b.WriteString(open + pad)
	c := col + len(open) + len(pad)
	multi := -1
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(", ")
			c += 2
		}
		s := elem(i, ind, c)
		if strings.Contains(s, "\n") && multi < 0 {
			multi = i
		}
		b.WriteString(s)
		c = advance(c, s)
	}
	b.WriteString(pad + close)
	inline := b.String()
	firstLine := inline
	if k := strings.IndexByte(inline, '\n'); k >= 0 {
		firstLine = inline[:k]
	}
	fits := col+len(firstLine) <= maxWidth
	// Inside a binary expression being tried on one line, stay flat: the
	// outermost operator chain decides where to break.
	if multi < 0 && (fits || p.flat > 0) {
		return inline
	}
	if multi == n-1 && fits && last != nil && overflows(last) {
		return inline
	}
	p.restore(saved)
	flat := p.flat
	p.flat = 0
	defer func() { p.flat = flat }()
	b.Reset()
	b.WriteString(open + "\n")
	for i := 0; i < n; i++ {
		b.WriteString(indent(ind + 1))
		b.WriteString(elem(i, ind+1, len(indent(ind+1))))
		b.WriteString(",\n")
	}
	b.WriteString(indent(ind) + close)
	return b.String()
}

func (p *printer) args(args []syntax.Expr, ind, col int) string {
	var last syntax.Expr
	if len(args) > 0 {
		last = args[len(args)-1]
	}
	return p.list("(", ")", "", len(args), func(i, ind, col int) string {
		return p.expr(args[i], ind, col, false)
	}, last, ind, col)
}

func (p *printer) structLit(e *syntax.StructLit, ind, col int) string {
	h := e.Name + " "
	if len(e.Fields) == 0 {
		return h + "{}"
	}
	var last syntax.Expr = e.Fields[len(e.Fields)-1].Value
	return h + p.list("{", "}", " ", len(e.Fields), func(i, ind, col int) string {
		f := e.Fields[i]
		if id, ok := f.Value.(*syntax.Ident); ok && id.Name == f.Name && id.Pos == f.Pos {
			return f.Name // shorthand `Point { x, y }`
		}
		return f.Name + ": " + p.expr(f.Value, ind, col+len(f.Name)+2, false)
	}, last, ind, col+len(h))
}

func (p *printer) ifExpr(e *syntax.IfExpr, ind, col int) string {
	if s, ok := p.singleLineIf(e, ind, col); ok {
		return s
	}
	cond := p.expr(e.Cond, ind, col+3, true)
	s := "if " + cond + " " + p.block(e.Then, ind)
	switch el := e.Else.(type) {
	case *syntax.IfExpr:
		s += " else " + p.ifExpr(el, ind, len(indent(ind))+7)
	case *syntax.Block:
		s += " else " + p.block(el, ind)
	}
	return s
}

// singleLineIf prints `if c { a } else { b }` on one line when both
// branches are a single short expression.
func (p *printer) singleLineIf(e *syntax.IfExpr, ind, col int) (string, bool) {
	els, ok := e.Else.(*syntax.Block)
	if !ok {
		return "", false
	}
	simple := func(b *syntax.Block) bool {
		return len(b.Stmts) == 0 && b.Tail != nil && !p.hasComments(b.Pos, b.End) && !containsBlock(b.Tail)
	}
	if !simple(e.Then) || !simple(els) || containsBlock(e.Cond) {
		return "", false
	}
	saved := p.save()
	cond := p.expr(e.Cond, ind, col+3, true)
	a := p.expr(e.Then.Tail, ind, col, false)
	b := p.expr(els.Tail, ind, col, false)
	s := "if " + cond + " { " + a + " } else { " + b + " }"
	if strings.Contains(s, "\n") || len(s) > maxSingleLineIf || col+len(s) > maxWidth {
		p.restore(saved)
		return "", false
	}
	return s, true
}

// containsBlock reports whether e contains a block-like expression or a
// closure (which always print across lines or would be ambiguous inline).
func containsBlock(e syntax.Expr) bool {
	found := false
	var walk func(e syntax.Expr)
	walk = func(e syntax.Expr) {
		if found || e == nil {
			return
		}
		switch e := e.(type) {
		case *syntax.IfExpr, *syntax.MatchExpr, *syntax.Block, *syntax.Closure:
			found = true
		case *syntax.BinaryExpr:
			walk(e.X)
			walk(e.Y)
		case *syntax.UnaryExpr:
			walk(e.X)
		case *syntax.CallExpr:
			walk(e.Func)
			for _, a := range e.Args {
				walk(a)
			}
		case *syntax.MethodCall:
			walk(e.Recv)
			for _, a := range e.Args {
				walk(a)
			}
		case *syntax.FieldExpr:
			walk(e.X)
		case *syntax.TryExpr:
			walk(e.X)
		case *syntax.StructLit:
			for _, f := range e.Fields {
				walk(f.Value)
			}
		}
	}
	walk(e)
	return found
}

func (p *printer) matchExpr(e *syntax.MatchExpr, ind, col int) string {
	x := p.expr(e.X, ind, col+6, true)
	var open int
	if len(e.Arms) > 0 {
		open = p.tokIndex(e.Arms[0].Pos) - 1
		for open > 0 && p.toks[open].Kind != syntax.LBrace {
			open--
		}
	} else {
		open = p.nextBrace(p.tokIndex(p.startPos(e.X)) + 1)
	}
	var units []unit
	for _, a := range e.Arms {
		a := a
		units = append(units, unit{a.Pos, func(ind int) string {
			h := p.pat(a.Pat) + " => "
			body := p.expr(a.Body, ind, len(indent(ind))+len(h), false)
			if _, ok := a.Body.(*syntax.Block); ok {
				return h + body
			}
			return h + body + ","
		}})
	}
	return "match " + x + " " + p.container(open, p.matchClose(open), units, ind)
}

func (p *printer) pat(pt *syntax.Pattern) string {
	switch {
	case pt.Wildcard:
		return "_"
	case pt.IntValue != nil:
		return p.intText(pt.Pos)
	case pt.BoolLit != nil:
		if *pt.BoolLit {
			return "true"
		}
		return "false"
	case pt.StrValue != nil:
		return p.strText(pt.Pos)
	case pt.Ctor != "":
		s := pt.Ctor
		if pt.Type != "" {
			s = pt.Type + "::" + s
			if len(pt.Args) == 0 {
				return s
			}
		}
		as := make([]string, len(pt.Args))
		for i, a := range pt.Args {
			as[i] = p.pat(a)
		}
		return s + "(" + strings.Join(as, ", ") + ")"
	}
	return pt.Bind
}

// ---- literals keep their source spelling ----

// intText returns the integer literal at pos as written (with `_`
// separators and, for patterns, a leading `-`).
func (p *printer) intText(pos syntax.Pos) string {
	i := p.offset(pos)
	neg := ""
	if i < len(p.src) && p.src[i] == '-' {
		neg = "-"
		i++
		for i < len(p.src) && (p.src[i] == ' ' || p.src[i] == '\t' || p.src[i] == '\n' || p.src[i] == '\r') {
			i++
		}
	}
	j := i
	for j < len(p.src) && (p.src[j] == '_' || ('0' <= p.src[j] && p.src[j] <= '9')) {
		j++
	}
	return neg + p.src[i:j]
}

// strText returns the string literal at pos as written (escapes intact).
func (p *printer) strText(pos syntax.Pos) string {
	i := p.offset(pos)
	j := i + 1
	for j < len(p.src) && p.src[j] != '"' && p.src[j] != '\n' {
		if p.src[j] == '\\' {
			j++
		}
		j++
	}
	if j >= len(p.src) {
		j = len(p.src) - 1
	}
	return p.src[i : j+1]
}

// mixedLogic reports an `&&` operand of `||`: parenthesized for clarity
// (the AST does not record source parentheses).
func mixedLogic(x syntax.Expr, op syntax.TokenKind) bool {
	b, ok := x.(*syntax.BinaryExpr)
	return ok && op == syntax.PipePipe && b.Op == syntax.AmpAmp
}

// leftParen reports whether the left operand of op needs parentheses.
func leftParen(x syntax.Expr, op syntax.TokenKind) bool {
	pr := precedence[op]
	return prec(x) < pr || (pr == 3 && prec(x) == 3) || mixedLogic(x, op)
}

// rightParen reports whether the right operand of op needs parentheses.
func rightParen(y syntax.Expr, op syntax.TokenKind) bool {
	return prec(y) <= precedence[op] || mixedLogic(y, op)
}
