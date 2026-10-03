package syntax

import (
	"fmt"
	"sort"
	"strings"
)

// Error is a diagnostic attached to a source position.
type Error struct {
	Pos Pos
	Msg string
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Pos, e.Msg) }

// ErrorList is a list of diagnostics, sorted by position when reported.
type ErrorList []*Error

func (l *ErrorList) Add(pos Pos, format string, args ...any) {
	*l = append(*l, &Error{Pos: pos, Msg: fmt.Sprintf(format, args...)})
}

func (l ErrorList) Err() error {
	if len(l) == 0 {
		return nil
	}
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].Pos.File != l[j].Pos.File {
			return l[i].Pos.File < l[j].Pos.File
		}
		if l[i].Pos.Line != l[j].Pos.Line {
			return l[i].Pos.Line < l[j].Pos.Line
		}
		return l[i].Pos.Col < l[j].Pos.Col
	})
	return l
}

func (l ErrorList) Error() string {
	var b strings.Builder
	for i, e := range l {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

// Lex converts source text into tokens. The final token is always EOF.
func Lex(src string) ([]Token, ErrorList) { return LexFile("", src) }

// LexFile is Lex with a file name recorded in every position.
func LexFile(file, src string) ([]Token, ErrorList) {
	lx := &lexer{src: src, line: 1, col: 1, file: file}
	lx.run()
	return lx.toks, lx.errs
}

// Comment is a `//` line comment or a `/* */` block comment. The parser
// ignores comments; tools that need them (the formatter) collect them with
// LexWithComments and re-attach them by position.
type Comment struct {
	Pos  Pos
	Text string // including the `//` or `/* */` delimiters, without the newline
}

// LexWithComments is Lex that also returns the comments, in source order.
func LexWithComments(src string) ([]Token, []Comment, ErrorList) {
	lx := &lexer{src: src, line: 1, col: 1, keepComments: true}
	lx.run()
	return lx.toks, lx.comments, lx.errs
}

type lexer struct {
	src       string
	off       int
	line, col int
	file      string
	toks      []Token
	errs      ErrorList

	keepComments bool
	comments     []Comment
}

func (lx *lexer) comment(pos Pos, start int) {
	if lx.keepComments {
		lx.comments = append(lx.comments, Comment{Pos: pos, Text: strings.TrimRight(lx.src[start:lx.off], "\r")})
	}
}

func (lx *lexer) peek(n int) byte {
	if lx.off+n < len(lx.src) {
		return lx.src[lx.off+n]
	}
	return 0
}

func (lx *lexer) advance() byte {
	c := lx.src[lx.off]
	lx.off++
	if c == '\n' {
		lx.line++
		lx.col = 1
	} else {
		lx.col++
	}
	return c
}

func (lx *lexer) emit(k TokenKind, text string, pos Pos) {
	lx.toks = append(lx.toks, Token{Kind: k, Text: text, Pos: pos})
}

func isIdentStart(c byte) bool {
	return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

var twoCharOps = map[string]TokenKind{
	"::": ColonColon, "->": Arrow, "=>": FatArrow, "&&": AmpAmp, "||": PipePipe,
	"==": Eq, "!=": Ne, "<=": Le, ">=": Ge, "..": DotDot,
}

var oneCharOps = map[byte]TokenKind{
	'(': LParen, ')': RParen, '{': LBrace, '}': RBrace, '[': LBracket, ']': RBracket,
	',': Comma, ';': Semi, ':': Colon, '.': Dot, '&': Amp, '|': Pipe, '?': Question,
	'#': Hash, '!': Bang, '=': Assign, '<': Lt, '>': Gt, '+': Plus, '-': Minus,
	'*': Star, '/': Slash, '%': Percent,
}

func (lx *lexer) run() {
	for lx.off < len(lx.src) {
		c := lx.peek(0)
		pos := Pos{Line: lx.line, Col: lx.col, File: lx.file}
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			lx.advance()
		case c == '/' && lx.peek(1) == '/':
			start := lx.off
			for lx.off < len(lx.src) && lx.peek(0) != '\n' {
				lx.advance()
			}
			lx.comment(pos, start)
		case c == '/' && lx.peek(1) == '*':
			start := lx.off
			lx.advance()
			lx.advance()
			for lx.off < len(lx.src) && !(lx.peek(0) == '*' && lx.peek(1) == '/') {
				lx.advance()
			}
			if lx.off >= len(lx.src) {
				lx.errs.Add(pos, "unterminated block comment")
				break
			}
			lx.advance()
			lx.advance()
			lx.comment(pos, start)
		case isIdentStart(c):
			start := lx.off
			for lx.off < len(lx.src) && (isIdentStart(lx.peek(0)) || isDigit(lx.peek(0))) {
				lx.advance()
			}
			word := lx.src[start:lx.off]
			if k, ok := keywords[word]; ok {
				lx.emit(k, word, pos)
			} else {
				lx.emit(TIdent, word, pos)
			}
		case isDigit(c):
			start := lx.off
			for lx.off < len(lx.src) && (isDigit(lx.peek(0)) || lx.peek(0) == '_') {
				lx.advance()
			}
			lx.emit(TInt, strings.ReplaceAll(lx.src[start:lx.off], "_", ""), pos)
		case c == '"':
			lx.lexString(pos)
		default:
			if lx.off+1 < len(lx.src) {
				if k, ok := twoCharOps[lx.src[lx.off:lx.off+2]]; ok {
					lx.advance()
					lx.advance()
					lx.emit(k, "", pos)
					continue
				}
			}
			if k, ok := oneCharOps[c]; ok {
				lx.advance()
				lx.emit(k, "", pos)
				continue
			}
			lx.errs.Add(pos, "unexpected character %q", c)
			lx.advance()
		}
	}
	lx.emit(EOF, "", Pos{Line: lx.line, Col: lx.col, File: lx.file})
}

func (lx *lexer) lexString(pos Pos) {
	lx.advance() // opening quote
	var b strings.Builder
	for {
		if lx.off >= len(lx.src) || lx.peek(0) == '\n' {
			lx.errs.Add(pos, "unterminated string literal")
			break
		}
		c := lx.advance()
		if c == '"' {
			break
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if lx.off >= len(lx.src) {
			continue
		}
		e := lx.advance()
		switch e {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		case '"':
			b.WriteByte('"')
		case '0':
			b.WriteByte(0)
		default:
			lx.errs.Add(Pos{Line: lx.line, Col: lx.col - 2, File: lx.file}, "unknown escape sequence \\%c", e)
		}
	}
	lx.emit(TString, b.String(), pos)
}
