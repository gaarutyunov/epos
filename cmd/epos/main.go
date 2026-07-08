// Command epos is the Helm-familiar CLI for authoring, distributing, composing,
// installing, and serving AI-agent Skills (SPEC §4). Lifecycle verbs are
// reinterpreted for Skills: "install" materializes files (or ConfigMaps), not a
// cluster workload.
package main

import (
	"fmt"
	"os"

	"github.com/gaarutyunov/epos/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
