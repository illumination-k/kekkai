package main

// Editor and agent tooling: `kek lsp`, `kek search`, and the JSON outputs
// of `kek check -json` / `kek caps -json`.

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/illumination-k/kekkai/internal/lsp"
	"github.com/illumination-k/kekkai/internal/tooling"
)

// errSilent makes main exit with status 1 without printing anything more
// (the diagnostics were already written as JSON).
var errSilent = errors.New("")

func runLSP(args []string) error {
	fs := flag.NewFlagSet("lsp", flag.ExitOnError)
	fs.Bool("stdio", true, "communicate over stdin/stdout (the only transport)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return lsp.Serve(os.Stdin, os.Stdout)
}

// jsonDiag is one diagnostic in `kek check -json`.
type jsonDiag struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Col      int    `json:"col"`
	EndLine  int    `json:"end_line"`
	EndCol   int    `json:"end_col"`
	Severity string `json:"severity"`
	Phase    string `json:"phase"`
	Message  string `json:"message"`
}

type checkResult struct {
	File        string     `json:"file"`
	OK          bool       `json:"ok"`
	Diagnostics []jsonDiag `json:"diagnostics"`
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// checkJSON writes the diagnostics of path as JSON and reports whether
// the file is free of errors (warnings do not fail the check).
func checkJSON(w io.Writer, path string) (bool, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	a := tooling.Analyze(string(src))
	res := checkResult{File: path, OK: len(a.Errors()) == 0, Diagnostics: []jsonDiag{}}
	for _, d := range a.Diags {
		res.Diagnostics = append(res.Diagnostics, jsonDiag{File: path, Line: d.Pos.Line, Col: d.Pos.Col,
			EndLine: d.End.Line, EndCol: d.End.Col, Severity: d.Severity, Phase: d.Phase, Message: d.Message})
	}
	return res.OK, writeJSON(w, res)
}

type capsResult struct {
	File      string              `json:"file"`
	Functions []tooling.CapReport `json:"functions"`
}

func capsJSON(w io.Writer, path string) error {
	if _, err := load(path); err != nil {
		return err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return writeJSON(w, capsResult{File: path, Functions: tooling.CapReports(tooling.Analyze(string(src)))})
}

type searchResult struct {
	Query   string          `json:"query"`
	Matches []tooling.Match `json:"matches"`
}

func runSearch(args []string) error {
	return search(os.Stdout, args)
}

func search(w io.Writer, args []string) error {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	limit := fs.Int("limit", 20, "maximum number of results (0: unlimited)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("usage: kek search [-json] [-limit n] '<type signature>' [file.kek]")
	}
	q, err := tooling.ParseQuery(fs.Arg(0))
	if err != nil {
		return err
	}
	var a *tooling.Analysis
	if fs.NArg() == 2 {
		src, err := os.ReadFile(fs.Arg(1))
		if err != nil {
			return err
		}
		a = tooling.Analyze(string(src))
	}
	ms := tooling.Search(q, a)
	if *limit > 0 && len(ms) > *limit {
		ms = ms[:*limit]
	}
	if *asJSON {
		if ms == nil {
			ms = []tooling.Match{}
		}
		return writeJSON(w, searchResult{Query: q.String(), Matches: ms})
	}
	if len(ms) == 0 {
		fmt.Fprintf(w, "no functions match %s\n", q)
		return nil
	}
	for _, m := range ms {
		loc := "builtin"
		if !m.Builtin {
			loc = fmt.Sprintf("%s:%d:%d", fs.Arg(1), m.Line, m.Col)
			if m.Line == 0 {
				loc = fs.Arg(1)
			}
		}
		fmt.Fprintf(w, "%-24s %s  [%s] (%s)\n", m.Name, m.Signature, m.Match, loc)
	}
	return nil
}
