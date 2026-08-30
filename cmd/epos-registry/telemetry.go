package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gaarutyunov/goga/registry"
	"github.com/gaarutyunov/goga/telemetry"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutlog"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// The exporter names epos-registry accepts, which are SPEC.md 5.3's names and
// not goga's.
//
// goga calls the write-to-the-terminal exporter "console", after the
// OTEL_<SIGNAL>_EXPORTER value the OpenTelemetry specification defines. SPEC.md
// 5.3 called it "stdout" a year earlier, and it is on the command line, in the
// CI workflow and in the godog suite. The name is translated here rather than
// changed everywhere: an adopted dependency's vocabulary is not a reason to
// break a documented flag.
const (
	exporterStdout = "stdout"
	exporterStderr = "stderr"
	exporterNone   = "none"
)

// gogaExporter translates an epos exporter name into the name goga resolves.
//
// Anything goga already knows — "otlp", "console", "none" — passes through
// unchanged, so the exporters SPEC.md 5.3 lists for production are reachable
// without epos maintaining a table that drifts from goga's.
func gogaExporter(name string) string {
	if name == exporterStdout {
		return "console"
	}
	return name
}

// envMetricInterval is the OpenTelemetry SDK's export-interval variable, read
// in milliseconds by the periodic reader goga wraps every push exporter in.
//
// It is here because it is the only way to reach the interval. goga has no
// WithMetricInterval, and its exporter registry hands back an sdkmetric.Exporter
// that goga itself wraps in sdkmetric.NewPeriodicReader with no options — so a
// consumer cannot supply a configured reader either. --metrics.interval predates
// the adoption and the godog suite depends on it (a scenario cannot wait on the
// SDK's minute-long default), so the flag survives by setting the variable the
// SDK reads. See the PR body: this is the sharpest edge of the adoption.
const envMetricInterval = "OTEL_METRIC_EXPORT_INTERVAL"

// logExporterSettings is the settings type of epos's registered exporters.
//
// They take no settings; goga opens a registry exporter with an empty subtree,
// which the registry decodes to the zero value without ever calling the
// decoder.
type logExporterSettings struct{}

// newExporterRegistry builds the exporter registry goga resolves names through.
//
// It exists for one exporter: "stderr". goga's own "console" log exporter is
// stdoutlog.New() with no writer option, and epos-registry's two output streams
// are not interchangeable — stdout carries the metric exports the godog suite
// parses (SPEC.md 5.3), stderr carries operator output, which is where
// log.Printf used to write. Registering the exporter is goga's documented way
// to supply what its standard table does not, and keeps the split intact.
func newExporterRegistry() (*registry.Registry, error) {
	// registry.New panics on a nil decoder, and these adapters have no
	// settings to decode: the decoder is unreachable, and says so if the
	// assumption ever stops holding.
	reg := registry.New(func(registry.Settings, any) error {
		return errors.New("epos-registry: telemetry exporters take no settings")
	})

	err := telemetry.RegisterLogExporter(reg, exporterStderr,
		func(context.Context, logExporterSettings) (sdklog.Exporter, error) {
			return stdoutlog.New(stdoutlog.WithWriter(os.Stderr))
		})
	if err != nil {
		return nil, fmt.Errorf("epos-registry: %w", err)
	}
	return reg, nil
}

// setupTelemetry configures OpenTelemetry for the process and returns the
// cleanup that flushes it.
//
// One call establishes all three signals. epos-registry asked for metrics and
// gets a tracer and a logger with them, because goga installs the three
// together or not at all — and that is the point: epos never called
// otel.SetMeterProvider, so before this its counter fed a provider that was
// never installed globally. There is no path through telemetry.Setup that
// installs a tracer without also installing a meter.
func setupTelemetry(ctx context.Context, cfg registryConfig) (*telemetry.Telemetry, func(), error) {
	if err := applyMetricInterval(cfg.Metrics.Interval); err != nil {
		return nil, nil, err
	}

	reg, err := newExporterRegistry()
	if err != nil {
		return nil, nil, err
	}

	tel, cleanup, err := telemetry.Setup(ctx,
		telemetry.WithServiceName("epos-registry"),
		telemetry.WithServiceVersion(Version),
		telemetry.WithExporterRegistry(reg),
		telemetry.WithMetricExporter(gogaExporter(cfg.Metrics.Exporter)),
		telemetry.WithTraceExporter(gogaExporter(cfg.Traces.Exporter)),
		telemetry.WithLogExporter(gogaExporter(cfg.Logs.Exporter)),
		// The scrape path SPEC.md 5.3 deferred until there was an endpoint to
		// scrape. goga/serve now mounts /metrics on the operational mux
		// unconditionally, and this reader is what puts epos.downloads on it:
		// it registers a collector with prometheus.DefaultRegisterer, which is
		// the registry goga's promhttp handler gathers from. Leaving it off
		// would ship an endpoint exporting Go runtime counters and not the one
		// instrument the registry exists to produce.
		//
		// It is additive to --metrics.exporter, not an alternative to it: both
		// readers feed the same meter provider, so a stdout run still writes
		// the stream the godog suite parses.
		telemetry.WithPrometheus(true),
		telemetry.WithShutdownTimeout(shutdownGrace),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("epos-registry: telemetry: %w", err)
	}
	return tel, cleanup, nil
}

// applyMetricInterval publishes --metrics.interval to the SDK.
//
// A sub-millisecond interval is not expressible in the variable's unit and is
// treated as unset rather than silently rounded to zero, which the SDK would
// reject.
func applyMetricInterval(interval time.Duration) error {
	millis := interval.Milliseconds()
	if millis < 1 {
		return nil
	}
	if err := os.Setenv(envMetricInterval, strconv.FormatInt(millis, 10)); err != nil {
		return fmt.Errorf("epos-registry: set %s: %w", envMetricInterval, err)
	}
	return nil
}
