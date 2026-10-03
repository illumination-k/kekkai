package syntax

import (
	"strconv"
)

// Parse parses a complete source file.
func Parse(src string) (*File, error) { return ParseFile("", src) }

// ParseFile parses a source file, recording name in positions.
func ParseFile(name, src string) (*File, error) {
	toks, lexErrs := LexFile(name, src)
	p := &parser{toks: toks, errs: lexErrs}
	f := p.parseFile()
	return f, p.errs.Err()
}

// Source is a named source text.
type Source struct {
	Name string
	Text string
}

// ParseFiles parses several files of one program into a single File (all
// files share one namespace).
func ParseFiles(srcs []Source) (*File, error) {
	out := &File{}
	var errs ErrorList
	for _, s := range srcs {
		f, err := ParseFile(s.Name, s.Text)
		if err != nil {
			errs = append(errs, err.(ErrorList)...)
		}
		if f != nil {
			out.Structs = append(out.Structs, f.Structs...)
			out.Enums = append(out.Enums, f.Enums...)
			out.Funcs = append(out.Funcs, f.Funcs...)
		}
	}
	return out, errs.Err()
}

type parser struct {
	toks     []Token
	pos      int
	errs     ErrorList
	noStruct bool // disallow struct literals (in if/while/match heads)
}

type bailout struct{}

func (p *parser) tok() Token          { return p.toks[p.pos] }
func (p *parser) at(k TokenKind) bool { return p.toks[p.pos].Kind == k }
func (p *parser) peekKind(n int) TokenKind {
	if p.pos+n < len(p.toks) {
		return p.toks[p.pos+n].Kind
	}
	return EOF
}

func (p *parser) next() Token {
	t := p.toks[p.pos]
	if t.Kind != EOF {
		p.pos++
	}
	return t
}

func (p *parser) accept(k TokenKind) bool {
	if p.at(k) {
		p.next()
		return true
	}
	return false
}

func (p *parser) fail(pos Pos, format string, args ...any) {
	p.errs.Add(pos, format, args...)
	panic(bailout{})
}

func (p *parser) expect(k TokenKind) Token {
	t := p.tok()
	if t.Kind != k {
		p.fail(t.Pos, "expected %s, found %s", k, describe(t))
	}
	return p.next()
}

func describe(t Token) string {
	switch t.Kind {
	case TIdent:
		return "identifier `" + t.Text + "`"
	case TInt:
		return "integer `" + t.Text + "`"
	case TString:
		return "string literal"
	}
	return t.Kind.String()
}

func (p *parser) parseFile() (f *File) {
	f = &File{}
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(bailout); !ok {
				panic(r)
			}
		}
	}()
	for !p.at(EOF) {
		var attrs []*Attr
		for p.at(Hash) {
			pos := p.next().Pos
			p.expect(LBracket)
			name := p.expect(TIdent)
			p.expect(RBracket)
			attrs = append(attrs, &Attr{Pos: pos, Name: name.Text})
		}
		switch p.tok().Kind {
		case KwFn:
			fn := p.parseFunc()
			fn.Attrs = attrs
			f.Funcs = append(f.Funcs, fn)
		case KwStruct:
			f.Structs = append(f.Structs, p.parseStruct())
		case KwEnum:
			f.Enums = append(f.Enums, p.parseEnum())
		default:
			p.fail(p.tok().Pos, "expected item (`fn`, `struct` or `enum`), found %s", describe(p.tok()))
		}
	}
	return f
}

func (p *parser) parseStruct() *StructDecl {
	pos := p.expect(KwStruct).Pos
	name := p.expect(TIdent).Text
	d := &StructDecl{Pos: pos, Name: name}
	p.expect(LBrace)
	for !p.at(RBrace) {
		fp := p.tok().Pos
		fname := p.expect(TIdent).Text
		p.expect(Colon)
		d.Fields = append(d.Fields, &Field{Pos: fp, Name: fname, Type: p.parseType()})
		if !p.accept(Comma) {
			break
		}
	}
	p.expect(RBrace)
	return d
}

func (p *parser) parseEnum() *EnumDecl {
	pos := p.expect(KwEnum).Pos
	name := p.expect(TIdent).Text
	d := &EnumDecl{Pos: pos, Name: name}
	p.expect(LBrace)
	for !p.at(RBrace) {
		vp := p.tok().Pos
		vname := p.expect(TIdent).Text
		v := &Variant{Pos: vp, Name: vname}
		if p.accept(LParen) {
			for !p.at(RParen) {
				v.Fields = append(v.Fields, p.parseType())
				if !p.accept(Comma) {
					break
				}
			}
			p.expect(RParen)
		}
		d.Variants = append(d.Variants, v)
		if !p.accept(Comma) {
			break
		}
	}
	p.expect(RBrace)
	return d
}

func (p *parser) parseFunc() *FuncDecl {
	pos := p.expect(KwFn).Pos
	name := p.expect(TIdent).Text
	fn := &FuncDecl{Pos: pos, Name: name}
	p.expect(LParen)
	for !p.at(RParen) {
		pp := p.tok().Pos
		var pname string
		if p.at(Underscore) {
			p.next()
			pname = "_"
		} else {
			pname = p.expect(TIdent).Text
		}
		p.expect(Colon)
		fn.Params = append(fn.Params, &Param{Pos: pp, Name: pname, Type: p.parseType()})
		if !p.accept(Comma) {
			break
		}
	}
	p.expect(RParen)
	if p.accept(Arrow) {
		fn.Result = p.parseType()
	}
	fn.Body = p.parseBlock()
	return fn
}

func (p *parser) parseType() TypeExpr {
	t := p.tok()
	switch t.Kind {
	case Amp:
		p.next()
		return &RefType{Pos: t.Pos, Elem: p.parseType()}
	case LParen:
		p.next()
		p.expect(RParen)
		return &UnitType{Pos: t.Pos}
	case TIdent:
		p.next()
		nt := &NamedType{Pos: t.Pos, Name: t.Text}
		if p.accept(Lt) {
			for {
				nt.Args = append(nt.Args, p.parseType())
				if !p.accept(Comma) {
					break
				}
			}
			p.expect(Gt)
		}
		return nt
	}
	p.fail(t.Pos, "expected type, found %s", describe(t))
	return nil
}

func (p *parser) parseBlock() *Block {
	pos := p.expect(LBrace).Pos
	saved := p.noStruct
	p.noStruct = false
	defer func() { p.noStruct = saved }()
	b := &Block{Pos: pos}
	for !p.at(RBrace) && !p.at(EOF) {
		sp := p.tok().Pos
		switch p.tok().Kind {
		case Semi:
			p.next()
			continue
		case KwLet:
			p.next()
			s := &LetStmt{Pos: sp}
			s.Mut = p.accept(KwMut)
			if p.at(Underscore) {
				p.next()
				s.Name = "_"
			} else {
				s.Name = p.expect(TIdent).Text
			}
			if p.accept(Colon) {
				s.Type = p.parseType()
			}
			p.expect(Assign)
			s.Init = p.parseExpr()
			p.expect(Semi)
			b.Stmts = append(b.Stmts, s)
			continue
		case KwWhile:
			p.next()
			p.noStruct = true
			cond := p.parseExpr()
			p.noStruct = false
			body := p.parseBlock()
			b.Stmts = append(b.Stmts, &WhileStmt{Pos: sp, Cond: cond, Body: body})
			continue
		case KwFor:
			p.next()
			fs := &ForStmt{Pos: sp}
			if p.accept(Underscore) {
				fs.Var = "_"
			} else {
				fs.Var = p.expect(TIdent).Text
			}
			p.expect(KwIn)
			p.noStruct = true
			fs.Iter = p.parseExpr()
			if p.accept(DotDot) {
				fs.End = p.parseExpr()
			}
			p.noStruct = false
			fs.Body = p.parseBlock()
			b.Stmts = append(b.Stmts, fs)
			continue
		case KwReturn:
			p.next()
			s := &ReturnStmt{Pos: sp}
			if !p.at(Semi) && !p.at(RBrace) {
				s.Value = p.parseExpr()
			}
			if !p.accept(Semi) && !p.at(RBrace) {
				p.fail(p.tok().Pos, "expected `;` after return, found %s", describe(p.tok()))
			}
			b.Stmts = append(b.Stmts, s)
			continue
		case KwBreak, KwContinue:
			k := p.next().Kind
			if !p.accept(Semi) && !p.at(RBrace) {
				p.fail(p.tok().Pos, "expected `;`, found %s", describe(p.tok()))
			}
			if k == KwBreak {
				b.Stmts = append(b.Stmts, &BreakStmt{Pos: sp})
			} else {
				b.Stmts = append(b.Stmts, &ContinueStmt{Pos: sp})
			}
			continue
		}
		var e Expr
		if k := p.tok().Kind; k == KwIf || k == KwMatch || k == LBrace {
			// A block-like expression in statement position ends the
			// statement unless it is followed by a method call or `?`.
			e = p.parsePrimary()
			if p.at(Dot) || p.at(Question) {
				e = p.parseBinaryRest(p.parsePostfix(e), 0)
			}
		} else {
			e = p.parseExpr()
		}
		if p.accept(Assign) {
			val := p.parseExpr()
			p.expect(Semi)
			switch t := e.(type) {
			case *Ident:
				b.Stmts = append(b.Stmts, &AssignStmt{Pos: sp, Name: t.Name, Value: val})
			case *FieldExpr:
				b.Stmts = append(b.Stmts, &FieldAssignStmt{Pos: sp, Target: t, Value: val})
			default:
				p.fail(sp, "invalid assignment target")
			}
			continue
		}
		if p.accept(Semi) {
			b.Stmts = append(b.Stmts, &ExprStmt{Pos: sp, X: e, Semi: true})
			continue
		}
		if p.at(RBrace) {
			b.Tail = e
			break
		}
		if blockLike(e) {
			b.Stmts = append(b.Stmts, &ExprStmt{Pos: sp, X: e})
			continue
		}
		p.fail(p.tok().Pos, "expected `;` or `}`, found %s", describe(p.tok()))
	}
	b.End = p.expect(RBrace).Pos
	return b
}

func blockLike(e Expr) bool {
	switch e.(type) {
	case *IfExpr, *MatchExpr, *Block:
		return true
	}
	return false
}

func (p *parser) parseExpr() Expr { return p.parseBinary(0) }

var precedence = map[TokenKind]int{
	PipePipe: 1,
	AmpAmp:   2,
	Eq:       3, Ne: 3, Lt: 3, Le: 3, Gt: 3, Ge: 3,
	Plus: 4, Minus: 4,
	Star: 5, Slash: 5, Percent: 5,
}

func (p *parser) parseBinary(minPrec int) Expr {
	return p.parseBinaryRest(p.parseUnary(), minPrec)
}

func (p *parser) parseBinaryRest(x Expr, minPrec int) Expr {
	for {
		op := p.tok().Kind
		prec, ok := precedence[op]
		if !ok || prec <= minPrec {
			return x
		}
		pos := p.next().Pos
		y := p.parseBinary(prec)
		if prec == 3 {
			// comparisons are non-associative
			if _, ok := precedence[p.tok().Kind]; ok && precedence[p.tok().Kind] == 3 {
				p.fail(p.tok().Pos, "comparison operators cannot be chained")
			}
		}
		x = &BinaryExpr{Pos: pos, Op: op, X: x, Y: y}
	}
}

func (p *parser) parseUnary() Expr {
	t := p.tok()
	if t.Kind == Minus || t.Kind == Bang {
		p.next()
		return &UnaryExpr{Pos: t.Pos, Op: t.Kind, X: p.parseUnary()}
	}
	return p.parsePostfix(p.parsePrimary())
}

func (p *parser) parseArgs() []Expr {
	p.expect(LParen)
	saved := p.noStruct
	p.noStruct = false
	defer func() { p.noStruct = saved }()
	var args []Expr
	for !p.at(RParen) {
		args = append(args, p.parseExpr())
		if !p.accept(Comma) {
			break
		}
	}
	p.expect(RParen)
	return args
}

func (p *parser) parsePostfix(x Expr) Expr {
	for {
		t := p.tok()
		switch t.Kind {
		case LParen:
			x = &CallExpr{Pos: ExprPos(x), Func: x, Args: p.parseArgs()}
		case Dot:
			p.next()
			name := p.expect(TIdent)
			if p.at(LParen) {
				x = &MethodCall{Pos: name.Pos, Recv: x, Name: name.Text, Args: p.parseArgs()}
			} else {
				x = &FieldExpr{Pos: name.Pos, X: x, Name: name.Text}
			}
		case Question:
			p.next()
			x = &TryExpr{Pos: t.Pos, X: x}
		default:
			return x
		}
	}
}

func isUpper(s string) bool { return s != "" && s[0] >= 'A' && s[0] <= 'Z' }

func (p *parser) parsePrimary() Expr {
	t := p.tok()
	switch t.Kind {
	case TInt:
		p.next()
		v, err := strconv.ParseInt(t.Text, 10, 64)
		if err != nil {
			p.fail(t.Pos, "integer literal out of range")
		}
		return &IntLit{Pos: t.Pos, Value: v}
	case TString:
		p.next()
		return &StringLit{Pos: t.Pos, Value: t.Text}
	case KwTrue, KwFalse:
		p.next()
		return &BoolLit{Pos: t.Pos, Value: t.Kind == KwTrue}
	case LParen:
		p.next()
		if p.accept(RParen) {
			return &UnitLit{Pos: t.Pos}
		}
		saved := p.noStruct
		p.noStruct = false
		e := p.parseExpr()
		p.noStruct = saved
		p.expect(RParen)
		return e
	case LBrace:
		return p.parseBlock()
	case KwIf:
		return p.parseIf()
	case KwMatch:
		return p.parseMatch()
	case Pipe, PipePipe:
		return p.parseClosure()
	case TIdent:
		p.next()
		if p.at(ColonColon) {
			p.next()
			name := p.expect(TIdent)
			return &PathExpr{Pos: t.Pos, Type: t.Text, Name: name.Text}
		}
		if p.at(LBrace) && !p.noStruct && isUpper(t.Text) {
			return p.parseStructLit(t)
		}
		return &Ident{Pos: t.Pos, Name: t.Text}
	}
	p.fail(t.Pos, "expected expression, found %s", describe(t))
	return nil
}

func (p *parser) parseStructLit(name Token) Expr {
	p.expect(LBrace)
	lit := &StructLit{Pos: name.Pos, Name: name.Text}
	for !p.at(RBrace) {
		ft := p.expect(TIdent)
		fi := &FieldInit{Pos: ft.Pos, Name: ft.Text}
		if p.accept(Colon) {
			fi.Value = p.parseExpr()
		} else {
			fi.Value = &Ident{Pos: ft.Pos, Name: ft.Text} // shorthand `Point { x, y }`
		}
		lit.Fields = append(lit.Fields, fi)
		if !p.accept(Comma) {
			break
		}
	}
	p.expect(RBrace)
	return lit
}

func (p *parser) parseClosure() Expr {
	pos := p.tok().Pos
	c := &Closure{Pos: pos}
	if !p.accept(PipePipe) {
		p.expect(Pipe)
		for !p.at(Pipe) {
			pp := p.tok().Pos
			name := p.expect(TIdent).Text
			prm := &Param{Pos: pp, Name: name}
			if p.accept(Colon) {
				prm.Type = p.parseType()
			}
			c.Params = append(c.Params, prm)
			if !p.accept(Comma) {
				break
			}
		}
		p.expect(Pipe)
	}
	c.Body = p.parseExpr()
	return c
}

func (p *parser) parseIf() Expr {
	pos := p.expect(KwIf).Pos
	saved := p.noStruct
	p.noStruct = true
	cond := p.parseExpr()
	p.noStruct = saved
	then := p.parseBlock()
	e := &IfExpr{Pos: pos, Cond: cond, Then: then}
	if p.accept(KwElse) {
		if p.at(KwIf) {
			e.Else = p.parseIf()
		} else {
			e.Else = p.parseBlock()
		}
	}
	return e
}

func (p *parser) parseMatch() Expr {
	pos := p.expect(KwMatch).Pos
	saved := p.noStruct
	p.noStruct = true
	x := p.parseExpr()
	p.noStruct = saved
	m := &MatchExpr{Pos: pos, X: x}
	p.expect(LBrace)
	for !p.at(RBrace) {
		ap := p.tok().Pos
		pat := p.parsePattern()
		p.expect(FatArrow)
		body := p.parseExpr()
		m.Arms = append(m.Arms, &MatchArm{Pos: ap, Pat: pat, Body: body})
		if !p.accept(Comma) {
			if blockLike(body) {
				continue
			}
			break
		}
	}
	p.expect(RBrace)
	return m
}

func (p *parser) parsePattern() *Pattern {
	t := p.tok()
	pat := &Pattern{Pos: t.Pos}
	switch t.Kind {
	case Underscore:
		p.next()
		pat.Wildcard = true
		return pat
	case TInt, Minus:
		neg := p.accept(Minus)
		it := p.expect(TInt)
		v, err := strconv.ParseInt(it.Text, 10, 64)
		if err != nil {
			p.fail(it.Pos, "integer literal out of range")
		}
		if neg {
			v = -v
		}
		pat.IntValue = &v
		return pat
	case KwTrue, KwFalse:
		p.next()
		b := t.Kind == KwTrue
		pat.BoolLit = &b
		return pat
	case TString:
		p.next()
		s := t.Text
		pat.StrValue = &s
		return pat
	case TIdent:
		p.next()
		name := t.Text
		if p.accept(ColonColon) {
			pat.Type = name
			name = p.expect(TIdent).Text
			pat.Ctor = name
		}
		if p.accept(LParen) {
			pat.Ctor = name
			for !p.at(RParen) {
				pat.Args = append(pat.Args, p.parsePattern())
				if !p.accept(Comma) {
					break
				}
			}
			p.expect(RParen)
			return pat
		}
		if pat.Ctor == "" {
			pat.Bind = name // resolved by the checker: binding or nullary constructor
		}
		return pat
	}
	p.fail(t.Pos, "expected pattern, found %s", describe(t))
	return nil
}
