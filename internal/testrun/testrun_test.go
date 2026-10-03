package testrun

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/syntax"
)

func read(t *testing.T, path string) string {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(src)
}

func TestDiscover(t *testing.T) {
	tests, err := Discover(read(t, "../../testdata/test/counter.kek"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, tc := range tests {
		got = append(got, tc.Name+":"+tc.Result+":"+strings.Join(tc.Caps, ","))
	}
	want := "key_format:bool: parse_count_defaults_to_zero:result: visits_are_counted:result:Db,Log " +
		"greeting_falls_back_offline:bool:Net clock_is_fixed:bool:Clock random_in_range:bool:Random"
	if strings.Join(got, " ") != want {
		t.Errorf("got  %s\nwant %s", strings.Join(got, " "), want)
	}
}

// The synthesized harness must parse (a cheap guard against template
// mistakes).
func TestHarnessParses(t *testing.T) {
	h := Harness([]Test{
		{Name: "a", Caps: []string{}, Result: "unit"},
		{Name: "b", Caps: []string{"Db", "Log"}, Result: "result"},
		{Name: "c", Caps: []string{"Net"}, Result: "bool"},
	})
	if _, err := syntax.Parse(h); err != nil {
		t.Fatalf("%v\n%s", err, h)
	}
}

func needNode(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found (run through `mise run test`)")
	}
}

func TestRunPassing(t *testing.T) {
	needNode(t)
	var out strings.Builder
	failed, err := Run("counter.kek", read(t, "../../testdata/test/counter.kek"), Options{}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if failed {
		t.Fatalf("tests failed:\n%s", out.String())
	}
	for _, want := range []string{
		"running 6 tests from counter.kek",
		"test key_format ... ok (pure: hermetic, cacheable;",
		"test visits_are_counted ... ok (mock Db, Log;",
		"test result: ok. 6 passed; 0 failed",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}
}

func TestRunFailing(t *testing.T) {
	needNode(t)
	src := read(t, "../../testdata/test/failing.kek")
	var out strings.Builder
	failed, err := Run("failing.kek", src, Options{}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !failed {
		t.Fatalf("expected failures:\n%s", out.String())
	}
	for _, want := range []string{
		"test passes ... ok",
		"test returns_false ... FAILED",
		"    returned false",
		"test returns_err ... FAILED (mock Log;",
		"    Err: expected 3, got 2",
		"    info: about to fail",
		"test unit_passes ... ok",
		"test canned_net ... FAILED",
		"no canned response for GET https://api.example/x",
		"test result: FAILED. 2 passed; 3 failed",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	// canned &Net responses and -run selection
	out.Reset()
	failed, err = Run("failing.kek", src, Options{
		Run: regexp.MustCompile("^canned_net$"),
		Net: map[string]any{"GET https://api.example/x": "hello"},
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if failed || !strings.Contains(out.String(), "test result: ok. 1 passed; 0 failed") {
		t.Errorf("canned response not used:\n%s", out.String())
	}
}

// A program's own #[handler] is demoted so the harness can take its place.
func TestRunWithHandler(t *testing.T) {
	needNode(t)
	src := read(t, "../../testdata/e2e/bank.kek") + `
#[test]
fn amount_parsing() -> Bool {
    match parse_amount("12") {
        Some(n) => n == 12,
        None => false,
    }
}
`
	var out strings.Builder
	failed, err := Run("bank.kek", src, Options{}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if failed {
		t.Fatalf("failed:\n%s", out.String())
	}
}
