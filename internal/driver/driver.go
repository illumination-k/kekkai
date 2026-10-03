// Package driver runs the whole compilation pipeline.
package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/illumination-k/kekkai/internal/glue"
	"github.com/illumination-k/kekkai/internal/ir"
	"github.com/illumination-k/kekkai/internal/syntax"
	"github.com/illumination-k/kekkai/internal/types"
	"github.com/illumination-k/kekkai/internal/wasm"
)

// Artifacts are the files of a compiled program.
type Artifacts struct {
	Info    *types.Info
	IR      *ir.Program
	Wasm    []byte
	MetaJS  string
	Runtime string
	// WorkerJS is empty when the program has no #[handler].
	WorkerJS string
}

// Check parses and type-checks source text.
func Check(src string) (*types.Info, error) {
	return CheckFiles([]syntax.Source{{Text: src}})
}

// CheckFiles parses and type-checks a multi-file program.
func CheckFiles(srcs []syntax.Source) (*types.Info, error) {
	f, err := syntax.ParseFiles(srcs)
	if err != nil {
		return nil, err
	}
	return types.Check(f)
}

// ReadSources reads a .kek file, or every .kek file of a directory (one
// program per directory).
func ReadSources(path string) ([]syntax.Source, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	files := []string{path}
	if st.IsDir() {
		files, _ = filepath.Glob(filepath.Join(path, "*.kek"))
		sort.Strings(files)
		if len(files) == 0 {
			return nil, fmt.Errorf("%s: no .kek files", path)
		}
	}
	var srcs []syntax.Source
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		srcs = append(srcs, syntax.Source{Name: f, Text: string(b)})
	}
	return srcs, nil
}

// Compile compiles source text to WasmGC plus JS glue.
func Compile(src string) (*Artifacts, error) {
	return CompileFiles([]syntax.Source{{Text: src}})
}

// CompileFiles compiles a multi-file program.
func CompileFiles(srcs []syntax.Source) (*Artifacts, error) {
	info, err := CheckFiles(srcs)
	if err != nil {
		return nil, err
	}
	prog := ir.Lower(info)
	out, err := wasm.Compile(prog)
	if err != nil {
		return nil, err
	}
	a := &Artifacts{
		Info:    info,
		IR:      prog,
		Wasm:    out.Wasm,
		MetaJS:  glue.MetaJS(glue.Meta{Strings: out.Strings, HandlerParams: prog.HandlerParams}),
		Runtime: glue.Runtime,
	}
	if prog.Entry == "handler" {
		a.WorkerJS = glue.WorkerJS(prog.HandlerParams)
	}
	return a, nil
}
