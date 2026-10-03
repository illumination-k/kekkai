// Command kek is the Kekkai toolchain: type checking, capability reports,
// and compilation to WasmGC for Cloudflare Workers.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/illumination-k/kekkai/internal/driver"
	"github.com/illumination-k/kekkai/internal/ir"
	"github.com/illumination-k/kekkai/internal/types"
)

const usage = `kek — the Kekkai toolchain

Usage:
  kek check [-json] <file.kek>    type-check (capabilities, effects, transactions)
  kek caps [-json] <file.kek>     list the capabilities (effects) of every function
  kek search [-json] [-limit n] '<signature>' [file.kek]
                                  find functions by type, e.g. 'String -> Option<Int>'
  kek lsp                         run the language server over stdio
  kek ir [-json] <file.kek>       print the intermediate representation
  kek build [-o dir] [-target d1|do] <file.kek>
                                  compile to a Cloudflare Workers module (WasmGC + JS glue)
  kek run <file.kek> [args...]    compile a #[main] program and run it on Node
  kek fmt [-w|-check] <paths>     format source files (directories are searched for *.kek)
  kek test [-run re] <file.kek>   run the #[test] functions on Node with mock capabilities
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "check":
		err = runCheck(args)
	case "caps":
		err = runCaps(args)
	case "ir":
		err = runIR(args)
	case "build":
		err = runBuild(args)
	case "run":
		err = runRun(args)
	case "search":
		err = runSearch(args)
	case "lsp":
		err = runLSP(args)
	case "fmt":
		err = runFmt(args)
	case "test":
		err = runTest(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "kek: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		if err != errSilent {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}

// load parses and type-checks a file, prefixing diagnostics with the path.
// load parses and type-checks a file or a directory of files.
// Diagnostics carry file names.
func load(path string) (*types.Info, error) {
	srcs, err := driver.ReadSources(path)
	if err != nil {
		return nil, err
	}
	return driver.CheckFiles(srcs)
}

func oneFile(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if fs.NArg() != 1 {
		return "", fmt.Errorf("kek %s: expected exactly one .kek file", fs.Name())
	}
	return fs.Arg(0), nil
}

func runCheck(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit diagnostics as JSON (exit status 1 if there are errors)")
	path, err := oneFile(fs, args)
	if err != nil {
		return err
	}
	if *asJSON {
		ok, err := checkJSON(os.Stdout, path)
		if err == nil && !ok {
			err = errSilent
		}
		return err
	}
	if _, err := load(path); err != nil {
		return err
	}
	fmt.Printf("%s: ok\n", path)
	return nil
}

func runIR(args []string) error {
	fs := flag.NewFlagSet("ir", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit JSON (input format of the Lean reference interpreter)")
	path, err := oneFile(fs, args)
	if err != nil {
		return err
	}
	info, err := load(path)
	if err != nil {
		return err
	}
	prog := ir.Lower(info)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(prog)
	}
	fmt.Print(prog)
	return nil
}
