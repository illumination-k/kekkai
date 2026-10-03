// Package e2e compiles the programs in testdata/e2e and runs them on Node
// (WasmGC) against their *.test.mjs scenarios.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/driver"
)

func TestE2E(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found (run through `mise run test`)")
	}
	files, _ := filepath.Glob("../../testdata/e2e/*.kek")
	if len(files) == 0 {
		t.Fatal("no e2e programs")
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".kek")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			a, err := driver.Compile(string(src))
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			for n, data := range map[string]string{
				"module.wasm": string(a.Wasm), "kekkai_meta.js": a.MetaJS, "kekkai_runtime.js": a.Runtime,
			} {
				if err := os.WriteFile(filepath.Join(dir, n), []byte(data), 0o644); err != nil {
					t.Fatal(err)
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
