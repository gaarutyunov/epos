// Command epos-registry fronts an upstream OCI registry.
//
// It serves the read surface of SPEC.md 4.1, relays blobs with the 4.2 transfer
// posture, sets Epos-Version on every response (4.3), and counts downloads
// (5.1). The write path (4.5) arrives in a later milestone.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/gaarutyunov/epos/internal/metrics"
	"github.com/gaarutyunov/epos/internal/registry"
	"github.com/gaarutyunov/epos/internal/upstream"
)

// Version is the semver reported in the Epos-Version header (SPEC.md 4.3).
// Overridden at release time via -ldflags.
var Version = "0.0.0-dev"

// envPrefix namespaces the environment variables that configure the registry:
// EPOS_REGISTRY_UPSTREAM sets `upstream`, EPOS_REGISTRY_METRICS_EXPORTER sets
// `metrics.exporter`, and so on.
const envPrefix = "EPOS_REGISTRY_"

// shutdownGrace bounds both the request drain and the final metric flush.
const shutdownGrace = 5 * time.Second

func main() {
	if err := newRootCommand().Execute(); err != nil {
		// cobra has already printed the error.
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "epos-registry",
		Short:   "Front an upstream OCI registry with the Epos read path",
		Version: Version,
		Long: "epos-registry speaks the OCI Distribution API and nothing else, so any\n" +
			"OCI client works against it unchanged. It relays to a configured upstream,\n" +
			"holds no state, passes blob redirects through, and counts content downloads.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
	}

	flags := cmd.Flags()
	flags.String("addr", ":8080", "address to listen on")
	flags.String("upstream", "", "upstream registry base URL (required)")
	flags.String("metrics.exporter", metrics.ExporterStdout, "metrics exporter: stdout or none")
	flags.Duration("metrics.interval", 0,
		"how often the metrics exporter emits (0 uses the SDK default)")
	flags.String("traces.exporter", metrics.TracesExporterNone,
		"download-span exporter: otlp or none")
	flags.String("traces.endpoint", "",
		"collector OTLP/HTTP endpoint as host:port (default: the OTLP default)")
	flags.Bool("traces.insecure", false,
		"send spans over plain HTTP, for a collector on the same host or network")
	flags.Duration("traces.timeout", 0,
		"how long one span export may take (0 uses the exporter default)")
	flags.Bool("metrics.version-attribute", false,
		"record the skill version on each download; off by default because "+
			"version-valued attributes are unbounded in cardinality")
	bindCatalogFlags(flags, true)

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg, err := loadConfig(flags)
		if err != nil {
			return err
		}
		return run(cmd.Context(), cfg)
	}

	// Adding a subcommand must not change what a bare `epos-registry` does: the
	// root keeps cobra.NoArgs and a RunE that serves.
	cmd.AddCommand(newCatalogExportCommand())

	return cmd
}

// config is the resolved runtime configuration.
type config struct {
	addr             string
	upstreamURL      string
	exporter         string
	interval         time.Duration
	versionAttribute bool

	// The download span's own switch, independent of the metrics exporter
	// above: numbers in a store without a metrics pipeline is a configuration,
	// and so is the reverse.
	tracesExporter string
	tracesEndpoint string
	tracesInsecure bool
	tracesTimeout  time.Duration

	// registryHost is the upstream's host[:port], which is what an OCI client
	// is built against; plainHTTP follows the upstream's scheme.
	registryHost string
	plainHTTP    bool

	catalog    catalogConfig
	catalogOut string
	// statsDSN is the store credential. It has no flag — a long-running
	// server's arguments are readable by every process on the host — so
	// EPOS_REGISTRY_CATALOG_STATS_DSN is its only path, and the `catalog_` line
	// in the TransformFunc below is what makes that path exist.
	statsDSN string
}

// loadConfig resolves flags and environment through koanf.
//
// Precedence is environment first, then flags, so a flag the user actually
// typed wins over the ambient environment while an untouched flag still
// contributes its default.
func loadConfig(flags *pflag.FlagSet) (config, error) {
	k := koanf.New(".")

	if err := k.Load(env.Provider(".", env.Opt{
		Prefix: envPrefix,
		TransformFunc: func(key, value string) (string, any) {
			key = strings.TrimPrefix(key, envPrefix)
			key = strings.ToLower(key)
			// EPOS_REGISTRY_METRICS__EXPORTER and
			// EPOS_REGISTRY_METRICS_EXPORTER both reach metrics.exporter.
			key = strings.ReplaceAll(key, "__", ".")
			key = strings.Replace(key, "metrics_", "metrics.", 1)
			// Beside the metrics_ line, and for the same reason. Without it
			// EPOS_REGISTRY_CATALOG_STATS_DSN lowercases to
			// catalog_stats_dsn, matches no `__`, misses the metrics_ replace
			// and ends up as the flat key catalog-stats-dsn, resolving to
			// nothing. That key is the credential and env is its only path, so
			// this fails silently and reads as "the store is not configured".
			//
			// Deliberately not generalised to "map every _ to .": that would
			// break metrics.version-attribute, whose env form is
			// EPOS_REGISTRY_METRICS_VERSION_ATTRIBUTE — a breaking change to a
			// shipped key, bought for punctuation.
			key = strings.Replace(key, "catalog_", "catalog.", 1)
			// And the same one line again for the span's keys. Each prefix
			// needs its own: the transform is a per-group replace, not a rule,
			// and a group without a line here collapses to a flat kebab key
			// that resolves to nothing.
			key = strings.Replace(key, "traces_", "traces.", 1)
			return strings.ReplaceAll(key, "_", "-"), value
		},
	}), nil); err != nil {
		return config{}, fmt.Errorf("load environment: %w", err)
	}

	if err := k.Load(posflag.ProviderWithFlag(flags, ".", k,
		func(f *pflag.Flag) (string, any) {
			// An untouched flag must not overwrite the environment; its default
			// is only a fallback for a key nothing else supplied.
			if !f.Changed && k.Exists(f.Name) {
				return "", nil
			}
			return f.Name, posflag.FlagVal(flags, f)
		}), nil); err != nil {
		return config{}, fmt.Errorf("load flags: %w", err)
	}

	cfg := config{
		addr:             k.String("addr"),
		upstreamURL:      k.String("upstream"),
		exporter:         k.String("metrics.exporter"),
		interval:         k.Duration("metrics.interval"),
		versionAttribute: k.Bool("metrics.version-attribute"),
		tracesExporter:   k.String("traces.exporter"),
		tracesEndpoint:   k.String("traces.endpoint"),
		tracesInsecure:   k.Bool("traces.insecure"),
		tracesTimeout:    k.Duration("traces.timeout"),
		catalogOut:       k.String("out"),
		statsDSN:         k.String("catalog.stats-dsn"),
		catalog: catalogConfig{
			enabled: k.Bool("catalog.enabled"),
			// One of the two exists in any invocation: the root registers
			// catalog.base-path and export registers base-path, and neither
			// registers the other's.
			basePath:    firstNonEmpty(k.String("catalog.base-path"), k.String("base-path")),
			namespace:   k.String("catalog.namespace"),
			refsFile:    k.String("catalog.refs"),
			statsSource: k.String("catalog.stats-source"),
			statsFile:   k.String("catalog.stats-file"),
			statsTTL:    k.Duration("catalog.stats-ttl"),
		},
	}
	if cfg.upstreamURL == "" {
		return config{}, errors.New("an upstream registry is required: pass --upstream or set " +
			envPrefix + "UPSTREAM")
	}

	// Which enumeration mode was chosen has to be readable from what the
	// operator actually supplied, and an empty namespace is legal — a registry
	// holding nothing but skills needs no filter. So the flag being *set* is the
	// signal, not the flag being non-empty; the environment's own key counts
	// too, since a deployment configures through it.
	cfg.catalog.namespaceMode = flags.Changed("catalog.namespace") ||
		k.Exists("catalog.namespace") && k.String("catalog.namespace") != ""

	host, plainHTTP, err := upstreamHost(cfg.upstreamURL)
	if err != nil {
		return config{}, err
	}
	cfg.registryHost, cfg.plainHTTP = host, plainHTTP

	return cfg, nil
}

// firstNonEmpty returns the first value that was actually supplied.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// upstreamHost reads the registry host an OCI client is built against out of
// the upstream URL the relay already takes.
//
// There is no separate registry setting for the catalog, and that is the point:
// the catalog shows the skills of the registry the process fronts, which is the
// only thing a registry's own UI should show. Adding a setting would make the
// registry's UI able to show somebody else's registry.
func upstreamHost(raw string) (host string, plainHTTP bool, err error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", false, fmt.Errorf("upstream %q is not a URL: write it as "+
			"http://host:port or https://host", raw)
	}
	return parsed.Host, parsed.Scheme == "http", nil
}

// newRegistryClient builds the catalog's OCI client for the upstream.
//
// Anonymous. The catalog reads a registry a deployment already fronts, and
// epos-registry holds no credential store of its own; a private upstream is a
// deployment concern the relay has the same way. Options is the plain struct
// internal/registry takes, with no cobra and no koanf in it.
func newRegistryClient(cfg config) (registry.Client, error) {
	return registry.NewOCIRegistry(cfg.registryHost, registry.Options{PlainHTTP: cfg.plainHTTP})
}

func run(ctx context.Context, cfg config) error {
	up, err := upstream.New(cfg.upstreamURL)
	if err != nil {
		return err
	}

	downloads, shutdownMetrics, err := metrics.New(ctx, metrics.Config{
		Exporter:         cfg.exporter,
		Interval:         cfg.interval,
		VersionAttribute: cfg.versionAttribute,
		TracesExporter:   cfg.tracesExporter,
		TracesEndpoint:   cfg.tracesEndpoint,
		TracesInsecure:   cfg.tracesInsecure,
		TracesTimeout:    cfg.tracesTimeout,
	})
	if err != nil {
		return err
	}

	handler := newHandler(Version, up, downloads)
	if cfg.catalog.enabled {
		client, err := newRegistryClient(cfg)
		if err != nil {
			return err
		}
		// The index build is a startup step, and its failure is the catalog's
		// alone: the listener still comes up and /v2/ still serves. Only a
		// misconfiguration — a base path under /v2/, both enumeration modes at
		// once, an unreadable refs file — stops the process, and each of those
		// is answered before any network request.
		handler, err = newCatalogHandler(ctx, cfg, client, handler)
		if err != nil {
			return err
		}
		log.Printf("epos-registry: serving the catalog at %s", cfg.catalog.basePath)
	}

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// A signalled shutdown drains in-flight requests and then flushes the
	// counter, so the last interval's downloads are not lost on every deploy.
	// This has to be wired explicitly: ListenAndServe blocks until the server
	// stops, and a deferred flush would never run on an exit path.
	signalled, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-signalled.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(os.Stderr, "epos-registry: shutdown:", err)
		}
	}()

	log.Printf("epos-registry %s listening on %s, fronting %s", Version, cfg.addr, cfg.upstreamURL)
	serveErr := srv.ListenAndServe()

	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	if err := shutdownMetrics(flushCtx); err != nil {
		fmt.Fprintln(os.Stderr, "epos-registry: flush metrics:", err)
	}

	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}
