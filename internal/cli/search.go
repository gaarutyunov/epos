package cli

import (
	"context"
	"errors"
	"io"

	"github.com/spf13/cobra"

	"github.com/gaarutyunov/epos/internal/registry"
)

func newSearchCommand() *cobra.Command {
	var flags discoveryFlags

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search a registry's skills by name or description",
		Long: "search enumerates the registry the way list --versions does and filters\n" +
			"the result on the client: the query is matched against the repository\n" +
			"name, the skill name and the description. The OCI Distribution API has no\n" +
			"search endpoint and epos-registry does not add one.\n\n" +
			"Discovery needs GET /v2/_catalog. Registries that do not implement it\n" +
			"cannot be searched at all; direct references still work.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := flags.open()
			if err != nil {
				return err
			}
			return runSearch(cmd.Context(), cmd.OutOrStdout(), client,
				flags.registry, flags.namespace, args[0])
		},
	}
	flags.bind(cmd)
	return cmd
}

func runSearch(ctx context.Context, out io.Writer, client registry.Client,
	host, namespace, query string) error {
	// Always the full pipeline: the skill name and description a query matches
	// against live in the manifest annotations, so there is nothing to filter
	// until steps 3 and 4 have run (SPEC.md 7.2).
	listing, err := registry.Discover(ctx, client, namespace, true)
	if err != nil {
		if errors.Is(err, registry.ErrNoCatalog) {
			return registry.CatalogUnavailable(host)
		}
		return err
	}

	matched := make([]registry.Skill, 0, len(listing))
	for _, s := range listing {
		if s.Matches(query) {
			matched = append(matched, s)
		}
	}

	printSkills(out, matched)
	return nil
}
