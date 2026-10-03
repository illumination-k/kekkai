package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/illumination-k/kekkai/internal/format"
	"github.com/illumination-k/kekkai/internal/syntax"
)

// runFmt implements `kek fmt [-w] [-check] <files or dirs>`.
func runFmt(args []string) error {
	fset := flag.NewFlagSet("fmt", flag.ExitOnError)
	write := fset.Bool("w", false, "write the result to the file instead of stdout")
	check := fset.Bool("check", false, "list files whose formatting differs and exit 1 if any")
	if err := fset.Parse(args); err != nil {
		return err
	}
	if fset.NArg() == 0 {
		return fmt.Errorf("kek fmt: expected .kek files or directories")
	}
	var files []string
	for _, arg := range fset.Args() {
		st, err := os.Stat(arg)
		if err != nil {
			return err
		}
		if !st.IsDir() {
			files = append(files, arg)
			continue
		}
		err = filepath.WalkDir(arg, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && path != arg && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(path, ".kek") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	var unformatted []string
	failed := false
	for _, path := range files {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := format.Source(src)
		if err != nil {
			failed = true
			if list, ok := err.(syntax.ErrorList); ok {
				for _, e := range list {
					fmt.Fprintf(os.Stderr, "%s:%s: %s\n", path, e.Pos, e.Msg)
				}
			} else {
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			}
			continue
		}
		switch {
		case *check:
			if !bytes.Equal(src, out) {
				unformatted = append(unformatted, path)
			}
		case *write:
			if !bytes.Equal(src, out) {
				if err := os.WriteFile(path, out, 0o644); err != nil {
					return err
				}
			}
		default:
			os.Stdout.Write(out)
		}
	}
	if len(unformatted) > 0 {
		for _, f := range unformatted {
			fmt.Println(f)
		}
		return fmt.Errorf("kek fmt: %d file(s) need formatting (run `kek fmt -w`)", len(unformatted))
	}
	if failed {
		return fmt.Errorf("kek fmt: some files could not be parsed")
	}
	return nil
}
