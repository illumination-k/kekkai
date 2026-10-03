// Package testrun implements `kek test`.
//
// Tests are ordinary functions marked `#[test]`. They receive only
// capabilities, so the runner can substitute mocks for every external
// dependency (see runner.mjs). A test fails when it returns `false` or
// `Err(message)`, or traps.
//
// To run tests without new code generation, the runner synthesizes a
// `#[handler]` that dispatches on the request path to each test (any
// handler of the program itself is demoted to a plain function), compiles
// the result to WasmGC as usual, and drives it on Node.
//
// Tests that take no capabilities are pure and therefore hermetic: their
// result depends only on the definitions they reach, so it can be cached
// by definition hash. The runner reports them as cacheable; the cache
// itself is not implemented yet (TODO: key = hash of the test's transitive
// definitions + compiler version).
package testrun

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/illumination-k/kekkai/internal/driver"
	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/types"
)

//go:embed runner.mjs
var runnerJS string

// mainName is the synthesized handler.
const mainName = "__kek_test_main"

// capOrder fixes the order of the synthesized handler's capability
// parameters.
var capOrder = []string{"Log", "Clock", "Random", "Db", "Net"}

// Test describes one #[test] function.
type Test struct {
	Name   string   `json:"name"`
	Caps   []string `json:"caps"`   // capability kinds, in parameter order
	Result string   `json:"result"` // "unit", "bool" or "result"
}

// Pure reports whether the test receives no capabilities (hermetic).
func (t Test) Pure() bool { return len(t.Caps) == 0 }

// Options configure the mock capabilities.
type Options struct {
	Run   *regexp.Regexp    // select tests by name (nil = all)
	Seed  int64             // seed of the mock &Random
	Clock int64             // fixed time of the mock &Clock (ms since the epoch)
	Net   map[string]any    // canned &Net responses: "GET url" -> body or {"error": msg}
	Db    map[string]string // initial contents of the mock &Db store
}

// DefaultClock is 2026-01-01T00:00:00Z.
const DefaultClock = 1767225600000

// Discover type-checks src and returns its tests.
func Discover(src string) ([]Test, error) {
	f, err := syntax.Parse(src)
	if err != nil {
		return nil, err
	}
	info, err := types.Check(f)
	if err != nil {
		return nil, err
	}
	var tests []Test
	for _, fn := range info.FuncList {
		if !fn.Test {
			continue
		}
		t := Test{Name: fn.Name, Caps: []string{}, Result: "unit"}
		for _, p := range fn.Params {
			t.Caps = append(t.Caps, p.Type.(*types.Cap).Kind.String())
		}
		switch fn.Result.(type) {
		case *types.ResultT:
			t.Result = "result"
		default:
			if fn.Result == types.Bool {
				t.Result = "bool"
			}
		}
		tests = append(tests, t)
	}
	return tests, nil
}

// Harness returns the source of the synthesized handler that runs tests.
func Harness(tests []Test) string {
	used := map[string]bool{}
	for _, t := range tests {
		for _, c := range t.Caps {
			used[c] = true
		}
	}
	var b strings.Builder
	b.WriteString("\n// ---- synthesized by kek test ----\n#[handler]\nfn " + mainName + "(__req: Request")
	for _, c := range capOrder {
		if used[c] {
			fmt.Fprintf(&b, ", __%s: &%s", strings.ToLower(c), c)
		}
	}
	b.WriteString(") -> Response {\n    let __name = __req.path();\n")
	for _, t := range tests {
		args := make([]string, len(t.Caps))
		for i, c := range t.Caps {
			args[i] = "__" + strings.ToLower(c)
		}
		call := t.Name + "(" + strings.Join(args, ", ") + ")"
		fmt.Fprintf(&b, "    if __name == %q {\n", "/"+t.Name)
		switch t.Result {
		case "bool":
			fmt.Fprintf(&b, "        if %s {\n            return Response::text(200, \"\");\n        }\n", call)
			b.WriteString("        return Response::text(500, \"test returned false\");\n")
		case "result":
			fmt.Fprintf(&b, "        return match %s {\n", call)
			b.WriteString("            Ok(_) => Response::text(200, \"\"),\n")
			b.WriteString("            Err(__e) => Response::text(500, __e),\n        };\n")
		default:
			fmt.Fprintf(&b, "        %s;\n        return Response::text(200, \"\");\n", call)
		}
		b.WriteString("    }\n")
	}
	b.WriteString("    Response::not_found()\n}\n")
	return b.String()
}

// Compile builds the test program: src plus the synthesized handler, with
// the program's own #[handler] demoted to a plain function.
func Compile(src string, tests []Test) (*driver.Artifacts, error) {
	f, err := syntax.Parse(src + Harness(tests))
	if err != nil {
		return nil, fmt.Errorf("kek test: internal error in synthesized harness: %w", err)
	}
	for _, fd := range f.Funcs {
		if fd.Name == mainName {
			continue
		}
		var attrs []*syntax.Attr
		for _, a := range fd.Attrs {
			if a.Name != "handler" {
				attrs = append(attrs, a)
			}
		}
		fd.Attrs = attrs
	}
	return driver.CompileFile(f)
}

// Run compiles and runs the tests of the file at path on Node, writing the
// report to w. It returns failed = true when a test failed.
func Run(path, src string, opts Options, w io.Writer) (failed bool, err error) {
	tests, err := Discover(src)
	if err != nil {
		return false, err
	}
	if opts.Run != nil {
		var sel []Test
		for _, t := range tests {
			if opts.Run.MatchString(t.Name) {
				sel = append(sel, t)
			}
		}
		tests = sel
	}
	if len(tests) == 0 {
		fmt.Fprintf(w, "%s: no tests\n", path)
		return false, nil
	}
	a, err := Compile(src, tests)
	if err != nil {
		return false, err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return false, fmt.Errorf("kek test: node not found (install it with `mise install`)")
	}
	dir, err := os.MkdirTemp("", "kek-test-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	clock := opts.Clock
	if clock == 0 {
		clock = DefaultClock
	}
	plan, _ := json.Marshal(map[string]any{
		"file": path, "tests": tests, "seed": opts.Seed, "clock": clock, "net": opts.Net, "db": opts.Db,
	})
	files := map[string][]byte{
		"module.wasm":       a.Wasm,
		"kekkai_meta.js":    []byte(a.MetaJS),
		"kekkai_runtime.js": []byte(a.Runtime),
		"runner.mjs":        []byte(runnerJS),
		"plan.json":         plan,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return false, err
		}
	}
	cmd := exec.Command(node, filepath.Join(dir, "runner.mjs"), dir, filepath.Join(dir, "plan.json"))
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
			return true, nil
		}
		return true, fmt.Errorf("kek test: runner failed: %w", err)
	}
	return false, nil
}
