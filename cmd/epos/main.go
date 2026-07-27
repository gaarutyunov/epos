// Command epos is the Epos CLI.
//
// pack, pull and the store subcommands are here (A2), and build (B1). push is
// deliberately absent — see the write-path note on the A2 issue. The rest
// arrive with their milestones: search/list in A3, install in A4, verify in A5.
package main

import (
	"os"

	"github.com/spf13/cobra"
)

// Version is overridden at release time via -ldflags.
var Version = "0.0.0-dev"

func main() {
	if err := newRootCommand().Execute(); err != nil {
		// cobra has already printed the error.
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "epos",
		Short:        "OCI-native packaging and composition for agent skills",
		Version:      Version,
		Long:         "epos packages agent skills as OCI artifacts and composes them.",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newPackCommand(), newPullCommand(), newStoreCommand(), newBuildCommand())
	return cmd
}
