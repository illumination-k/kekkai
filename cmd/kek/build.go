package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/illumination-k/kekkai/internal/driver"
	"github.com/illumination-k/kekkai/internal/glue"
)

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	outDir := fs.String("o", "out", "output directory")
	target := fs.String("target", "d1", "storage backend bound in the generated wrangler.toml: d1 or do (Durable Objects)")
	path, err := oneFile(fs, args)
	if err != nil {
		return err
	}
	if _, err := load(path); err != nil { // diagnostics with file names
		return err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	a, err := driver.Compile(string(src))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}
	files := map[string][]byte{
		"module.wasm":       a.Wasm,
		"kekkai_meta.js":    []byte(a.MetaJS),
		"kekkai_runtime.js": []byte(a.Runtime),
	}
	if a.WorkerJS != "" {
		files["worker.js"] = []byte(a.WorkerJS)
		// Do not clobber a user-edited wrangler.toml.
		wt := filepath.Join(*outDir, "wrangler.toml")
		if _, err := os.Stat(wt); os.IsNotExist(err) {
			name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			toml, err := glue.WranglerTomlFor(name, a.IR.HandlerParams, glue.Target(*target))
			if err != nil {
				return err
			}
			files["wrangler.toml"] = []byte(toml)
		}
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(*outDir, name), data, 0o644); err != nil {
			return err
		}
	}
	fmt.Printf("%s -> %s (%d bytes of wasm)\n", path, *outDir, len(a.Wasm))
	return nil
}
