// Command epos-registry fronts an upstream OCI registry.
//
// It serves the read surface of SPEC.md 4.1, relays blobs with the 4.2 transfer
// posture, sets Epos-Version on every API response (4.3), and counts downloads
// (5.1). The write path (4.5) arrives in a later milestone.
//
// The listener is github.com/gaarutyunov/goga/serve, which contributes the
// bounded timeouts, the bounded drain, the OpenTelemetry wrapper applied
// exactly once, and the operational endpoints — /livez, /readyz, /healthz and
// /metrics. Those four are dispatched before the instrumented handler is
// reached and are therefore never traced, which is the property epos-registry
// adopted goga/serve for: a liveness probe every second is not a request the
// registry received. They are not part of the OCI Distribution API and do not
// carry Epos-Version; --ops-addr moves them to a listener of their own.
//
// The routing is unchanged by the adoption. goga/serve's port is a plain
// net/http.Handler, so newHandler is handed over as it stands.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gaarutyunov/goga/serve"
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

// signalPrefixes are the configuration groups whose single-underscore
// environment spelling reaches a dotted key: EPOS_REGISTRY_METRICS_EXPORTER and
// EPOS_REGISTRY_METRICS__EXPORTER both mean metrics.exporter.
var signalPrefixes = []string{"metrics_", "traces_", "logs_"}

// shutdownGrace bounds the request drain and the final telemetry flush.
const shutdownGrace = 5 * time.Second

// readHeaderTimeout bounds how long a connection may take to send its request
// headers. An unbounded one is the cheapest denial of service there is: one
// idle connection holds a goroutine for the life of the process.
const readHeaderTimeout = 10 * time.Second

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
	// The operational endpoints share --addr by default, which is what a
	// single-port deployment wants. They move to their own listener when this
	// is set, for a deployment whose registry port is public and whose probes
	// and metrics must not be (SPEC.md 4.1).
	flags.String("ops-addr", "",
		"address for /livez, /readyz, /healthz and /metrics "+
			"(empty serves them on --addr)")
	flags.String("upstream", "", "upstream registry base URL (required)")
	flags.String("metrics.exporter", exporterStdout, "metrics exporter: stdout, otlp or none")
	flags.Duration("metrics.interval", 0,
		"how often the metrics exporter emits (0 uses the SDK default)")
	flags.Bool("metrics.version-attribute", false,
		"record the skill version on each download; off by default because "+
			"version-valued attributes are unbounded in cardinality")
	// Traces and logs are configurable because they exist: goga/telemetry
	// installs all three signals or none, so the choice is which exporter each
	// one gets, not whether the provider is there. Traces default to none —
	// there is no collector to push to until one is deployed — and logs to
	// stderr, which is where this command's operator output has always gone.
	flags.String("traces.exporter", exporterNone, "trace exporter: otlp, stdout or none")
	flags.String("logs.exporter", exporterStderr, "log exporter: stderr, stdout, otlp or none")

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
	opsAddr          string
	upstreamURL      string
	exporter         string
	interval         time.Duration
	versionAttribute bool
	tracesExporter   string
	logsExporter     string
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
			for _, prefix := range signalPrefixes {
				if after, ok := strings.CutPrefix(key, prefix); ok {
					key = strings.TrimSuffix(prefix, "_") + "." + after
					break
				}
			}
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
		opsAddr:          k.String("ops-addr"),
		upstreamURL:      k.String("upstream"),
		exporter:         k.String("metrics.exporter"),
		interval:         k.Duration("metrics.interval"),
		versionAttribute: k.Bool("metrics.version-attribute"),
		tracesExporter:   k.String("traces.exporter"),
		logsExporter:     k.String("logs.exporter"),
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

	// Telemetry first, and the flush deferred immediately: everything after
	// this line records into providers that are installed globally, and the
	// last interval's counts are lost if the flush is skipped on any exit path.
	tel, flushTelemetry, err := setupTelemetry(ctx, cfg)
	if err != nil {
		return err
	}
	defer flushTelemetry()

	// The meter is taken from the returned provider rather than from
	// tel.Meter so that epos.downloads keeps epos's own instrumentation scope
	// (SPEC.md 5.3 names the instrument; the scope is how an operator tells
	// whose instrument it is). Reaching a provider without going through
	// package-level state is exactly what goga returns *Telemetry for.
	downloads, err := metrics.New(
		tel.MeterProvider.Meter(metrics.ScopeName),
		metrics.Config{VersionAttribute: cfg.versionAttribute},
	)
	if err != nil {
		return err
	}

	srv, err := serve.New(ctx, newHandler(Version, up, downloads),
		serverOptions(cfg, up.Ping)...)
	if err != nil {
		return err
	}

	// The signal handling stays here. goga/serve installs none of its own by
	// design — one process gets one handler, and it belongs to the composition
	// root, which is this function until goga/cli lands (goga's
	// docs/CONVENTIONS.md 1.4). Run returns when the context is cancelled,
	// having drained within shutdownGrace, and the deferred flush then runs on
	// the way out of run. That is the whole of what the old ListenAndServe
	// plus a shutdown goroutine did.
	signalled, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	slog.InfoContext(ctx, "epos-registry listening",
		"version", Version, "addr", cfg.addr, "upstream", cfg.upstreamURL)
	return srv.Run(signalled)
}

// serverOptions is the operational configuration epos-registry ships.
//
// It is a function rather than an inline literal so that the tests assert
// against the options the command actually runs with: a drain or a timeout
// asserted against a configuration nobody deploys proves nothing.
//
// ready is registered as a readiness check rather than a health check because
// an unreachable upstream is not a broken process. epos-registry relays and
// holds no state (SPEC.md 4.4), so an instance whose upstream is down should
// leave the load balancer's rotation and stay running — which is exactly what
// /readyz means and /livez does not.
func serverOptions(cfg config, ready func(ctx context.Context) error) []serve.Option {
	opts := []serve.Option{
		serve.WithAddr(cfg.addr),
		serve.WithReadHeaderTimeout(readHeaderTimeout),
		serve.WithShutdownGrace(shutdownGrace),
	}
	if ready != nil {
		opts = append(opts, serve.WithReadinessCheck("upstream", ready))
	}
	if cfg.opsAddr != "" {
		opts = append(opts, serve.WithOpsAddr(cfg.opsAddr))
	}
	return opts
}
