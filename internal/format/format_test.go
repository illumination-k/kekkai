package format_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/difftest"
	"github.com/illumination-k/kekkai/internal/format"
	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/types"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestGolden formats tests/fmt/*.in.kek and compares with *.golden.
func TestGolden(t *testing.T) {
	files, _ := filepath.Glob("../../tests/fmt/*.in.kek")
	if len(files) == 0 {
		t.Fatal("no golden inputs")
	}
	for _, in := range files {
		name := strings.TrimSuffix(filepath.Base(in), ".in.kek")
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := format.Source(src)
			if err != nil {
				t.Fatal(err)
			}
			golden := strings.TrimSuffix(in, ".in.kek") + ".golden"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}
			if string(got) != string(want) {
				t.Errorf("formatting differs from %s:\n%s", golden, got)
			}
			checkRoundTrip(t, string(src))
		})
	}
}

// TestTestdata formats every .kek file of the repository's testdata and
// checks that formatting is idempotent, preserves the AST and comments,
// and does not change the type-checking result.
func TestTestdata(t *testing.T) {
	var files []string
	filepath.WalkDir("../../testdata", func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".kek") {
			files = append(files, path)
		}
		return nil
	})
	if len(files) == 0 {
		t.Fatal("no testdata")
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			checkRoundTrip(t, string(src))
		})
	}
}

// TestGenerated runs the round-trip checks over random programs.
func TestGenerated(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		prog := difftest.New(seed).Generate()
		t.Run(fmt.Sprint(seed), func(t *testing.T) { checkRoundTrip(t, prog.Source) })
	}
}

func checkRoundTrip(t *testing.T, src string) {
	t.Helper()
	out, err := format.Source([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	again, err := format.Source(out)
	if err != nil {
		t.Fatalf("formatted output does not parse: %v\n%s", err, out)
	}
	if string(again) != string(out) {
		t.Errorf("not idempotent:\n--- first\n%s\n--- second\n%s", out, again)
	}
	f1, _ := syntax.Parse(src)
	f2, _ := syntax.Parse(string(out))
	if d1, d2 := dump(f1), dump(f2); d1 != d2 {
		t.Errorf("AST changed:\n--- before\n%s\n--- after\n%s\n--- output\n%s", d1, d2, out)
	}
	if c1, c2 := comments(src), comments(string(out)); !reflect.DeepEqual(c1, c2) {
		t.Errorf("comments changed:\n%q\n%q", c1, c2)
	}
	if e1, e2 := checkMsgs(f1), checkMsgs(f2); !reflect.DeepEqual(e1, e2) {
		t.Errorf("type-check result changed:\n%q\n%q", e1, e2)
	}
}

func comments(src string) []string {
	_, cs, _ := syntax.LexWithComments(src)
	var out []string
	for _, c := range cs {
		out = append(out, c.Text)
	}
	sort.Strings(out)
	return out
}

// checkMsgs type-checks f and returns the diagnostics without positions.
func checkMsgs(f *syntax.File) []string {
	_, err := types.Check(f)
	if err == nil {
		return nil
	}
	var out []string
	if list, ok := err.(syntax.ErrorList); ok {
		for _, e := range list {
			out = append(out, e.Msg)
		}
	} else {
		out = append(out, err.Error())
	}
	sort.Strings(out)
	return out
}

var posType = reflect.TypeOf(syntax.Pos{})

// dump renders an AST without positions.
func dump(v any) string {
	var b strings.Builder
	var walk func(v reflect.Value, depth int)
	walk = func(v reflect.Value, depth int) {
		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				b.WriteString("nil")
				return
			}
			walk(v.Elem(), depth)
		case reflect.Struct:
			b.WriteString(v.Type().Name() + "{")
			for i := 0; i < v.NumField(); i++ {
				if v.Field(i).Type() == posType {
					continue
				}
				fmt.Fprintf(&b, "%s: ", v.Type().Field(i).Name)
				walk(v.Field(i), depth+1)
				b.WriteString(", ")
			}
			b.WriteString("}")
		case reflect.Slice:
			b.WriteString("[")
			for i := 0; i < v.Len(); i++ {
				b.WriteString("\n" + strings.Repeat("  ", depth+1))
				walk(v.Index(i), depth+1)
			}
			b.WriteString("]")
		default:
			fmt.Fprintf(&b, "%#v", v.Interface())
		}
	}
	walk(reflect.ValueOf(v), 0)
	return b.String()
}
