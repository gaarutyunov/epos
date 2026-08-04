package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// Traces exporter names accepted by Config.TracesExporter.
//
// none is the default, and it is a configuration rather than a degraded mode:
// an operator who does not want to run a collector and a store gets a registry
// that counts to its metrics exporter and stores nothing.
const (
	TracesExporterOTLP = "otlp"
	TracesExporterNone = "none"
)

// ServiceName is what the span is attributed to, and the rollup that turns
// spans into counts filters on it. Changing it silently empties the catalog's
// numbers, so it is a constant rather than a setting.
const ServiceName = "epos-registry"

// SpanName is the download event. The rollup filters on this too.
const SpanName = "epos.download"

// defaultTraceTimeout bounds one export attempt.
const defaultTraceTimeout = 10 * time.Second

// newTracer builds the tracer provider that carries the download span.
//
// # Why a span at all, when there is already a counter
//
// The counter lives in an exporter's pipeline and dies with the process. A span
// is an *event*: one row per download, with a timestamp, which makes the
// catalog's count a `count()` and its hourly bucketing a `GROUP BY` rather than
// a second instrument. It also avoids the cumulative-versus-delta temporality
// problem entirely — N replicas and several process lifetimes writing cumulative
// counter rows into one table is a `sum()` that double-counts, and there is no
// such question about events.
//
// # Why HTTP rather than gRPC
//
// Measured rather than assumed, because the obvious reason to prefer one is
// weight and weight is not the difference: otlptracegrpc resolves to 375
// packages across 21 modules and otlptracehttp to 374 across the same 21 — and
// the HTTP exporter's own closure contains 65 gRPC packages, so choosing it
// saves nothing. What differs is what they traverse. OTLP over HTTP is an
// ordinary POST that a corporate proxy or a TLS-terminating load balancer
// forwards without special configuration; gRPC needs HTTP/2 end to end. The
// collector configuration in deploy/ listens on both, so an operator who
// prefers gRPC changes one endpoint.
func newTracer(ctx context.Context, cfg Config) (trace.Tracer, func(context.Context) error, error) {
	name := cfg.TracesExporter
	if name == "" {
		name = TracesExporterNone
	}
	if name == TracesExporterNone {
		return nil, func(context.Context) error { return nil }, nil
	}
	if name != TracesExporterOTLP {
		return nil, nil, fmt.Errorf("traces exporter %q is not implemented; use %q or %q",
			name, TracesExporterOTLP, TracesExporterNone)
	}

	opts := []otlptracehttp.Option{}
	if cfg.TracesEndpoint != "" {
		opts = append(opts, otlptracehttp.WithEndpoint(cfg.TracesEndpoint))
	}
	if cfg.TracesInsecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}
	timeout := cfg.TracesTimeout
	if timeout <= 0 {
		timeout = defaultTraceTimeout
	}
	opts = append(opts, otlptracehttp.WithTimeout(timeout))

	// Deliberately not otlptracehttp.NewClient + a blocking dial. Construction
	// must not reach the collector: a registry that would not start because a
	// telemetry backend was down would have made an availability problem out of
	// an observability feature. The exporter connects lazily and the batch
	// processor below drops spans it cannot deliver.
	exporter, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("otlp trace exporter: %w", err)
	}

	provider := sdktrace.NewTracerProvider(
		// AlwaysSample, deliberately, and this is load-bearing rather than a
		// default left in place. Every span is a download and the catalog counts
		// spans, so sampling this at 10% would not make the leaderboard
		// approximate — it would make it wrong, by a factor nothing on the page
		// discloses. If volume ever forces a reduction the answer is
		// pre-aggregation in the collector, never a sampled count.
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(downloadResource()),
	)
	return provider.Tracer("github.com/gaarutyunov/epos"), provider.Shutdown, nil
}

// record emits one download span.
//
// Called from the same function that increments the counter, with the same
// attribute set, which is what makes "one instrumentation path" true. A second
// recording site would be two descriptions of one event that drift.
//
// The span is minimal on purpose: one name, three attributes, no events, no
// links, no HTTP semantics. This is a measurement that travels as a span, not a
// tracing deployment — and in particular the relay is never wrapped in
// otelhttp, which would record http.user_agent on a server span of its own and
// silently reintroduce exactly what dropping the client attribute removes.
func (d *Downloads) record(ctx context.Context, attrs []attribute.KeyValue) {
	if d.tracer == nil {
		return
	}
	_, span := d.tracer.Start(ctx, SpanName,
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(attrs...),
	)
	span.End()
}
