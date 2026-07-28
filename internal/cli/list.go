package cli

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

func newListCommand() *cobra.Command {
	var flags discoveryFlags
	var versions bool

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the skills a registry holds",
		Long: "list reads the registry's catalog and keeps the repositories under the\n" +
			"configured namespace. It stops there unless --versions is given: with it,\n" +
			"each repository is asked for its versions and each version's manifest for\n" +
			"the skill name and description.\n\n" +
			"Discovery needs GET /v2/_catalog. Registries that do not implement it\n" +
			"cannot be enumerated at all; direct references still work.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := flags.open()
			if err != nil {
				return err
			}
			return runList(cmd.Context(), cmd.OutOrStdout(), client,
				flags.registry, flags.namespace, versions)
		},
	}
	flags.bind(cmd)
	cmd.Flags().BoolVar(&versions, "versions", false,
		"also resolve each repository's versions, names and descriptions")
	return cmd
}

func runList(ctx context.Context, out io.Writer, client registryClient,
	registry, namespace string, versions bool) error {
	listing, err := discover(ctx, client, namespace, versions)
	if err != nil {
		if errors.Is(err, errNoCatalog) {
			return catalogUnavailable(registry)
		}
		return err
	}

	printSkills(out, listing)
	return nil
}
