package difftest

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/illumination-k/kekkai/internal/driver"
)

var (
	nprogs = flag.Int("difftest.n", 60, "number of random programs")
	seed0  = flag.Int64("difftest.seed", 1, "first seed")
	dump   = flag.String("difftest.dump", "", "write the program for -difftest.seed to this file (TestDump)")
)

// TestDump writes the generated program for -difftest.seed to -difftest.dump.
func TestDump(t *testing.T) {
	if *dump == "" {
		t.Skip("use -difftest.dump=<file>")
	}
	if err := os.WriteFile(*dump, []byte(New(*seed0).Generate().Source), 0o644); err != nil {
		t.Fatal(err)
	}
}

type call struct {
	Fn   string `json:"fn"`
	Args []any  `json:"args"`
}

var argSets = [][]any{
	{"0", "0", false}, {"1", "-1", true}, {"7", "3", true}, {"-13", "4", false},
	{"9223372036854775807", "-1", true}, {"-9223372036854775808", "-1", false},
	{"123456789", "0", true}, {"42", "65536", false},
}

// refBinary returns the Lean reference interpreter if it has been built.
func refBinary() string {
	p, _ := filepath.Abs("../../lean/.lake/build/bin/kekkai-ref")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

// TestDifferential compiles random programs, runs them as WasmGC on Node and
// (when built) with the Lean reference interpreter, and compares results.
func TestDifferential(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	ref := refBinary()
	if ref == "" {
		t.Log("Lean reference interpreter not built (mise run lean); checking that wasm runs without traps only")
	}
	for s := *seed0; s < *seed0+int64(*nprogs); s++ {
		prog := New(s).Generate()
		a, err := driver.Compile(prog.Source)
		if err != nil {
			t.Fatalf("seed %d: generated program does not compile: %v\n%s", s, err, prog.Source)
		}
		dir := t.TempDir()
		write := func(name string, data []byte) {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("module.wasm", a.Wasm)
		write("kekkai_meta.js", []byte(a.MetaJS))
		write("kekkai_runtime.js", []byte(a.Runtime))
		write("prog.kek", []byte(prog.Source))
		irJSON, _ := json.Marshal(a.IR)
		write("ir.json", irJSON)
		var calls []call
		for _, f := range prog.Funcs {
			for _, as := range argSets {
				calls = append(calls, call{Fn: f, Args: as})
			}
		}
		cj, _ := json.Marshal(calls)
		write("calls.json", cj)
		// generated programs terminate: a hang in wasm is a bug
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		out, err := exec.CommandContext(ctx, node, "run_pure.mjs", dir, filepath.Join(dir, "calls.json")).Output()
		cancel()
		if err != nil {
			t.Fatalf("seed %d: node: %v\n%s", s, err, out)
		}
		var wasmRes []map[string]string
		if err := json.Unmarshal(out, &wasmRes); err != nil {
			t.Fatalf("seed %d: bad node output: %s", s, out)
		}
		for i, c := range calls {
			if e, ok := wasmRes[i]["error"]; ok {
				t.Errorf("seed %d: %s%v trapped in wasm: %s\n%s", s, c.Fn, c.Args, e, prog.Source)
				continue
			}
			if ref == "" {
				continue
			}
			args := []string{filepath.Join(dir, "ir.json"), c.Fn}
			for _, a := range c.Args {
				args = append(args, fmt.Sprint(a))
			}
			rout, err := exec.Command(ref, args...).Output()
			if err != nil {
				t.Fatalf("seed %d: kekkai-ref: %v\n%s", s, err, rout)
			}
			var r struct {
				OK    json.RawMessage `json:"ok"`
				Error string          `json:"error"`
			}
			if err := json.NewDecoder(strings.NewReader(string(rout))).Decode(&r); err != nil {
				t.Fatalf("seed %d: bad kekkai-ref output %q", s, rout)
			}
			// all generated functions return Int: compare the exact JSON number
			if r.Error != "" || string(r.OK) != wasmRes[i]["ok"] {
				t.Errorf("seed %d: %s%v: wasm=%s reference=%s%s\nprogram kept at %s", s, c.Fn, c.Args, wasmRes[i]["ok"], r.OK, r.Error, dir)
			}
		}
	}
}
