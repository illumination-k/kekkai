package tooling

import (
	"fmt"
	"sort"
	"strings"

	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/types"
)

// term is a type in the search language. Lowercase names and `_` in a
// query are type variables; builtins with type parameters (Option<T>
// methods, constructors) use variables too.
type term struct {
	con  string // constructor: "Int", "Option", "&Log", "()", ...
	args []*term
	v    string // non-empty for a type variable (namespaced "q:" / "c:")
}

func (t *term) String() string {
	if t.v != "" {
		s := t.v[strings.IndexByte(t.v, ':')+1:]
		if strings.HasPrefix(s, "_") {
			return "_"
		}
		return s
	}
	if len(t.args) == 0 {
		return t.con
	}
	var as []string
	for _, a := range t.args {
		as = append(as, a.String())
	}
	return t.con + "<" + strings.Join(as, ", ") + ">"
}

func con(name string, args ...*term) *term { return &term{con: name, args: args} }
func tvar(name string) *term               { return &term{v: name} }

// termOf converts a checked type (capabilities compare by kind only:
// `Log`, `&Log` match each other).
func termOf(t types.Type) *term {
	switch t := types.Prune(t).(type) {
	case *types.ResultT:
		return con("Result", termOf(t.Ok), termOf(t.Err))
	case *types.OptionT:
		return con("Option", termOf(t.Elem))
	case *types.Cap:
		return con("&" + t.Kind.String())
	case *types.Var:
		return tvar(fmt.Sprintf("c:_%d", t.ID))
	case nil:
		return con("()")
	default:
		return con(t.String())
	}
}

// Query is a parsed type signature: `A, B -> R`, `(A, B) -> R`,
// `A -> B -> R`, or a bare type `R` (match on the result type only).
type Query struct {
	Params     []*term
	Result     *term
	ResultOnly bool
	text       string
}

// ParseQuery parses a type signature query.
func ParseQuery(s string) (*Query, error) {
	toks, errs := syntax.Lex(s)
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid query %q: %s", s, errs[0].Msg)
	}
	p := &qparser{toks: toks}
	q, err := p.parse()
	if err != nil {
		return nil, fmt.Errorf("invalid query %q: %v", s, err)
	}
	q.text = s
	return q, nil
}

type qparser struct {
	toks []syntax.Token
	i    int
	anon int
}

func (p *qparser) peek() syntax.TokenKind { return p.toks[p.i].Kind }
func (p *qparser) next() syntax.Token {
	t := p.toks[p.i]
	if t.Kind != syntax.EOF {
		p.i++
	}
	return t
}

func (p *qparser) parse() (*Query, error) {
	// Split into arrow-separated segments; each segment is a comma list
	// (optionally parenthesized). `()` alone means "no parameters".
	var segs [][]*term
	for {
		seg, err := p.segment()
		if err != nil {
			return nil, err
		}
		segs = append(segs, seg)
		if p.peek() == syntax.Arrow {
			p.next()
			continue
		}
		break
	}
	if p.peek() != syntax.EOF {
		return nil, fmt.Errorf("unexpected %s", p.toks[p.i].Kind)
	}
	last := segs[len(segs)-1]
	if len(last) != 1 {
		return nil, fmt.Errorf("the result must be a single type")
	}
	q := &Query{Result: last[0], Params: []*term{}}
	if len(segs) == 1 {
		q.ResultOnly = true
		return q, nil
	}
	for _, seg := range segs[:len(segs)-1] {
		for _, t := range seg {
			if t.con == "()" && len(segs) == 2 && len(seg) == 1 {
				continue // `() -> R`: no parameters
			}
			q.Params = append(q.Params, t)
		}
	}
	return q, nil
}

func (p *qparser) segment() ([]*term, error) {
	if p.peek() == syntax.LParen {
		// `()` or `(A, B)`
		save := p.i
		p.next()
		if p.peek() == syntax.RParen {
			p.next()
			return []*term{con("()")}, nil
		}
		list, err := p.list()
		if err != nil {
			return nil, err
		}
		if p.peek() != syntax.RParen {
			p.i = save
			return nil, fmt.Errorf("expected `)`")
		}
		p.next()
		return list, nil
	}
	return p.list()
}

func (p *qparser) list() ([]*term, error) {
	var out []*term
	for {
		t, err := p.typ()
		if err != nil {
			return nil, err
		}
		out = append(out, t)
		if p.peek() != syntax.Comma {
			return out, nil
		}
		p.next()
	}
}

func (p *qparser) typ() (*term, error) {
	t := p.next()
	switch t.Kind {
	case syntax.Amp:
		inner, err := p.typ()
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(inner.con, "&") {
			return nil, fmt.Errorf("`&` applies only to capabilities")
		}
		return inner, nil
	case syntax.Underscore:
		p.anon++
		return tvar(fmt.Sprintf("q:_%d", p.anon)), nil
	case syntax.LParen:
		if p.next().Kind != syntax.RParen {
			return nil, fmt.Errorf("expected `()`")
		}
		return con("()"), nil
	case syntax.TIdent:
		name := t.Text
		if name[0] >= 'a' && name[0] <= 'z' {
			return tvar("q:" + name), nil
		}
		if IsCapName(name) {
			return con("&" + name), nil
		}
		var args []*term
		if p.peek() == syntax.Lt {
			p.next()
			for {
				a, err := p.typ()
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if p.peek() != syntax.Comma {
					break
				}
				p.next()
			}
			if p.next().Kind != syntax.Gt {
				return nil, fmt.Errorf("expected `>`")
			}
		}
		return con(name, args...), nil
	}
	return nil, fmt.Errorf("expected a type, found %s", t.Kind)
}

// ---- unification ----

type subst map[string]*term

func (s subst) walk(t *term) *term {
	for t.v != "" {
		b, ok := s[t.v]
		if !ok {
			return t
		}
		t = b
	}
	return t
}

func (s subst) occurs(v string, t *term) bool {
	t = s.walk(t)
	if t.v != "" {
		return t.v == v
	}
	for _, a := range t.args {
		if s.occurs(v, a) {
			return true
		}
	}
	return false
}

func (s subst) unify(a, b *term) bool {
	a, b = s.walk(a), s.walk(b)
	if a.v != "" && b.v != "" && a.v == b.v {
		return true
	}
	if a.v != "" {
		if s.occurs(a.v, b) {
			return false
		}
		s[a.v] = b
		return true
	}
	if b.v != "" {
		return s.unify(b, a)
	}
	if a.con != b.con || len(a.args) != len(b.args) {
		return false
	}
	for i := range a.args {
		if !s.unify(a.args[i], b.args[i]) {
			return false
		}
	}
	return true
}

func (s subst) clone() subst {
	c := subst{}
	for k, v := range s {
		c[k] = v
	}
	return c
}

// ---- candidates ----

// Candidate is a searchable function: a user function, a builtin method
// (its receiver counts as the first parameter), a builtin static, or a
// constructor.
type Candidate struct {
	Name      string
	Kind      string // "function", "method", "static", "constructor"
	Signature string
	Params    []*term
	Result    *term
	Pure      bool
	Caps      []string
	Line, Col int // user functions only
}

func builtinCandidates() []*Candidate {
	var out []*Candidate
	for _, b := range types.AllBuiltins() {
		c := &Candidate{Signature: BuiltinSignature(b), Result: termOf(b.Result), Pure: true}
		if IsStatic(b) {
			c.Kind, c.Name = "static", b.Recv+"::"+b.Name
		} else {
			c.Kind, c.Name = "method", b.Recv+"."+b.Name
			if IsCapName(b.Recv) {
				c.Params = append(c.Params, con("&"+b.Recv))
				c.Pure = false
				c.Caps = []string{"&" + b.Recv}
			} else {
				c.Params = append(c.Params, con(b.Recv))
			}
		}
		for _, p := range b.Params {
			c.Params = append(c.Params, termOf(p))
		}
		out = append(out, c)
	}
	T, E := tvar("c:T"), tvar("c:E")
	gen := func(name, sig string, params []*term, result *term, kind string) {
		out = append(out, &Candidate{Name: name, Kind: kind, Signature: sig, Params: params, Result: result, Pure: true})
	}
	opt, res := con("Option", T), con("Result", T, E)
	gen("Option.is_some", "fn Option<T>.is_some(self) -> Bool", []*term{opt}, con("Bool"), "method")
	gen("Option.is_none", "fn Option<T>.is_none(self) -> Bool", []*term{opt}, con("Bool"), "method")
	gen("Option.unwrap_or", "fn Option<T>.unwrap_or(self, T) -> T", []*term{opt, T}, T, "method")
	gen("Result.is_ok", "fn Result<T, E>.is_ok(self) -> Bool", []*term{res}, con("Bool"), "method")
	gen("Result.is_err", "fn Result<T, E>.is_err(self) -> Bool", []*term{res}, con("Bool"), "method")
	gen("Result.unwrap_or", "fn Result<T, E>.unwrap_or(self, T) -> T", []*term{res, T}, T, "method")
	gen("Some", "Some(T) -> Option<T>", []*term{T}, opt, "constructor")
	gen("None", "None : Option<T>", []*term{}, opt, "constructor")
	gen("Ok", "Ok(T) -> Result<T, E>", []*term{T}, res, "constructor")
	gen("Err", "Err(E) -> Result<T, E>", []*term{E}, res, "constructor")
	gen("Db.transaction", "fn Db.transaction(&self, |tx: Tx| -> Result<T, E>) -> Result<Result<T, E>, TxError>",
		[]*term{con("&Db")}, con("Result", res, con("TxError")), "method")
	out[len(out)-1].Pure = false
	out[len(out)-1].Caps = []string{"&Db"}
	return out
}

func userCandidates(a *Analysis) []*Candidate {
	var out []*Candidate
	if a == nil || a.Info == nil {
		return out
	}
	for _, r := range CapReports(a) {
		fn := a.Info.Funcs[r.Name]
		c := &Candidate{Name: fn.Name, Kind: "function", Signature: r.Signature, Result: termOf(fn.Result),
			Pure: fn.Pure(), Line: r.Line, Col: r.Col, Params: []*term{}}
		for _, p := range fn.Params {
			c.Params = append(c.Params, termOf(p.Type))
		}
		for _, cp := range r.Caps {
			c.Caps = append(c.Caps, cp.Type)
		}
		out = append(out, c)
	}
	// Enum variant constructors are functions too.
	names := make([]string, 0, len(a.Info.Enums))
	for n := range a.Info.Enums {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		en := a.Info.Enums[n]
		for i, v := range en.Variants {
			c := &Candidate{Name: en.Name + "::" + v.Name, Kind: "constructor", Signature: variantText(en, v), Result: con(en.Name), Pure: true, Params: []*term{}}
			if a.ix != nil && i < len(a.ix.variantPos[en]) {
				c.Line, c.Col = a.ix.variantPos[en][i].Line, a.ix.variantPos[en][i].Col
			}
			for _, f := range v.Fields {
				c.Params = append(c.Params, termOf(f))
			}
			out = append(out, c)
		}
	}
	return out
}

// Match is a search hit.
type Match struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	Signature string   `json:"signature"`
	Match     string   `json:"match"` // "exact", "reordered", "unifies", "result"
	Score     int      `json:"score"` // lower is better
	Pure      bool     `json:"pure"`
	Caps      []string `json:"caps"`
	Builtin   bool     `json:"builtin"`
	Line      int      `json:"line,omitempty"`
	Col       int      `json:"col,omitempty"`
}

// Search finds functions whose signature matches q. Capability parameters
// are part of the signature: `&Log, String -> ()` finds `Log.info`, and a
// pure query never returns a function that needs capabilities. Parameter
// order is ignored (at a small penalty) and type variables unify.
func Search(q *Query, a *Analysis) []Match {
	var out []Match
	for _, src := range []struct {
		cands   []*Candidate
		builtin bool
	}{{userCandidates(a), false}, {builtinCandidates(), true}} {
		for _, c := range src.cands {
			kind, score, ok := matchCandidate(q, c)
			if !ok {
				continue
			}
			if !src.builtin {
				score-- // prefer reusing project code
			}
			caps := c.Caps
			if caps == nil {
				caps = []string{}
			}
			out = append(out, Match{Name: c.Name, Kind: c.Kind, Signature: c.Signature, Match: kind, Score: score,
				Pure: c.Pure, Caps: caps, Builtin: src.builtin, Line: c.Line, Col: c.Col})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score < out[j].Score
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func isVar(t *term) bool { return t.v != "" }

// exactly reports structural identity without instantiating variables.
func exactly(a, b *term) bool {
	if a.v != "" || b.v != "" {
		return a.v != "" && b.v != "" && a.v[2:] == b.v[2:]
	}
	if a.con != b.con || len(a.args) != len(b.args) {
		return false
	}
	for i := range a.args {
		if !exactly(a.args[i], b.args[i]) {
			return false
		}
	}
	return true
}

func matchCandidate(q *Query, c *Candidate) (kind string, score int, ok bool) {
	if q.ResultOnly {
		if exactly(q.Result, c.Result) {
			return "result", 20, true
		}
		if !isVar(c.Result) && (subst{}).unify(q.Result, c.Result) {
			return "result", 25, true
		}
		return "", 0, false
	}
	if len(q.Params) != len(c.Params) {
		return "", 0, false
	}
	// Exact, in order.
	same := exactly(q.Result, c.Result)
	for i := range q.Params {
		same = same && exactly(q.Params[i], c.Params[i])
	}
	if same {
		return "exact", 0, true
	}
	// Unify in order, then over permutations of the parameters.
	if s := (subst{}); s.unify(q.Result, c.Result) {
		inOrder := true
		s2 := s.clone()
		for i := range q.Params {
			if !s2.unify(q.Params[i], c.Params[i]) {
				inOrder = false
				break
			}
		}
		if inOrder {
			return "unifies", 5 + generality(c), true
		}
		if len(q.Params) <= 6 && permMatch(s, q.Params, c.Params, make([]bool, len(c.Params)), 0) {
			kind := "reordered"
			score = 3
			if hasVars(q) || hasVarsC(c) {
				kind, score = "unifies", 8
			}
			return kind, score + generality(c), true
		}
	}
	return "", 0, false
}

// generality penalizes very polymorphic candidates (e.g. `Some : T -> Option<T>`)
// so that concrete matches rank first.
func generality(c *Candidate) int {
	if hasVarsC(c) {
		return 2
	}
	return 0
}

func hasVarsT(t *term) bool {
	if t.v != "" {
		return true
	}
	for _, a := range t.args {
		if hasVarsT(a) {
			return true
		}
	}
	return false
}

func hasVars(q *Query) bool {
	if hasVarsT(q.Result) {
		return true
	}
	for _, p := range q.Params {
		if hasVarsT(p) {
			return true
		}
	}
	return false
}

func hasVarsC(c *Candidate) bool {
	if hasVarsT(c.Result) {
		return true
	}
	for _, p := range c.Params {
		if hasVarsT(p) {
			return true
		}
	}
	return false
}

func permMatch(s subst, qs, cs []*term, used []bool, i int) bool {
	if i == len(qs) {
		return true
	}
	for j := range cs {
		if used[j] {
			continue
		}
		s2 := s.clone()
		if s2.unify(qs[i], cs[j]) {
			used[j] = true
			if permMatch(s2, qs, cs, used, i+1) {
				return true
			}
			used[j] = false
		}
	}
	return false
}

// String renders the normalized query.
func (q *Query) String() string {
	if q.ResultOnly {
		return q.Result.String()
	}
	var ps []string
	for _, p := range q.Params {
		ps = append(ps, p.String())
	}
	return "(" + strings.Join(ps, ", ") + ") -> " + q.Result.String()
}
