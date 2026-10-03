// Package selfhost checks the self-hosted (stage1) compiler in compiler/
// against the Go implementation (stage0).
package selfhost

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/illumination-k/kekkai/internal/difftest"
	"github.com/illumination-k/kekkai/internal/driver"
	"github.com/illumination-k/kekkai/internal/glue"
	"github.com/illumination-k/kekkai/internal/syntax"
)

var nrandom = flag.Int("selfhost.random", 20, "number of random difftest programs checked by TestBackend")

var (
	stage1Once sync.Once
	stage1Dir  string
	stage1Err  error
)

// buildStage1 compiles compiler/ with stage0 once and lays it out for run.mjs.
func buildStage1(t *testing.T) string {
	stage1Once.Do(func() {
		srcs, err := driver.ReadSources("../../compiler")
		if err != nil {
			stage1Err = err
			return
		}
		a, err := driver.CompileFiles(srcs)
		if err != nil {
			stage1Err = fmt.Errorf("stage1 does not compile: %v", err)
			return
		}
		dir, err := os.MkdirTemp("", "kek-stage1-")
		if err != nil {
			stage1Err = err
			return
		}
		for n, data := range map[string]string{
			"module.wasm": string(a.Wasm), "kekkai_meta.js": a.MetaJS,
			"kekkai_runtime.js": a.Runtime, "run.mjs": glue.RunMJS,
		} {
			if err := os.WriteFile(filepath.Join(dir, n), []byte(data), 0o644); err != nil {
				stage1Err = err
				return
			}
		}
		stage1Dir = dir
	})
	if stage1Err != nil {
		t.Fatal(stage1Err)
	}
	return stage1Dir
}

type program struct {
	name string
	srcs []syntax.Source
}

func corpus(t *testing.T) []program {
	var progs []program
	files, _ := filepath.Glob("../../testdata/*/*.kek")
	for _, f := range files {
		srcs, err := driver.ReadSources(f)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel("../../testdata", f)
		progs = append(progs, program{name: strings.TrimSuffix(rel, ".kek"), srcs: srcs})
	}
	srcs, err := driver.ReadSources("../../compiler")
	if err != nil {
		t.Fatal(err)
	}
	progs = append(progs, program{name: "compiler", srcs: srcs})
	for s := 1; s <= *nrandom; s++ {
		p := difftest.New(int64(s)).Generate()
		progs = append(progs, program{name: fmt.Sprintf("random/%d", s), srcs: []syntax.Source{{Text: p.Source}}})
	}
	return progs
}

// metaJSON extracts the JSON object embedded by glue.MetaJS.
func metaJSON(metaJS string) string {
	const prefix = "export default "
	i := strings.Index(metaJS, prefix)
	return strings.TrimSuffix(metaJS[i+len(prefix):], ";\n")
}

// TestBackend compiles every program of the corpus to IR with stage0, runs
// stage1 `ir2wasm` on the IR JSON and requires the module (and metadata)
// to be byte-identical to stage0's output.
func TestBackend(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	wasmTools, _ := exec.LookPath("wasm-tools")
	stage1 := buildStage1(t)
	for _, p := range corpus(t) {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()
			a, err := driver.CompileFiles(p.srcs)
			if err != nil {
				t.Skipf("does not compile with stage0: %v", firstLine(err.Error()))
			}
			irJSON, err := json.MarshalIndent(a.IR, "", "  ") // as `kek ir -json`
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			irPath := filepath.Join(dir, "ir.json")
			wasmPath := filepath.Join(dir, "out.wasm")
			metaPath := filepath.Join(dir, "meta.json")
			if err := os.WriteFile(irPath, irJSON, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(node, filepath.Join(stage1, "run.mjs"), stage1, "ir2wasm", irPath, wasmPath, metaPath)
			var out bytes.Buffer
			cmd.Stdout, cmd.Stderr = &out, &out
			start := time.Now()
			if err := cmd.Run(); err != nil {
				t.Fatalf("stage1 ir2wasm: %v\n%s", err, out.String())
			}
			t.Logf("stage1 ir2wasm: %v (IR JSON %d bytes, wasm %d bytes)", time.Since(start).Round(time.Millisecond), len(irJSON), len(a.Wasm))
			got, err := os.ReadFile(wasmPath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, a.Wasm) {
				t.Errorf("wasm differs from stage0 (%d vs %d bytes, first difference at offset %d)",
					len(got), len(a.Wasm), firstDiff(got, a.Wasm))
			}
			meta, err := os.ReadFile(metaPath)
			if err != nil {
				t.Fatal(err)
			}
			if want := metaJSON(a.MetaJS); string(meta) != want {
				t.Errorf("meta differs from stage0:\n%s\nwant:\n%s", meta, want)
			}
			if wasmTools != "" {
				if out, err := exec.Command(wasmTools, "validate", "--features", "all", wasmPath).CombinedOutput(); err != nil {
					t.Errorf("wasm-tools validate: %v\n%s", err, out)
				}
			}
		})
	}
}

func firstDiff(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
