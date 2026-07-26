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
	flags.Bool("metrics.version-attribute", false,
		"record the skill version on each download; off by default because "+
			"version-valued attributes are unbounded in cardinality")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		cfg, err := loadConfig(flags)
		if err != nil {
			return err
		}
		return run(cmd.Context(), cfg)
	}

	return cmd
}

// config is the resolved runtime configuration.
type config struct {
	addr             string
	upstreamURL      string
	exporter         string
	interval         time.Duration
	versionAttribute bool
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
	}
	if cfg.upstreamURL == "" {
		return config{}, errors.New("an upstream registry is required: pass --upstream or set " +
			envPrefix + "UPSTREAM")
	}
	return cfg, nil
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
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           newHandler(Version, up, downloads),
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
