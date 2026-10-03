package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/driver"
	"github.com/illumination-k/kekkai/internal/glue"
)

// TestRun runs the #[main] programs in testdata/run with a temporary file
// path as the only argument and compares stdout with the .out file and the
// exit code with the `// exit: N` comment on the first line.
func TestRun(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	files, _ := filepath.Glob("../../testdata/run/*.kek")
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".kek")
		t.Run(name, func(t *testing.T) {
			src, _ := os.ReadFile(file)
			want, err := os.ReadFile(strings.TrimSuffix(file, ".kek") + ".out")
			if err != nil {
				t.Fatal(err)
			}
			a, err := driver.Compile(string(src))
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			for n, data := range map[string]string{
				"module.wasm": string(a.Wasm), "kekkai_meta.js": a.MetaJS,
				"kekkai_runtime.js": a.Runtime, "run.mjs": glue.RunMJS,
			} {
				os.WriteFile(filepath.Join(dir, n), []byte(data), 0o644)
			}
			cmd := exec.Command(node, filepath.Join(dir, "run.mjs"), dir, filepath.Join(dir, "scratch.txt"))
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			err = cmd.Run()
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			wantCode := 0
			fmt.Sscanf(string(src), "// exit: %d", &wantCode)
			if code != wantCode {
				t.Errorf("exit code %d, want %d\n%s", code, wantCode, stderr.String())
			}
			if stdout.String() != string(want) {
				t.Errorf("stdout:\n%s\nwant:\n%s\nstderr:\n%s", stdout.String(), want, stderr.String())
			}
		})
	}
}
