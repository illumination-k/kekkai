package tooling

import (
	"os"
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/syntax"
)

const sample = `struct Point { x: Int, y: Int }

enum Shape {
    Circle(Int),
    Rect(Point),
}

fn area(s: Shape) -> Int {
    match s {
        Shape::Circle(r) => 3 * r * r,
        Shape::Rect(p) => p.x * p.y,
    }
}

fn greet(log: &Log, clock: &Clock, name: String) -> Int {
    let msg = "日本語 " + name;
    log.info(msg);
    msg.len()
}
`

// posOf returns the position of the n-th (0-based) occurrence of needle
// plus off bytes.
func posOf(t *testing.T, src, needle string, n, off int) syntax.Pos {
	t.Helper()
	idx := -1
	from := 0
	for i := 0; i <= n; i++ {
		j := strings.Index(src[from:], needle)
		if j < 0 {
			t.Fatalf("needle %q #%d not found", needle, n)
		}
		idx = from + j
		from = idx + 1
	}
	return NewDoc(src).PosAt(idx + off)
}

func TestUTF16Mapping(t *testing.T) {
	src := "let s = \"日本😀\" + name;\nx"
	d := NewDoc(src)
	// `name` starts after 3+2*3(日本)+4(😀) bytes in the string literal.
	bytePos := d.PosAt(strings.Index(src, "name"))
	line, ch := d.ToLSP(bytePos)
	// UTF-16: `let s = "` (9) + 日本 (2) + 😀 (2) + `" + ` (4) = 17
	if line != 0 || ch != 17 {
		t.Fatalf("ToLSP = %d:%d, want 0:17", line, ch)
	}
	if back := d.FromLSP(line, ch); back != bytePos {
		t.Fatalf("FromLSP = %v, want %v", back, bytePos)
	}
	if p := d.FromLSP(1, 99); p != (syntax.Pos{Line: 2, Col: 2}) {
		t.Fatalf("clamp = %v", p)
	}
	crlf := NewDoc("ab\r\ncd")
	if crlf.Line(0) != "ab" || crlf.Line(1) != "cd" {
		t.Fatalf("CRLF lines: %q %q", crlf.Line(0), crlf.Line(1))
	}
}

func TestDiagnostics(t *testing.T) {
	a := Analyze(sample)
	var warns []string
	for _, d := range a.Diags {
		if d.Severity == "error" {
			t.Errorf("unexpected error %v: %s", d.Pos, d.Message)
		} else {
			warns = append(warns, d.Message)
		}
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "`clock: &Clock`") {
		t.Fatalf("want one unused-capability warning, got %q", warns)
	}

	a = Analyze("fn f() -> Int {\n    let x = ;\n}\n")
	if a.ParseOK || len(a.Errors()) != 1 || a.Diags[0].Phase != "parse" || a.Diags[0].Pos != (syntax.Pos{Line: 2, Col: 13}) {
		t.Fatalf("parse diagnostics: %+v", a.Diags)
	}
	a = Analyze("fn f() -> Int {\n    \"no\"\n}\n")
	if len(a.Diags) != 1 || a.Diags[0].Phase != "type" || a.Diags[0].End.Col-a.Diags[0].Pos.Col != 4 {
		t.Fatalf("type diagnostics: %+v", a.Diags)
	}
}

func TestHover(t *testing.T) {
	a := Analyze(sample)
	cases := []struct {
		needle string
		n, off int
		want   []string
	}{
		{"greet", 0, 2, []string{"fn greet(log: &Log, clock: &Clock, name: String) -> Int", "**capabilities:**", "(unused)"}},
		{"area", 0, 0, []string{"fn area(s: Shape) -> Int", "**pure**"}},
		{"msg", 1, 1, []string{"let msg: String"}},           // use
		{"log.info", 0, 0, []string{"capability log: &Log"}}, // capability use
		{"info", 0, 1, []string{"fn Log.info(&self, String)", "effect: performs `Log`"}},
		{"len", 0, 0, []string{"fn String.len(self) -> Int", "pure"}},
		{"Shape::Circle", 0, 8, []string{"Shape::Circle(Int)"}},
		{"p.x", 0, 2, []string{"field Point.x: Int"}},
		{"Point)", 0, 0, []string{"struct Point {"}},
		{"r) =>", 0, 0, []string{"let r: Int"}},
		{"Log,", 0, 0, []string{"capability Log", "info"}},
	}
	for _, c := range cases {
		p := posOf(t, sample, c.needle, c.n, c.off)
		text, _, _, ok := a.Hover(p)
		if !ok {
			t.Errorf("hover %q: nothing", c.needle)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("hover %q = %q, missing %q", c.needle, text, w)
			}
		}
	}
	// The cursor right after a name still resolves.
	if _, _, _, ok := a.Hover(posOf(t, sample, "msg.len", 0, 3)); !ok {
		t.Errorf("hover at end of identifier failed")
	}
}

func TestDefinition(t *testing.T) {
	a := Analyze(sample)
	cases := []struct {
		needle string
		n, off int
		def    string
		dn     int
	}{
		{"msg", 1, 0, "msg", 0},              // local
		{"name;", 0, 0, "name", 0},           // param (first `name` is in the signature)
		{"Shape::Circle", 0, 8, "Circle", 0}, // variant
		{"Shape::Rect", 0, 0, "Shape", 0},    // enum
		{"Rect(Point)", 0, 5, "Point", 0},    // struct
		{"p.x", 0, 2, "x", 0},                // field
		{"r * r", 0, 0, "r) =>", 0},          // pattern binding
		{"p.x", 0, 0, "p) =>", 0},            // pattern binding
	}
	for _, c := range cases {
		start, _, ok := a.Definition(posOf(t, sample, c.needle, c.n, c.off))
		want := posOf(t, sample, c.def, c.dn, 0)
		if !ok || start != want {
			t.Errorf("definition of %q = %v (%v), want %v", c.needle, start, ok, want)
		}
	}
	src := "fn helper() -> Int { 1 }\nfn main2() -> Int { helper() + 1 }\n"
	a = Analyze(src)
	start, _, ok := a.Definition(posOf(t, src, "helper()", 1, 0))
	if !ok || start != (syntax.Pos{Line: 1, Col: 4}) {
		t.Errorf("function definition = %v %v", start, ok)
	}
}

func TestDefinitionTransaction(t *testing.T) {
	src, err := os.ReadFile("../../testdata/e2e/bank.kek")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	a := Analyze(s)
	if len(a.Errors()) != 0 {
		t.Fatalf("bank.kek: %+v", a.Errors())
	}
	start, _, ok := a.Definition(posOf(t, s, "tx.rollback", 0, 0))
	if !ok || start != posOf(t, s, "tx| {", 0, 0) {
		t.Errorf("tx definition = %v %v", start, ok)
	}
	text, _, _, _ := a.Hover(posOf(t, s, "transaction(", 0, 0))
	if !strings.Contains(text, "Db.transaction") {
		t.Errorf("transaction hover = %q", text)
	}
	text, _, _, _ = a.Hover(posOf(t, s, "unwrap_or(0)", 0, 0))
	if !strings.Contains(text, "Option<Int>.unwrap_or(self, default: Int) -> Int") {
		t.Errorf("unwrap_or hover = %q", text)
	}
	text, _, _, _ = a.Hover(posOf(t, s, "fn handle", 0, 3))
	if !strings.Contains(text, "#[handler]") || !strings.Contains(text, "**async**") {
		t.Errorf("handler hover = %q", text)
	}
}

func TestHoverWithErrors(t *testing.T) {
	// Hover keeps working on complete items while a later item is broken.
	src := "fn ok1(s: String) -> Int { s.len() }\nfn broken( {\n"
	a := Analyze(src)
	if a.ParseOK {
		t.Fatal("expected a parse error")
	}
	text, _, _, ok := a.Hover(posOf(t, src, "ok1", 0, 0))
	if !ok || !strings.Contains(text, "fn ok1(s: String) -> Int") {
		t.Errorf("hover = %q", text)
	}
}

func TestSymbols(t *testing.T) {
	syms := Analyze(sample).Symbols()
	var names []string
	for _, s := range syms {
		names = append(names, s.Kind+":"+s.Name)
	}
	if got := strings.Join(names, " "); got != "struct:Point enum:Shape function:area function:greet" {
		t.Fatalf("symbols = %s", got)
	}
	if len(syms[1].Children) != 2 || syms[1].End.Line != 6 {
		t.Errorf("enum symbol = %+v", syms[1])
	}
	if !strings.Contains(syms[2].Detail, "[pure]") {
		t.Errorf("area detail = %q", syms[2].Detail)
	}
}

func TestCapReports(t *testing.T) {
	rs := CapReports(Analyze(sample))
	if len(rs) != 2 {
		t.Fatalf("reports = %+v", rs)
	}
	g := rs[1]
	if g.Name != "greet" || g.Pure || len(g.Caps) != 2 || len(g.UnusedCaps) != 1 || g.UnusedCaps[0] != "clock" ||
		strings.Join(g.DirectEffects, ",") != "log.info" || g.Line != 15 || g.Col != 4 {
		t.Errorf("greet report = %+v", g)
	}
	if !rs[0].Pure || rs[0].Async {
		t.Errorf("area report = %+v", rs[0])
	}
}

func search(t *testing.T, q string, a *Analysis) []Match {
	t.Helper()
	pq, err := ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	return Search(pq, a)
}

func names(ms []Match) string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Name+"/"+m.Match)
	}
	return strings.Join(out, " ")
}

func TestSearch(t *testing.T) {
	src := `fn parse_age(s: String) -> Option<Int> { s.parse_int() }
fn log_twice(name: String, log: &Log) { log.info(name); log.info(name); }
fn pick(n: Int, s: String) -> String { s }
`
	a := Analyze(src)
	cases := []struct{ q, want string }{
		{"String -> Option<Int>", "parse_age/exact String.parse_int/exact"},
		// argument order does not matter; capabilities are part of the signature
		{"&Log, String -> ()", "Log.error/exact Log.info/exact Log.warn/exact log_twice/reordered"},
		{"Log -> String -> ()", "Log.error/exact Log.info/exact Log.warn/exact log_twice/reordered"},
		{"(String, Int) -> String", "pick/reordered"},
		// type variables unify
		{"Option<a> -> a -> a", "Option.unwrap_or/unifies"},
		{"Option<Int> -> Int -> Int", "Option.unwrap_or/unifies"},
		{"a -> Option<a>", "Some/unifies"},
		// bare type: result only
		{"Option<Int>", "parse_age/result String.char_at/result String.index_of/result String.parse_int/result None/result Some/result"},
		// a pure query never returns capability methods such as Clock.now_ms
		{"() -> Int", ""},
	}
	for _, c := range cases {
		got := names(search(t, c.q, a))
		if got != c.want {
			t.Errorf("search %q = %s, want %s", c.q, got, c.want)
		}
	}
	if got := names(search(t, "Clock -> Int", nil)); got != "Clock.now_ms/exact" {
		t.Errorf("Clock -> Int = %s", got)
	}
	for _, bad := range []string{"String ->", "&Int -> ()", "A, B", "Option<Int"} {
		if _, err := ParseQuery(bad); err == nil {
			t.Errorf("ParseQuery(%q) succeeded", bad)
		}
	}
}
