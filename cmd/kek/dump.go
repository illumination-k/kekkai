package main

import (
	"fmt"
	"os"

	"github.com/illumination-k/kekkai/internal/selfhost"
)

// runDump implements `kek tokens <file>` and `kek ast <file>`: canonical
// text dumps compared against the self-hosted compiler (docs/selfhost.md).
func runDump(cmd string, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("kek %s: expected exactly one .kek file", cmd)
	}
	src, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	var out string
	var ok bool
	if cmd == "tokens" {
		out, ok = selfhost.Tokens(string(src))
	} else {
		out, ok = selfhost.AST(string(src))
	}
	fmt.Print(out)
	if !ok {
		return errSilent
	}
	return nil
}
