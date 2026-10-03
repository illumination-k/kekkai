package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/illumination-k/kekkai/internal/driver"
	"github.com/illumination-k/kekkai/internal/glue"
)

// runRun compiles a #[main] program and runs it on Node:
// kek run <file.kek> [args...]
func runRun(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("kek run: expected a .kek file")
	}
	path := args[0]
	if _, err := load(path); err != nil {
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
	if a.IR.Entry != "main" {
		return fmt.Errorf("kek run: %s has no #[main] function", path)
	}
	dir, err := os.MkdirTemp("", "kek-run-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	for name, data := range map[string]string{
		"module.wasm": string(a.Wasm), "kekkai_meta.js": a.MetaJS,
		"kekkai_runtime.js": a.Runtime, "run.mjs": glue.RunMJS,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			return err
		}
	}
	cmd := exec.Command("node", append([]string{filepath.Join(dir, "run.mjs"), dir}, args[1:]...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}
