package types_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/types"
)

var errorRe = regexp.MustCompile(`ERROR "([^"]*)"`)

// TestCheck runs every testdata/check/*.kek file. Lines annotated with
// `// ERROR "substring"` must produce a matching diagnostic; no other
// diagnostics are allowed.
func TestCheck(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/check/*.kek")
	if len(files) == 0 {
		t.Fatal("no test files")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			src, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			want := map[int][]string{}
			for i, line := range strings.Split(string(src), "\n") {
				for _, m := range errorRe.FindAllStringSubmatch(line, -1) {
					want[i+1] = append(want[i+1], m[1])
				}
			}
			var got syntax.ErrorList
			f, err := syntax.Parse(string(src))
			if err == nil {
				_, err = types.Check(f)
			}
			if err != nil {
				got = err.(syntax.ErrorList)
			}
			matched := map[*syntax.Error]bool{}
			for line, subs := range want {
				for _, sub := range subs {
					found := false
					for _, e := range got {
						if e.Pos.Line == line && !matched[e] && strings.Contains(e.Msg, sub) {
							matched[e] = true
							found = true
							break
						}
					}
					if !found {
						t.Errorf("%d: missing error matching %q", line, sub)
					}
				}
			}
			for _, e := range got {
				if !matched[e] {
					t.Errorf("unexpected error: %s", e)
				}
			}
		})
	}
}
