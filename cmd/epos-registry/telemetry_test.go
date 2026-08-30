package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gaarutyunov/goga/serve"
	"github.com/gaarutyunov/goga/telemetry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/epos/internal/metrics"
)

// silentConfig configures every signal to go nowhere, so a test that calls
// telemetry.Setup writes nothing to the process's own output.
func silentConfig() registryConfig {
	return registryConfig{
		Upstream: "http://zot:5000",
		Metrics:  metricsConfig{Exporter: exporterNone},
		Traces:   signalConfig{Exporter: exporterNone},
		Logs:     signalConfig{Exporter: exporterNone},
	}
}

// setup runs the composition root's telemetry wiring and restores what it
// changed process-wide.
//
// telemetry.Setup installs the OpenTelemetry globals and calls
// slog.SetDefault, which also redirects the standard log package. Neither is
// reversible on its own, so the previous default logger is put back by hand —
// otherwise the first test to call Setup silences every later test in this
// binary.
func setup(t *testing.T, cfg registryConfig) (*telemetry.Telemetry, error) {
	t.Helper()

	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })

	tel, cleanup, err := setupTelemetry(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	t.Cleanup(cleanup)
	return tel, nil
}

// SPEC.md 5.3's exporter names are epos's, and survive the adoption: goga calls
// the terminal exporter "console", and only the translation moved.
func TestExporterNamesTranslateToGoga(t *testing.T) {
	assert.Equal(t, "console", gogaExporter(exporterStdout),
		"SPEC.md 5.3 names it stdout; goga names it console")
	for _, passthrough := range []string{exporterNone, exporterStderr, "otlp", "console"} {
		assert.Equal(t, passthrough, gogaExporter(passthrough),
			"a name goga already resolves must not be rewritten")
	}
}

// epos-registry never called otel.SetMeterProvider before this adoption, so its
// counter fed a provider no global reader could ever see. goga installs all
// three signals or none, and hands the providers back.
func TestSetupInstallsAllThreeSignals(t *testing.T) {
	tel, err := setup(t, silentConfig())
	require.NoError(t, err)

	require.NotNil(t, tel.MeterProvider, "a meter provider is always installed")
	assert.NotNil(t, tel.TracerProvider)
	assert.NotNil(t, tel.LoggerProvider)

	// The wiring the registry actually depends on: epos.downloads is built off
	// the returned provider, on epos's own instrumentation scope.
	downloads, err := metrics.New(
		tel.MeterProvider.Meter(metrics.ScopeName), metrics.Config{})
	require.NoError(t, err)
	assert.NotPanics(t, func() {
		downloads.Record(context.Background(), metrics.Download{Repository: "demo/hello"})
	})
}

// "stderr" is not one of goga's standard names. It resolves because epos
// registers it, which is how a consumer supplies an exporter goga's own table
// does not have — here, a console log exporter that writes to stderr so stdout
// stays the metrics channel the godog suite parses.
func TestStderrLogExporterResolvesThroughTheRegistry(t *testing.T) {
	cfg := silentConfig()
	cfg.Logs.Exporter = exporterStderr

	_, err := setup(t, cfg)
	require.NoError(t, err)
}

// SPEC.md 5.3's production scrape. The endpoint is goga/serve's, mounted on
// the operational mux and never traced; what makes it worth exposing is that
// epos.downloads reaches it, which is the Prometheus reader's doing and not
// the endpoint's.
func TestDownloadsReachThePrometheusScrape(t *testing.T) {
	tel, err := setup(t, silentConfig())
	require.NoError(t, err)

	downloads, err := metrics.New(
		tel.MeterProvider.Meter(metrics.ScopeName), metrics.Config{})
	require.NoError(t, err)
	downloads.Record(t.Context(), metrics.Download{Repository: "demo/hello"})

	srv, err := serve.New(t.Context(), http.NotFoundHandler())
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	srv.Ops().ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, serve.MetricsPath, nil))

	require.Equal(t, http.StatusOK, rec.Code, "the scrape failed:\n%s", rec.Body)
	assert.Contains(t, rec.Body.String(), `epos_downloads_total{`,
		"SPEC.md 5.3's instrument is what a scrape is for")
	assert.Contains(t, rec.Body.String(), `repository="demo/hello"`)
}

func TestUnknownExporterIsRejected(t *testing.T) {
	cfg := silentConfig()
	cfg.Metrics.Exporter = "carrier-pigeon"

	_, err := setup(t, cfg)
	require.Error(t, err, "an unknown exporter must be rejected")
	assert.ErrorIs(t, err, telemetry.ErrUnknownExporter)
}

// --metrics.interval reaches the SDK's periodic reader only through
// OTEL_METRIC_EXPORT_INTERVAL: goga exposes no option for it, and the exporter
// it takes from the registry is wrapped in a reader the consumer never sees.
func TestMetricIntervalReachesTheSDKThroughTheEnvironment(t *testing.T) {
	t.Setenv(envMetricInterval, "")

	require.NoError(t, applyMetricInterval(200*time.Millisecond))
	assert.Equal(t, "200", os.Getenv(envMetricInterval),
		"the SDK reads this variable in milliseconds")
}

func TestSubMillisecondIntervalIsLeftUnset(t *testing.T) {
	t.Setenv(envMetricInterval, "")

	for _, interval := range []time.Duration{0, 999 * time.Microsecond} {
		require.NoError(t, applyMetricInterval(interval))
		assert.Empty(t, os.Getenv(envMetricInterval),
			"an interval that rounds to zero milliseconds would be rejected by the SDK")
	}
}
