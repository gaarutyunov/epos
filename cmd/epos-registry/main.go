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
//
// Configuration is github.com/gaarutyunov/goga/config (SPEC.md 4.6). One
// config.Load call replaces the koanf pipeline that used to live here, and with
// it the paragraph explaining what order the sources were merged in: the order
// is now a property of Load. The visible cost is the environment spelling,
// which is goga's — EPOS_REGISTRY__METRICS__EXPORTER, not
// EPOS_REGISTRY_METRICS_EXPORTER — and which breaks every variable this command
// used to read.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gaarutyunov/goga/config"
	"github.com/gaarutyunov/goga/serve"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/gaarutyunov/epos/internal/metrics"
	"github.com/gaarutyunov/epos/internal/upstream"
)

// Version is the semver reported in the Epos-Version header (SPEC.md 4.3).
// Overridden at release time via -ldflags.
var Version = "0.0.0-dev"

// envPrefix namespaces the environment variables that configure the registry.
//
// goga/config owns the spelling and epos-registry does not get a say in it
// (SPEC.md 4.6): the prefix, then "__" between the segments of a key path, a
// single "_" between the words of one segment, the whole name lower-cased.
// EPOS_REGISTRY__UPSTREAM sets `upstream`, EPOS_REGISTRY__METRICS__EXPORTER
// sets `metrics.exporter`, EPOS_REGISTRY__METRICS__VERSION_ATTRIBUTE sets
// `metrics.version_attribute`.
//
// The single-underscore spellings this command accepted before the adoption —
// EPOS_REGISTRY_UPSTREAM, EPOS_REGISTRY_METRICS_EXPORTER — are gone, and so is
// the per-group exception table that made two spellings mean one key. A name
// that no longer maps to anything is ignored rather than rejected, which is
// what every environment-variable loader does with a name it does not know, so
// an operator who misses the rename sees the default and not an error. The
// required upstream is the exception: it fails the start and names the variable.
const envPrefix = "EPOS_REGISTRY"

// upstreamEnv is the one variable an error message has to name. Spelled from
// envPrefix so the message and the convention cannot drift apart.
const upstreamEnv = envPrefix + "__UPSTREAM"

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
		cfg, err := loadConfig(cmd.Context(), flags)
		if err != nil {
			return err
		}
		return run(cmd.Context(), cfg)
	}

	return cmd
}

// registryConfig is the resolved runtime configuration.
//
// The struct tags are the key paths, and there is exactly one per setting: a
// flag name with "-" rewritten to "_" (--ops-addr is ops_addr,
// --metrics.version-attribute is metrics.version_attribute), which is the same
// path the environment reaches through "__". The nesting is not decoration —
// `metrics` is a parent and never also a value, which is the one merge koanf
// cannot represent and goga/config now refuses rather than silently resolves.
//
// The tag is `koanf`, not `mapstructure`, and that is not a preference.
// goga/config decodes through koanf's UnmarshalWithConf, which sets the
// mapstructure TagName to "koanf" whenever its own Tag field is empty — which
// it always is, because goga supplies a DecoderConfig and no Tag. A field
// tagged `mapstructure:"ops_addr"` is therefore untagged as far as the decoder
// is concerned, and falls back to case-insensitive matching on the field name:
// Addr still finds addr, and OpsAddr silently does NOT find ops_addr. It
// decodes to the empty string with no error, which is exactly the failure mode
// goga/config exists to remove, arriving one layer further down.
type registryConfig struct {
	Addr     string `koanf:"addr"`
	OpsAddr  string `koanf:"ops_addr"`
	Upstream string `koanf:"upstream"`

	Metrics metricsConfig `koanf:"metrics"`
	Traces  signalConfig  `koanf:"traces"`
	Logs    signalConfig  `koanf:"logs"`
}

// metricsConfig is the `metrics` subtree: the signal epos-registry exists to
// produce, and the only one with more to configure than its exporter.
type metricsConfig struct {
	Exporter         string        `koanf:"exporter"`
	Interval         time.Duration `koanf:"interval"`
	VersionAttribute bool          `koanf:"version_attribute"`
}

// signalConfig is a telemetry signal whose only setting is its exporter, which
// is traces and logs.
type signalConfig struct {
	Exporter string `koanf:"exporter"`
}

// loadConfig resolves the runtime configuration through goga/config.
//
// Precedence is goga's fixed order — defaults, files, environment, flags, later
// beating earlier — and it is a property of config.Load rather than of the
// order these two options are passed. epos-registry supplies the last two: the
// environment, and the parsed flag set, whose defaults are the bottom layer
// because posflag contributes an untouched flag only where nothing else set
// that key. So a flag the operator typed beats the environment, the environment
// beats a flag default, and no arrangement of this call changes that.
//
// There is no file source. epos-registry is configured by flags in the godog
// suite and by the environment in a container, and a config file would be a
// user-visible surface SPEC.md does not describe; adding one is a decision, not
// a consequence of the adoption.
//
// The upstream is checked here rather than with config.WithRequiredKeys because
// --upstream has a default: the key exists whether or not anyone set it, so
// "required" would always be satisfied and never fire.
func loadConfig(ctx context.Context, flags *pflag.FlagSet) (registryConfig, error) {
	cfg, err := config.Load[registryConfig](ctx,
		config.WithEnv(envPrefix),
		config.WithFlags(flags),
	)
	if err != nil {
		return registryConfig{}, err
	}
	if cfg.Value.Upstream == "" {
		return registryConfig{}, errors.New(
			"an upstream registry is required: pass --upstream or set " + upstreamEnv)
	}
	return cfg.Value, nil
}

func run(ctx context.Context, cfg registryConfig) error {
	up, err := upstream.New(cfg.Upstream)
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
		metrics.Config{VersionAttribute: cfg.Metrics.VersionAttribute},
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
		"version", Version, "addr", cfg.Addr, "upstream", cfg.Upstream)
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
func serverOptions(cfg registryConfig, ready func(ctx context.Context) error) []serve.Option {
	opts := []serve.Option{
		serve.WithAddr(cfg.Addr),
		serve.WithReadHeaderTimeout(readHeaderTimeout),
		serve.WithShutdownGrace(shutdownGrace),
	}
	if ready != nil {
		opts = append(opts, serve.WithReadinessCheck("upstream", ready))
	}
	if cfg.OpsAddr != "" {
		opts = append(opts, serve.WithOpsAddr(cfg.OpsAddr))
	}
	return opts
}
