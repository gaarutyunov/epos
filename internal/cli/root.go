// Package cli is the epos command tree.
//
// pack, push, pull and the store subcommands are here (A2), registry login and
// logout, build (B1), list and search (A3), install, uninstall and ls (A4), and
// generate-key-pair, sign, attest and verify (A5).
//
// push publishes straight from the local store to the upstream registry. What
// stays withdrawn is the epos-registry *write path* (SPEC.md 4.5): relaying an
// upload contradicts 4.2, and redirecting one produced a blob-upload Location
// on a different host from the one the client targeted, which oras-go refuses
// as the fix for GHSA-jxpm-75mh-9fp7. A client pointed straight at the upstream
// gets that upstream's own Location, so the CLI's push was never affected.
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
	cmd.AddCommand(newPackCommand(), newPushCommand(), newPullCommand(), newStoreCommand(),
		newRegistryCommand(), newBuildCommand(),
		newListCommand(), newSearchCommand(),
		newInstallCommand(), newUninstallCommand(), newLsCommand(),
		newGenerateKeyPairCommand(), newSignCommand(), newAttestCommand(), newVerifyCommand())
	return cmd
}
