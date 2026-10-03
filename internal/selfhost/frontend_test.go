package selfhost_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/illumination-k/kekkai/internal/driver"
	"github.com/illumination-k/kekkai/internal/glue"
	"github.com/illumination-k/kekkai/internal/selfhost"
)

// The self-hosted compiler (compiler/, stage1) is compiled once with the Go
// compiler (stage0) and run on Node; its output must match stage0 byte for
// byte (docs/selfhost.md).

var (
	node      string
	stage1Dir string
	buildErr  error
)

func TestMain(m *testing.M) {
	code := func() int {
		var err error
		node, err = exec.LookPath("node")
		if err != nil {
			return m.Run() // tests skip
		}
		dir, err := os.MkdirTemp("", "kek-stage1-")
		if err != nil {
			buildErr = err
			return m.Run()
		}
		defer os.RemoveAll(dir)
		stage1Dir = dir
		buildErr = buildStage1(dir)
		return m.Run()
	}()
	os.Exit(code)
}

func buildStage1(dir string) error {
	srcs, err := driver.ReadSources("../../compiler")
	if err != nil {
		return err
	}
	a, err := driver.CompileFiles(srcs)
	if err != nil {
		return fmt.Errorf("compiling compiler/: %w", err)
	}
	for n, data := range map[string]string{
		"module.wasm": string(a.Wasm), "kekkai_meta.js": a.MetaJS,
		"kekkai_runtime.js": a.Runtime, "run.mjs": glue.RunMJS,
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(data), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// stage1 runs the self-hosted compiler and returns its combined output and
// exit code.
func stage1(t *testing.T, args ...string) (string, int) {
	t.Helper()
	if node == "" {
		t.Skip("node not found")
	}
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	cmd := exec.Command(node, append([]string{filepath.Join(stage1Dir, "run.mjs"), stage1Dir}, args...)...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), code
}

// corpus returns every .kek file of the test data and the compiler itself.
func corpus(t *testing.T) []string {
	var files []string
	for _, pat := range []string{"../../testdata/*/*.kek", "../../compiler/*.kek", "../../lean/test/*.kek"} {
		m, _ := filepath.Glob(pat)
		files = append(files, m...)
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatal("empty corpus")
	}
	return files
}

func compareDump(t *testing.T, sub string, dump func(string) (string, bool)) {
	for _, file := range corpus(t) {
		name, _ := filepath.Rel("../..", file)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			want, ok := dump(string(src))
			got, code := stage1(t, sub, file)
			if got != want {
				t.Errorf("stage1 %s output differs from stage0:\n%s", sub, firstDiff(want, got))
			}
			if (code == 0) != ok {
				t.Errorf("exit code %d, stage0 ok=%v", code, ok)
			}
		})
	}
}

func TestTokens(t *testing.T) { compareDump(t, "lex", selfhost.Tokens) }

func TestAST(t *testing.T) { compareDump(t, "ast", selfhost.AST) }

// stage0Check mirrors `kek check <path>`: the diagnostics (exit 1) or
// "<path>: ok".
func stage0Check(path string) (string, bool) {
	srcs, err := driver.ReadSources(path)
	if err == nil {
		_, err = driver.CheckFiles(srcs)
	}
	if err != nil {
		return err.Error() + "\n", false
	}
	return path + ": ok\n", true
}

func TestCheck(t *testing.T) {
	paths := append(corpus(t), "../../compiler")
	for _, path := range paths {
		name, _ := filepath.Rel("../..", path)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want, ok := stage0Check(path)
			got, code := stage1(t, "check", path)
			if got != want {
				t.Errorf("stage1 check output differs from stage0:\n%s", firstDiff(want, got))
			}
			if (code == 0) != ok {
				t.Errorf("exit code %d, stage0 ok=%v", code, ok)
			}
		})
	}
}

// firstDiff shows the first differing line of two outputs.
func firstDiff(want, got string) string {
	wl := bytes.Split([]byte(want), []byte("\n"))
	gl := bytes.Split([]byte(got), []byte("\n"))
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g []byte
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if !bytes.Equal(w, g) {
			return fmt.Sprintf("line %d:\n  stage0: %q\n  stage1: %q", i+1, w, g)
		}
	}
	return "(identical lines)"
}
