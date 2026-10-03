package syntax

// File is a parsed .kek source file.
type File struct {
	Structs []*StructDecl
	Enums   []*EnumDecl
	Funcs   []*FuncDecl
}

// ---- types ----

// TypeExpr is a type as written in source.
type TypeExpr interface{ typeExpr() }

// NamedType is `Name` or `Name<Args...>`.
type NamedType struct {
	Pos  Pos
	Name string
	Args []TypeExpr
}

// RefType is `&T`: a capability borrowed for the duration of a call.
type RefType struct {
	Pos  Pos
	Elem TypeExpr
}

// UnitType is `()`.
type UnitType struct{ Pos Pos }

func (*NamedType) typeExpr() {}
func (*RefType) typeExpr()   {}
func (*UnitType) typeExpr()  {}

// TypePos returns the position of a type expression.
func TypePos(t TypeExpr) Pos {
	switch t := t.(type) {
	case *NamedType:
		return t.Pos
	case *RefType:
		return t.Pos
	case *UnitType:
		return t.Pos
	}
	return Pos{}
}

// ---- declarations ----

type Attr struct {
	Pos  Pos
	Name string
}

type Field struct {
	Pos  Pos
	Name string
	Type TypeExpr
}

type StructDecl struct {
	Pos    Pos
	Name   string
	Fields []*Field
}

type Variant struct {
	Pos    Pos
	Name   string
	Fields []TypeExpr
}

type EnumDecl struct {
	Pos      Pos
	Name     string
	Variants []*Variant
}

type Param struct {
	Pos  Pos
	Name string
	Type TypeExpr
}

type FuncDecl struct {
	Pos    Pos
	Attrs  []*Attr
	Name   string
	Params []*Param
	Result TypeExpr // nil means ()
	Body   *Block
}

func (f *FuncDecl) HasAttr(name string) bool {
	for _, a := range f.Attrs {
		if a.Name == name {
			return true
		}
	}
	return false
}

// ---- statements ----

type Stmt interface{ stmt() }

type LetStmt struct {
	Pos  Pos
	Mut  bool
	Name string
	Type TypeExpr // optional
	Init Expr
}

type AssignStmt struct {
	Pos   Pos
	Name  string
	Value Expr
}

type WhileStmt struct {
	Pos  Pos
	Cond Expr
	Body *Block
}

type ReturnStmt struct {
	Pos   Pos
	Value Expr // optional
}

type ExprStmt struct {
	Pos  Pos
	X    Expr
	Semi bool
}

func (*LetStmt) stmt()    {}
func (*AssignStmt) stmt() {}
func (*WhileStmt) stmt()  {}
func (*ReturnStmt) stmt() {}
func (*ExprStmt) stmt()   {}

// ---- expressions ----

type Expr interface{ exprPos() Pos }

type IntLit struct {
	Pos   Pos
	Value int64
}

type BoolLit struct {
	Pos   Pos
	Value bool
}

type StringLit struct {
	Pos   Pos
	Value string
}

type UnitLit struct{ Pos Pos }

type Ident struct {
	Pos  Pos
	Name string
}

type BinaryExpr struct {
	Pos  Pos
	Op   TokenKind
	X, Y Expr
}

type UnaryExpr struct {
	Pos Pos
	Op  TokenKind // Minus or Bang
	X   Expr
}

// CallExpr is `f(args)` where f is a function name or a constructor
// (Ok, Err, Some) or a path `Type::name`.
type CallExpr struct {
	Pos  Pos
	Func Expr // *Ident or *PathExpr
	Args []Expr
}

// MethodCall is `recv.name(args)`.
type MethodCall struct {
	Pos  Pos
	Recv Expr
	Name string
	Args []Expr
}

// FieldExpr is `x.name`.
type FieldExpr struct {
	Pos  Pos
	X    Expr
	Name string
}

// PathExpr is `Type::Name`.
type PathExpr struct {
	Pos  Pos
	Type string
	Name string
}

type FieldInit struct {
	Pos   Pos
	Name  string
	Value Expr
}

type StructLit struct {
	Pos    Pos
	Name   string
	Fields []*FieldInit
}

type IfExpr struct {
	Pos  Pos
	Cond Expr
	Then *Block
	Else Expr // nil, *Block or *IfExpr
}

// Pattern in a match arm.
type Pattern struct {
	Pos      Pos
	Wildcard bool     // `_`
	Bind     string   // a bare identifier binding the whole value (non-empty when not a constructor)
	Type     string   // `Type::Variant` qualifier (optional)
	Ctor     string   // constructor / variant name (Ok, Err, Some, None, Variant)
	Args     []string // bound names for constructor fields ("_" for ignored)
	IntValue *int64   // integer literal pattern
	BoolLit  *bool    // boolean literal pattern
	StrValue *string  // string literal pattern
}

type MatchArm struct {
	Pos  Pos
	Pat  *Pattern
	Body Expr
}

type MatchExpr struct {
	Pos  Pos
	X    Expr
	Arms []*MatchArm
}

type Block struct {
	Pos   Pos
	Stmts []Stmt
	Tail  Expr // optional final expression
	End   Pos
}

// TryExpr is `x?`.
type TryExpr struct {
	Pos Pos
	X   Expr
}

// Closure is `|params| body`. Closures are second-class: they may only
// appear as the argument of a capability method such as `transaction`.
type Closure struct {
	Pos    Pos
	Params []*Param // types optional
	Body   Expr
}

func (e *IntLit) exprPos() Pos     { return e.Pos }
func (e *BoolLit) exprPos() Pos    { return e.Pos }
func (e *StringLit) exprPos() Pos  { return e.Pos }
func (e *UnitLit) exprPos() Pos    { return e.Pos }
func (e *Ident) exprPos() Pos      { return e.Pos }
func (e *BinaryExpr) exprPos() Pos { return e.Pos }
func (e *UnaryExpr) exprPos() Pos  { return e.Pos }
func (e *CallExpr) exprPos() Pos   { return e.Pos }
func (e *MethodCall) exprPos() Pos { return e.Pos }
func (e *FieldExpr) exprPos() Pos  { return e.Pos }
func (e *PathExpr) exprPos() Pos   { return e.Pos }
func (e *StructLit) exprPos() Pos  { return e.Pos }
func (e *IfExpr) exprPos() Pos     { return e.Pos }
func (e *MatchExpr) exprPos() Pos  { return e.Pos }
func (e *Block) exprPos() Pos      { return e.Pos }
func (e *TryExpr) exprPos() Pos    { return e.Pos }
func (e *Closure) exprPos() Pos    { return e.Pos }

// ExprPos returns the position of an expression.
func ExprPos(e Expr) Pos { return e.exprPos() }
