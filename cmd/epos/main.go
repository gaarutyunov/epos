// Command epos is the Epos CLI.
//
// The command tree itself lives in internal/cli, which this package only runs:
// the documentation generator has to import it, and a main package cannot be
// imported (SPEC.md 14.1).
package main

import (
	"os"

	"github.com/gaarutyunov/epos/internal/cli"
)

// Version is overridden at release time via -ldflags.
var Version = "0.0.0-dev"

func main() {
	if err := cli.NewRootCommand(Version).Execute(); err != nil {
		// cobra has already printed the error.
		os.Exit(1)
	}
}
