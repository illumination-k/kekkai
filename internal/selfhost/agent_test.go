package selfhost_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"
)

// The agent tooling commands (caps, check -json, search, ir -json) and the
// Workers glue of build must match the Go toolchain (cmd/kek) byte for
// byte. stage0 here is the kek binary built from cmd/kek.

var (
	kek0Once sync.Once
	kek0Path string
	kek0Err  error
)

func kek0(t *testing.T) string {
	t.Helper()
	kek0Once.Do(func() {
		dir, err := os.MkdirTemp("", "kek-stage0-")
		if err != nil {
			kek0Err = err
			return
		}
		kek0Path = filepath.Join(dir, "kek")
		out, err := exec.Command("go", "build", "-o", kek0Path, "../../cmd/kek").CombinedOutput()
		if err != nil {
			kek0Err = &buildError{out: string(out), err: err}
		}
	})
	if kek0Err != nil {
		t.Fatal(kek0Err)
	}
	return kek0Path
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

// stage0 runs the Go kek binary and returns its combined output and exit code.
func stage0(t *testing.T, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(kek0(t), args...)
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

func compareCmd(t *testing.T, args ...string) {
	t.Helper()
	want, wcode := stage0(t, args...)
	got, gcode := stage1(t, args...)
	if got != want {
		t.Errorf("stage1 %v differs from stage0:\n%s", args, firstDiff(want, got))
	}
	if gcode != wcode {
		t.Errorf("stage1 %v: exit code %d, stage0 %d", args, gcode, wcode)
	}
}

func agentPaths(t *testing.T) []string {
	paths := corpus(t)
	for _, pat := range []string{"../../examples/*/*.kek"} {
		m, _ := filepath.Glob(pat)
		paths = append(paths, m...)
	}
	sort.Strings(paths)
	return append(paths, "../../compiler")
}

func forEachPath(t *testing.T, f func(t *testing.T, path string)) {
	for _, path := range agentPaths(t) {
		name, _ := filepath.Rel("../..", path)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f(t, path)
		})
	}
}

func TestCaps(t *testing.T) {
	forEachPath(t, func(t *testing.T, path string) { compareCmd(t, "caps", path) })
}

func TestCapsJSON(t *testing.T) {
	forEachPath(t, func(t *testing.T, path string) { compareCmd(t, "caps", "-json", path) })
}

func TestCheckJSON(t *testing.T) {
	forEachPath(t, func(t *testing.T, path string) { compareCmd(t, "check", "-json", path) })
}

func TestIRJSON(t *testing.T) {
	forEachPath(t, func(t *testing.T, path string) { compareCmd(t, "ir", "-json", path) })
}

var searchQueries = []string{
	"String -> Option<Int>", "Int", "&Log, String -> ()", "a -> Option<a>", "_", "Vec<String>",
	"(Int, Int) -> Int", "() -> Response", "String, Int -> String", "&Db -> Result<_, TxError>", "Fs",
	"Int -> String -> String", "Result<a, b>", "Option<_> -> Bool", "Tx, String -> Result<Option<String>, TxError>",
}

func TestSearch(t *testing.T) {
	forEachPath(t, func(t *testing.T, path string) {
		for _, q := range searchQueries {
			compareCmd(t, "search", "-json", "-limit", "0", q, path)
		}
		compareCmd(t, "search", "String -> _", path)
	})
	t.Run("builtins", func(t *testing.T) {
		for _, q := range searchQueries {
			compareCmd(t, "search", q)
		}
	})
	t.Run("errors", func(t *testing.T) {
		for _, q := range []string{"->", "Int ->", "(Int", "Option<Int", "fn", "&Int", `"x"`, "#", "Int, String", "$", "", "é"} {
			compareCmd(t, "search", q)
		}
		compareCmd(t, "search", "-limit", "x", "Int")
		compareCmd(t, "search", "-h")
		compareCmd(t, "caps", "-x", "f.kek")
		compareCmd(t, "check", "-json=x", "f.kek")
	})
}

// TestBuildGlue compares the files written by `build` for Workers programs
// (worker.js, wrangler.toml for each target, and an existing
// wrangler.toml left alone). The launcher adds kekkai_runtime.js.
func TestBuildGlue(t *testing.T) {
	m, _ := filepath.Glob("../../examples/*/*.kek")
	m = append(m, "../../testdata/e2e/bank.kek", "../../testdata/run/strings.kek")
	for _, path := range m {
		for _, target := range []string{"d1", "do", "bogus"} {
			for _, pre := range []bool{false, true} {
				name, _ := filepath.Rel("../..", path)
				t.Run(name+"/"+target, func(t *testing.T) {
					t.Parallel()
					d0, d1 := t.TempDir(), t.TempDir()
					if pre {
						for _, d := range []string{d0, d1} {
							if err := os.WriteFile(filepath.Join(d, "wrangler.toml"), []byte("# mine\n"), 0o644); err != nil {
								t.Fatal(err)
							}
						}
					}
					_, c0 := stage0(t, "build", "-o", d0, "-target", target, path)
					out, c1 := stage1(t, "build", path, d1, "-target", target)
					if (c0 == 0) != (c1 == 0) {
						t.Fatalf("exit code %d, stage0 %d: %s", c1, c0, out)
					}
					os.Remove(filepath.Join(d0, "kekkai_runtime.js"))
					f0, f1 := readDir(t, d0), readDir(t, d1)
					if len(f0) != len(f1) {
						t.Errorf("files %v, stage0 %v", keys(f1), keys(f0))
					}
					for n, data := range f0 {
						if f1[n] != data {
							t.Errorf("%s differs from stage0:\n%s", n, firstDiff(data, f1[n]))
						}
					}
				})
			}
		}
	}
}

func readDir(t *testing.T, dir string) map[string]string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = string(b)
	}
	return out
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
