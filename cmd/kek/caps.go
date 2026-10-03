package main

import (
	"flag"
	"fmt"
	"sort"
	"strings"
)

// runCaps prints, for every function, the capabilities it receives. Since
// capabilities are second-class and there is no ambient authority, this
// set is exactly the effects the function (and everything it calls) can
// perform. Functions with no capabilities are pure.
func runCaps(args []string) error {
	path, err := oneFile(flag.NewFlagSet("caps", flag.ExitOnError), args)
	if err != nil {
		return err
	}
	info, err := load(path)
	if err != nil {
		return err
	}
	for _, fn := range info.FuncList {
		var caps, unused []string
		for _, c := range fn.Caps() {
			caps = append(caps, fmt.Sprintf("%s: %s", c.Name, c.Type))
			if c.Uses == 0 {
				unused = append(unused, c.Name)
			}
		}
		tag := ""
		if fn.Handler {
			tag = " #[handler]"
		}
		if len(caps) == 0 {
			fmt.Printf("%s%s: pure\n", fn.Name, tag)
			continue
		}
		var ops []string
		for op := range fn.Effects {
			ops = append(ops, op)
		}
		sort.Strings(ops)
		fmt.Printf("%s%s: %s\n", fn.Name, tag, strings.Join(caps, ", "))
		if len(ops) > 0 {
			fmt.Printf("    direct effects: %s\n", strings.Join(ops, ", "))
		}
		if len(unused) > 0 {
			fmt.Printf("    warning: granted but unused: %s\n", strings.Join(unused, ", "))
		}
	}
	return nil
}
