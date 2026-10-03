package selfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/driver"
	"github.com/illumination-k/kekkai/internal/glue"
)

// runStage runs a compiler stage laid out in dir (module.wasm,
// kekkai_meta.js, runtime, run.mjs) with the given arguments.
func runStage(t *testing.T, dir string, args ...string) (string, int) {
	cmd := exec.Command("node", append([]string{filepath.Join(dir, "run.mjs"), dir}, args...)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return out.String(), ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), 0
}

// layout turns the output of a `build` into a runnable compiler stage.
func layout(t *testing.T, outDir string) {
	for n, data := range map[string]string{"kekkai_runtime.js": glue.Runtime, "run.mjs": glue.RunMJS} {
		if err := os.WriteFile(filepath.Join(outDir, n), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestBootstrap is the self-hosting fixed point (docs/selfhost.md):
//
//	stage1 = stage0(compiler/)    the Go compiler compiles the Kekkai compiler
//	stage2 = stage1(compiler/)    must equal stage1 byte for byte
//	stage3 = stage2(compiler/)    must equal stage2 byte for byte
func TestBootstrap(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not found")
	}
	s1 := buildStage1(t)
	compilerDir, _ := filepath.Abs("../../compiler")

	s2 := t.TempDir()
	out, code := runStage(t, s1, "build", compilerDir, s2)
	if code == 2 && strings.Contains(out, "usage") {
		t.Skip("stage1 has no `build` command yet")
	}
	if code != 0 {
		t.Fatalf("stage1 build failed (%d):\n%s", code, out)
	}
	srcs, err := driver.ReadSources(compilerDir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := driver.CompileFiles(srcs)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"module.wasm": string(a.Wasm), "kekkai_meta.js": a.MetaJS} {
		got, err := os.ReadFile(filepath.Join(s2, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("stage2 %s differs from stage1 (stage0 output): %d vs %d bytes", name, len(got), len(want))
		}
	}
	layout(t, s2)

	s3 := t.TempDir()
	if out, code := runStage(t, s2, "build", compilerDir, s3); code != 0 {
		t.Fatalf("stage2 build failed (%d):\n%s", code, out)
	}
	for _, name := range []string{"module.wasm", "kekkai_meta.js"} {
		a, _ := os.ReadFile(filepath.Join(s2, name))
		b, _ := os.ReadFile(filepath.Join(s3, name))
		if !bytes.Equal(a, b) {
			t.Fatalf("stage3 %s differs from stage2", name)
		}
	}
	t.Logf("fixed point reached: stage2 == stage3 (%d bytes of wasm)", len(a.Wasm))
}
