package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"

	"github.com/illumination-k/kekkai/internal/testrun"
)

// runTest implements `kek test`: run the #[test] functions of a file on
// Node with mock capabilities.
func runTest(args []string) error {
	fs := flag.NewFlagSet("test", flag.ExitOnError)
	run := fs.String("run", "", "run only tests whose name matches this regular expression")
	seed := fs.Int64("seed", 0, "seed of the mock &Random")
	clock := fs.Int64("clock", testrun.DefaultClock, "fixed time of the mock &Clock (ms since the epoch)")
	netFile := fs.String("net", "", `JSON file of canned &Net responses: {"GET url": "body", "POST url": {"error": "msg"}}`)
	dbFile := fs.String("db", "", `JSON file with the initial contents of the mock &Db store: {"key": "value"}`)
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
	opts := testrun.Options{Seed: *seed, Clock: *clock}
	if *run != "" {
		if opts.Run, err = regexp.Compile(*run); err != nil {
			return fmt.Errorf("kek test: -run: %w", err)
		}
	}
	if err := readJSON(*netFile, &opts.Net); err != nil {
		return err
	}
	if err := readJSON(*dbFile, &opts.Db); err != nil {
		return err
	}
	failed, err := testrun.Run(path, string(src), opts, os.Stdout)
	if err != nil {
		return err
	}
	if failed {
		os.Exit(1)
	}
	return nil
}

func readJSON(path string, v any) error {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
