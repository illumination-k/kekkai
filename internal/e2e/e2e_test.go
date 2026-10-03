// Package e2e compiles the programs in testdata/e2e and examples/ and runs
// them on Node (WasmGC) against their *.test.mjs scenarios, checks the
// store adapters against a conformance suite, and (when wrangler is
// installed) runs a compiled program on workerd; see workers_test.go.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/driver"
)

func nodePath(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found (run through `mise run test`)")
	}
	return node
}

// programs lists the e2e programs: every testdata/e2e/*.kek and every
// examples/*/*.kek that has a sibling *.test.mjs.
func programs(t *testing.T) []string {
	t.Helper()
	files, _ := filepath.Glob("../../testdata/e2e/*.kek")
	ex, _ := filepath.Glob("../../examples/*/*.kek")
	for _, f := range ex {
		if _, err := os.Stat(strings.TrimSuffix(f, ".kek") + ".test.mjs"); err == nil {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		t.Fatal("no e2e programs")
	}
	return files
}

// compileTo compiles a .kek file and writes the artifacts into dir.
func compileTo(t *testing.T, file, dir string) *driver.Artifacts {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	a, err := driver.Compile(string(src))
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	files := map[string]string{
		"module.wasm": string(a.Wasm), "kekkai_meta.js": a.MetaJS, "kekkai_runtime.js": a.Runtime,
	}
	if a.WorkerJS != "" {
		files["worker.js"] = a.WorkerJS
	}
	for n, data := range files {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func TestE2E(t *testing.T) {
	node := nodePath(t)
	for _, file := range programs(t) {
		name := strings.TrimSuffix(filepath.Base(file), ".kek")
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if a := compileTo(t, file, dir); a.WorkerJS != "" {
				if out, err := exec.Command(node, "--check", filepath.Join(dir, "worker.js")).CombinedOutput(); err != nil {
					t.Fatalf("generated worker.js does not parse: %v\n%s", err, out)
				}
			}
			testFile := strings.TrimSuffix(file, ".kek") + ".test.mjs"
			out, err := exec.Command(node, "harness.mjs", dir, testFile).CombinedOutput()
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
		})
	}
}

// TestAdapters runs the store adapter conformance suite (MemoryStore,
// D1KvStore on a node:sqlite D1, DurableObjectStore, RemoteKvStore).
func TestAdapters(t *testing.T) {
	node := nodePath(t)
	out, err := exec.Command(node, "adapters.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Logf("%s", out)
}

// TestBadExamples checks that the *.bad.kek files under examples/ are
// rejected with the diagnostic named in their first line
// (`// kek check: <substring>`).
func TestBadExamples(t *testing.T) {
	files, _ := filepath.Glob("../../examples/*/*.bad.kek")
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			first, _, _ := strings.Cut(string(src), "\n")
			want, ok := strings.CutPrefix(first, "// kek check: ")
			if !ok {
				t.Fatalf("first line must be `// kek check: <expected error>`")
			}
			_, err = driver.Check(string(src))
			if err == nil {
				t.Fatalf("expected a type error containing %q", want)
			}
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("expected error containing %q, got:\n%v", want, err)
			}
		})
	}
}
