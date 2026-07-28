// Package cli is the epos command tree.
//
// pack, pull and the store subcommands are here (A2), build (B1), list and
// search (A3), install, uninstall and ls (A4), and generate-key-pair, sign,
// attest and verify (A5). push is deliberately absent — see the write-path
// note on the A2 issue.
//
// It is a library rather than the `package main` of cmd/epos so that the
// documentation generator can import it. SPEC.md 14.1 requires the CLI
// reference to be generated from the same source as the CLI's own help output,
// and a main package cannot be imported: the reference would have had to be
// scraped from the binary's output or retyped, and both drift.
package cli

import "github.com/spf13/cobra"

// NewRootCommand builds the whole command tree.
//
// version is what `epos --version` reports. It is a parameter rather than a
// variable here because the release build stamps it with
// `-X main.Version=<v>`, which can only reach the main package.
func NewRootCommand(version string) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "epos",
		Short:   "OCI-native packaging and composition for agent skills",
		Version: version,
		Long: "epos packages agent skills as OCI artifacts and composes them.\n\n" +
			"State lives under ~/.epos, or under $EPOS_HOME when that is set.",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newPackCommand(), newPullCommand(), newStoreCommand(), newBuildCommand(),
		newListCommand(), newSearchCommand(),
		newInstallCommand(), newUninstallCommand(), newLsCommand(),
		newGenerateKeyPairCommand(), newSignCommand(), newAttestCommand(), newVerifyCommand())
	return cmd
}
