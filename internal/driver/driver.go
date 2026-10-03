// Package driver runs the whole compilation pipeline.
package driver

import (
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
	f, err := syntax.Parse(src)
	if err != nil {
		return nil, err
	}
	return types.Check(f)
}

// Compile compiles source text to WasmGC plus JS glue.
func Compile(src string) (*Artifacts, error) {
	info, err := Check(src)
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
	if prog.Handler != "" {
		a.WorkerJS = glue.WorkerJS(prog.HandlerParams)
	}
	return a, nil
}
