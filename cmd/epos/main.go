// Command epos is the Epos CLI.
//
// Minimal buildable shim (SPEC.md 13.1) — it prints usage. The commands
// themselves arrive with their milestones: pack/push/pull in A2, search/list in
// A3, install in A4, verify in A5, build in B1.
package main

import (
	"flag"
	"fmt"
	"os"
)

// Version is overridden at release time via -ldflags.
var Version = "0.0.0-dev"

func main() {
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() == 0 {
		usage()
		os.Exit(2)
	}

	fmt.Fprintf(os.Stderr, "epos: unknown command %q\n\n", flag.Arg(0))
	usage()
	os.Exit(2)
}

func usage() {
	fmt.Fprintf(os.Stderr, `epos %s — OCI-native packaging and composition for agent skills

usage: epos <command> [flags]

No commands are implemented yet; they arrive with their milestones.
`, Version)
}
