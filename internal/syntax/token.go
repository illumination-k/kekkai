// Package syntax implements the lexer, AST and parser for the Kekkai
// surface language (Rust-like syntax, file extension .kek).
package syntax

import "fmt"

// Pos is a source position (1-based line and column).
type Pos struct {
	Line, Col int
}

func (p Pos) String() string { return fmt.Sprintf("%d:%d", p.Line, p.Col) }

type TokenKind int

const (
	EOF TokenKind = iota
	TIdent
	TInt
	TString

	// keywords
	KwFn
	KwLet
	KwMut
	KwIf
	KwElse
	KwMatch
	KwWhile
	KwReturn
	KwTrue
	KwFalse
	KwStruct
	KwEnum

	// punctuation
	LParen
	RParen
	LBrace
	RBrace
	LBracket
	RBracket
	Comma
	Semi
	Colon
	ColonColon
	Dot
	Arrow    // ->
	FatArrow // =>
	Amp      // &
	AmpAmp   // &&
	Pipe     // |
	PipePipe // ||
	Question // ?
	Hash     // #
	Bang     // !
	Assign   // =
	Eq       // ==
	Ne       // !=
	Lt
	Le
	Gt
	Ge
	Plus
	Minus
	Star
	Slash
	Percent
	Underscore
)

var kindNames = map[TokenKind]string{
	EOF: "end of file", TIdent: "identifier", TInt: "integer literal", TString: "string literal",
	KwFn: "`fn`", KwLet: "`let`", KwMut: "`mut`", KwIf: "`if`", KwElse: "`else`", KwMatch: "`match`",
	KwWhile: "`while`", KwReturn: "`return`", KwTrue: "`true`", KwFalse: "`false`",
	KwStruct: "`struct`", KwEnum: "`enum`",
	LParen: "`(`", RParen: "`)`", LBrace: "`{`", RBrace: "`}`", LBracket: "`[`", RBracket: "`]`",
	Comma: "`,`", Semi: "`;`", Colon: "`:`", ColonColon: "`::`", Dot: "`.`", Arrow: "`->`",
	FatArrow: "`=>`", Amp: "`&`", AmpAmp: "`&&`", Pipe: "`|`", PipePipe: "`||`", Question: "`?`",
	Hash: "`#`", Bang: "`!`", Assign: "`=`", Eq: "`==`", Ne: "`!=`", Lt: "`<`", Le: "`<=`",
	Gt: "`>`", Ge: "`>=`", Plus: "`+`", Minus: "`-`", Star: "`*`", Slash: "`/`", Percent: "`%`",
	Underscore: "`_`",
}

func (k TokenKind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("token(%d)", int(k))
}

var keywords = map[string]TokenKind{
	"fn": KwFn, "let": KwLet, "mut": KwMut, "if": KwIf, "else": KwElse, "match": KwMatch,
	"while": KwWhile, "return": KwReturn, "true": KwTrue, "false": KwFalse,
	"struct": KwStruct, "enum": KwEnum, "_": Underscore,
}

type Token struct {
	Kind TokenKind
	Text string // identifier name, decoded string literal, or integer digits
	Pos  Pos
}
