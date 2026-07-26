// Command epos is the Epos CLI.
//
// Minimal buildable shim (SPEC.md 13.1) — it carries the root command and no
// subcommands. Those arrive with their milestones: pack/push/pull in A2,
// search/list in A3, install in A4, verify in A5, build in B1.
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
		Use:     "epos",
		Short:   "OCI-native packaging and composition for agent skills",
		Version: Version,
		Long: "epos packages agent skills as OCI artifacts and composes them.\n\n" +
			"No subcommands are implemented yet; they arrive with their milestones.",
		SilenceUsage: true,
		// With no subcommands registered, any argument is an unknown command,
		// and cobra says so rather than silently ignoring it.
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	return cmd
}
