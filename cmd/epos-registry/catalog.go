package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/gaarutyunov/epos/internal/catalog"
	"github.com/gaarutyunov/epos/internal/registry"
)

// statsTimeout bounds one statistics query.
//
// The catalog shares a process with the relay, so a query that hangs holds a
// goroutine in the process answering /v2/. Bounding it is what keeps a slow
// store from becoming a registry problem; the TTL cache above it is what keeps
// a burst of page loads from becoming a burst of queries.
const statsTimeout = 5 * time.Second

// bindCatalogFlags registers the catalog settings.
//
// Shared between the root command and `catalog export` rather than promoted to
// PersistentFlags: an export subcommand that opens no port should not advertise
// --addr, and the root's flags are local precisely so it doesn't.
//
// Exactly one dot, then kebab-case — matching metrics.version-attribute, and
// not a style preference. loadConfig's env TransformFunc lowercases, maps `__`
// to `.`, applies a hardcoded per-prefix replace and then turns every remaining
// `_` into `-`. A three-level key like catalog.stats.source would need the
// transform generalised, and generalising it would break
// metrics.version-attribute, whose env form is
// EPOS_REGISTRY_METRICS_VERSION_ATTRIBUTE.
//
// catalog.stats-dsn has no flag at all and is deliberately absent from this
// list. It is a working credential for a queryable database, and a server runs
// for days with its arguments readable by every process on the host. It is
// reachable from EPOS_REGISTRY_CATALOG_STATS_DSN and from nowhere else, which
// is why the `catalog_` line in that TransformFunc is load-bearing rather than
// tidy: without it the variable resolves to a flat key matching nothing, and
// the failure looks exactly like "the store is not configured".
//
// serving distinguishes the root command, which opens a listener and needs the
// switch and the freshness bound, from `catalog export`, which does neither.
// The base path is spelled differently on each and that is deliberate: on the
// server it is one catalog setting among several, so it is `--catalog.base-path`
// like its siblings; on export the whole command is the catalog, so it is plain
// `--base-path` beside `--out`.
func bindCatalogFlags(flags *pflag.FlagSet, serving bool) {
	if serving {
		// `catalog.enabled`, not a bare `catalog`, and this is a correction to
		// the design's key table rather than a preference. koanf holds a dotted
		// key as a tree: a scalar at `catalog` and a map at `catalog.base-path`
		// cannot both exist, and whichever loads last silently wins. Measured,
		// not reasoned about — with `catalog` set, koanf's Keys() returns
		// [catalog upstream] and every catalog.* key resolves to nothing, which
		// is exactly the silent-configuration failure the DSN key's own note
		// warns about one paragraph down.
		flags.Bool("catalog.enabled", false,
			"serve the read-only catalog on this listener, under --catalog.base-path")
		flags.Duration("catalog.stats-ttl", 5*time.Second,
			"how long a statistics read is reused; 0 queries on every request")
		flags.String("catalog.base-path", "/",
			"prefix every catalog URL with this path")
	} else {
		flags.String("base-path", "/",
			"prefix every catalog URL with this path")
	}
	flags.String("catalog.namespace", "",
		"enumerate this namespace through GET /v2/_catalog (empty means the whole registry)")
	flags.String("catalog.refs", "",
		"a file of <repository>:<tag> references to show instead of enumerating")
	flags.String("catalog.stats-source", catalog.SourceNone,
		"where pull counts come from: none or file")
	flags.String("catalog.stats-file", "",
		"a JSON counts document, for --catalog.stats-source file")
}

// catalogConfig is the resolved catalog configuration.
type catalogConfig struct {
	enabled       bool
	basePath      string
	namespace     string
	namespaceMode bool
	refsFile      string
	statsSource   string
	statsFile     string
	statsTTL      time.Duration
}

// index reads the catalog's skills, and never fails the caller.
//
// A failed or partial index must not stop the registry: the listener comes up
// first, /v2/ serves immediately, and the catalog answers a page saying it
// could not be built. Enabling the catalog cannot reduce the registry's
// availability.
func (c catalogConfig) index(ctx context.Context, client registry.Client, host string) (catalog.Catalog, error) {
	opts := catalog.IndexOptions{
		Host:          host,
		Namespace:     c.namespace,
		NamespaceMode: c.namespaceMode,
	}
	if c.refsFile != "" {
		refs, err := catalog.ReadRefsFile(c.refsFile)
		if err != nil {
			return catalog.Catalog{}, err
		}
		opts.Refs = refs
	}
	// Checked before any network request: an operator who configured neither
	// mode, or both, is told so by a process that has not yet opened a
	// connection.
	if err := opts.Validate(); err != nil {
		return catalog.Catalog{}, err
	}
	return catalog.BuildIndex(ctx, client, opts), nil
}

// stats builds the statistics source, scoped to the index.
//
// It does not take the DSN. config.statsDSN is resolved — that is what the
// `catalog_` line in loadConfig's TransformFunc exists for, and a test asserts
// the environment variable reaches the key — but no source in this build reads
// one, so it is never passed anywhere, never logged and never put in an error.
func (c catalogConfig) stats(repos []string) (catalog.Stats, error) {
	source, err := catalog.StatsFor(c.statsSource, c.statsFile, repos)
	if err != nil {
		return nil, err
	}
	return catalog.WithCache(source, c.statsTTL, statsTimeout), nil
}

// newCatalogHandler builds the catalog and wraps the relay with it.
func newCatalogHandler(ctx context.Context, cfg config, client registry.Client,
	relay http.Handler) (http.Handler, error) {
	if err := catalog.CheckBasePath(cfg.catalog.basePath); err != nil {
		return nil, err
	}

	index, err := cfg.catalog.index(ctx, client, cfg.registryHost)
	if err != nil {
		return nil, err
	}
	renderer, err := catalog.NewRenderer(index, cfg.catalog.basePath)
	if err != nil {
		return nil, err
	}
	stats, err := cfg.catalog.stats(index.Repositories())
	if err != nil {
		return nil, err
	}
	server, err := catalog.NewServer(renderer, client, stats)
	if err != nil {
		return nil, err
	}
	return server.Handler(relay), nil
}

// newCatalogExportCommand is the binary's first subcommand.
//
// It is on epos-registry, not on epos: the exported site is the same renderer
// over the same model, and putting export on the CLI would link internal/catalog
// — the templates, the stylesheet and the vendored ui-kit bundle — back into the
// binary a user installs, undoing the whole decision for the sake of where a
// subcommand is spelled.
func newCatalogExportCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "catalog",
		Short: "Work with the catalog without serving it",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}

	export := &cobra.Command{
		Use:   "export",
		Short: "Render the catalog to a directory as static HTML",
		Long: "export drives the same renderer the served catalog uses, without opening a\n" +
			"listener, and writes the whole site to --out. Counts are read once and baked\n" +
			"into the pages with their capture time: on a static host a pull does not move\n" +
			"a number until the next export, which is a property rather than a surprise.\n\n" +
			"An export prunes pages it did not write, so --out must be empty or a previous\n" +
			"export's directory. It is never a directory holding the export's own inputs.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}

	flags := export.Flags()
	flags.String("upstream", "", "upstream registry base URL (required)")
	flags.String("out", "", "directory to write the site into (required)")
	bindCatalogFlags(flags, false)

	export.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg, err := loadConfig(flags)
		if err != nil {
			return err
		}
		out := cfg.catalogOut
		if out == "" {
			return fmt.Errorf("an output directory is required: pass --out")
		}
		// An export prunes what it did not write, so pointed at the directory
		// holding its own reference list or counts file it deletes them.
		if err := catalog.GuardInputs(out, cfg.catalog.refsFile, cfg.catalog.statsFile); err != nil {
			return err
		}

		client, err := newRegistryClient(cfg)
		if err != nil {
			return err
		}
		index, err := cfg.catalog.index(cmd.Context(), client, cfg.registryHost)
		if err != nil {
			return err
		}
		renderer, err := catalog.NewRenderer(index, cfg.catalog.basePath)
		if err != nil {
			return err
		}
		stats, err := cfg.catalog.stats(index.Repositories())
		if err != nil {
			return err
		}
		if err := catalog.Export(cmd.Context(), renderer, client, stats, out); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "wrote %d pages for %d skills to %s\n",
			len(index.Routes()), len(index.Skills), out)
		return nil
	}

	cmd.AddCommand(export)
	return cmd
}
