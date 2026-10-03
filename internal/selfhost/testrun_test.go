package selfhost_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/illumination-k/kekkai/internal/testrun"
)

// TestTestBuild requires stage1 `test-build` (compiler/testrun.kek) to
// discover the same tests as stage0's testrun.Discover and to build a test
// module byte-identical to testrun.Compile.
func TestTestBuild(t *testing.T) {
	bank, err := os.ReadFile("../../testdata/e2e/bank.kek")
	if err != nil {
		t.Fatal(err)
	}
	withTest := t.TempDir() + "/bank_test.kek"
	if err := os.WriteFile(withTest, append(bank, []byte(`
#[test]
fn amount_parsing() -> Bool {
    match parse_amount("12") {
        Some(n) => n == 12,
        None => false,
    }
}
`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob("../../testdata/test/*.kek")
	for _, path := range append(files, withTest) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			tests, err := testrun.Discover(string(src))
			if err != nil {
				t.Fatal(err)
			}
			a, err := testrun.Compile(string(src), tests)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			if out, code := stage1(t, "test-build", path, dir); code != 0 {
				t.Fatalf("stage1 test-build: exit %d\n%s", code, out)
			}
			var got []struct {
				testrun.Test
				Pure bool `json:"pure"`
			}
			data, err := os.ReadFile(filepath.Join(dir, "tests.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tests) {
				t.Fatalf("tests.json: %d tests, want %d\n%s", len(got), len(tests), data)
			}
			for i, w := range tests {
				g := got[i]
				if g.Name != w.Name || g.Result != w.Result || g.Pure != w.Pure() || !bytes.Equal(mustJSON(g.Caps), mustJSON(w.Caps)) {
					t.Errorf("test %d: got %+v, want %+v", i, g, w)
				}
			}
			wasm, err := os.ReadFile(filepath.Join(dir, "module.wasm"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(wasm, a.Wasm) {
				t.Errorf("module.wasm differs from stage0 (%d vs %d bytes)", len(wasm), len(a.Wasm))
			}
			meta, err := os.ReadFile(filepath.Join(dir, "kekkai_meta.js"))
			if err != nil {
				t.Fatal(err)
			}
			if string(meta) != a.MetaJS {
				t.Errorf("kekkai_meta.js differs from stage0:\n%s\nwant:\n%s", meta, a.MetaJS)
			}
		})
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
