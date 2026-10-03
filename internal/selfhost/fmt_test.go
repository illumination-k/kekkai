package selfhost_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/difftest"
	"github.com/illumination-k/kekkai/internal/format"
	"github.com/illumination-k/kekkai/internal/syntax"
)

// stage0Fmt is the output of `kek fmt <path>` (stdout and stderr) for one
// file, and whether it succeeded.
func stage0Fmt(path string) (string, bool) {
	src, err := os.ReadFile(path)
	if err != nil {
		return err.Error(), false
	}
	out, err := format.Source(src)
	if err != nil {
		var b strings.Builder
		for _, e := range err.(syntax.ErrorList) {
			fmt.Fprintf(&b, "%s:%s: %s\n", path, e.Pos, e.Msg)
		}
		b.WriteString("kek fmt: some files could not be parsed\n")
		return b.String(), false
	}
	return string(out), true
}

// TestFmt requires stage1 `fmt` to print exactly what stage0's formatter
// (internal/format) prints, over the golden inputs, the corpus (including
// malformed files) and random programs.
func TestFmt(t *testing.T) {
	files := corpus(t)
	golden, _ := filepath.Glob("../../tests/fmt/*.in.kek")
	files = append(files, golden...)
	nested, _ := filepath.Glob("../../testdata/*/*/*.kek")
	files = append(files, nested...)
	dir := t.TempDir()
	for s := 1; s <= 20; s++ {
		p := difftest.New(int64(s)).Generate()
		f := filepath.Join(dir, fmt.Sprintf("random%d.kek", s))
		if err := os.WriteFile(f, []byte(p.Source), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	sort.Strings(files)
	for _, file := range files {
		name := file
		if rel, err := filepath.Rel("../..", file); err == nil && !strings.HasPrefix(rel, "..") {
			name = rel
		} else {
			name = filepath.Base(file)
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			want, ok := stage0Fmt(file)
			got, code := stage1(t, "fmt", file)
			if got != want {
				t.Errorf("stage1 fmt output differs from stage0:\n%s", firstDiff(want, got))
			}
			if (code == 0) != ok {
				t.Errorf("exit code %d, stage0 ok=%v", code, ok)
			}
		})
	}
}

// TestFmtCheck runs `fmt -check` over directories: formatted files pass,
// and an unformatted one is listed.
func TestFmtCheck(t *testing.T) {
	if out, code := stage1(t, "fmt", "-check", "../../testdata/test"); code != 0 {
		t.Fatalf("fmt -check testdata/test: exit %d\n%s", code, out)
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	hidden := filepath.Join(dir, ".hidden")
	for _, d := range []string{sub, hidden} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ugly := "fn main()->Int{1}\n"
	for _, f := range []string{filepath.Join(sub, "a.kek"), filepath.Join(hidden, "b.kek"), filepath.Join(dir, "c.txt")} {
		if err := os.WriteFile(f, []byte(ugly), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, code := stage1(t, "fmt", "-check", dir)
	want := filepath.Join(sub, "a.kek") + "\nkek fmt: 1 file(s) need formatting (run `kek fmt -w`)\n"
	if code != 1 || out != want {
		t.Fatalf("fmt -check: exit %d\n%s\nwant:\n%s", code, out, want)
	}
	if out, code := stage1(t, "fmt", "-w", dir); code != 0 {
		t.Fatalf("fmt -w: exit %d\n%s", code, out)
	}
	got, _ := os.ReadFile(filepath.Join(sub, "a.kek"))
	if string(got) != "fn main() -> Int {\n    1\n}\n" {
		t.Errorf("fmt -w wrote %q", got)
	}
}
