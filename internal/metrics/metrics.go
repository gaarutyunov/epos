// Package metrics implements the OTel instruments and exporters of SPEC.md 5.
//
// One instrumentation path: the OpenTelemetry Go SDK, with the exporter chosen
// by configuration (5.3). Nothing here holds state that outlives a process —
// a counter lives in the exporter's pipeline, not in a store shared between
// replicas, so 4.4 still holds.
package metrics

import (
	"context"
	"fmt"
	"io"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/exemplar"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Exporter names accepted by Config.Exporter (SPEC.md 5.3). prometheus and otlp
// are production exporters and arrive with the deployment story; A1 needs
// stdout, for godog runs and local development.
const (
	ExporterStdout = "stdout"
	ExporterNone   = "none"
)

// Config selects the exporter and the attribute set.
type Config struct {
	// Exporter is one of the Exporter* constants. Empty means stdout.
	Exporter string

	// Interval is how often a periodic exporter emits. Zero means the SDK
	// default.
	Interval time.Duration

	// Out is where the stdout exporter writes. Nil means os.Stdout.
	Out io.Writer

	// VersionAttribute adds the skill version to each download.
	//
	// Off by default and deliberately so: SPEC.md 5.3 calls out that
	// version-valued attributes accumulate without bound under a Prometheus
	// exporter, one time series per version per repository, forever.
	VersionAttribute bool

	// TracesExporter selects the download span's exporter: none (the default)
	// or otlp. Independent of Exporter above — an operator may want numbers in
	// a store without a metrics pipeline, or the reverse, and neither implies
	// the other.
	TracesExporter string

	// TracesEndpoint is the collector's OTLP HTTP endpoint as host:port. Empty
	// uses the OTLP default.
	TracesEndpoint string

	// TracesInsecure sends to the collector over plain HTTP. For a collector on
	// the same host or the same private network, which is where one usually is.
	TracesInsecure bool

	// TracesTimeout bounds one export attempt.
	TracesTimeout time.Duration
}

// Downloads records the epos.downloads counter of SPEC.md 5.1.
//
// The zero value is usable and records nothing, so a caller with metrics
// disabled needs no nil checks.
type Downloads struct {
	counter          metric.Int64Counter
	tracer           trace.Tracer
	versionAttribute bool
}

// Download is one counted blob fetch.
type Download struct {
	// Repository is the OCI repository name, which identifies the skill —
	// SPEC.md 5.1 is explicit that no manifest parsing is required.
	Repository string
	// Verified is true iff the request carried a well-formed Epos-Download
	// header (SPEC.md 5.2).
	//
	// The unverified side of this attribute is known to be inflated, and
	// signatures are the largest single source: a cosign signature is a
	// referrer of the skill manifest (SPEC.md 11), so its blob shares the
	// skill's repository, and every `epos verify` fetches one. Those fetches
	// are counted here as unverified downloads of the skill and cannot be
	// distinguished without a digest→role table, which is the durable state
	// SPEC.md 4.4 refuses. See cmd/epos-registry's countDownload for the
	// consequences and how to read the numbers.
	Verified bool
	// Version is the version from Epos-Download, recorded only when
	// Config.VersionAttribute is on.
	Version string
}

// There is deliberately no Client field.
//
// It used to carry the request's raw User-Agent. Under a metrics exporter that
// was unbounded cardinality — one time series per distinct User-Agent per
// repository, forever, created by anyone who can issue a blob GET. Once a
// download became a durable row it stopped being a cardinality problem and
// became a data one: a registry with a public read path storing arbitrary
// attacker-supplied text on behalf of anyone who can curl a blob, retained for
// the store's TTL, in a table somebody will eventually put a dashboard on.
//
// Removing the field rather than filtering the attribute is the point: there is
// nothing left to set, so it cannot come back by someone adding a line. The
// allow-list view below is the other half, and it guards the attributes nobody
// has thought of yet.
//
// Bucketing it into an enum was considered and rejected: epos/oras/docker/other
// duplicates `verified` — a request from `epos pull` is exactly a request
// carrying Epos-Download — and adds a parsing rule that rots on every client
// release.

// New builds the meter provider and the epos.downloads counter.
//
// The returned shutdown function flushes pending metrics and must be called
// before the process exits, or the last interval's counts are lost.
func New(ctx context.Context, cfg Config) (*Downloads, func(context.Context) error, error) {
	tracer, shutdownTraces, err := newTracer(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	name := cfg.Exporter
	if name == "" {
		name = ExporterStdout
	}

	if name == ExporterNone {
		// versionAttribute travels with the span too. Leaving it off here made
		// `--metrics.exporter none --traces.exporter otlp` — the configuration
		// an operator who wants a store and no metrics pipeline actually runs —
		// silently drop the version from every span.
		return &Downloads{
			tracer:           tracer,
			versionAttribute: cfg.VersionAttribute,
		}, shutdownTraces, nil
	}
	if name != ExporterStdout {
		// The tracer provider is already up, so it is shut down rather than
		// leaked on the way out.
		_ = shutdownTraces(ctx)
		return nil, nil, fmt.Errorf("metrics exporter %q is not implemented; use %q or %q",
			name, ExporterStdout, ExporterNone)
	}

	opts := []stdoutmetric.Option{}
	if cfg.Out != nil {
		opts = append(opts, stdoutmetric.WithWriter(cfg.Out))
	}
	exporter, err := stdoutmetric.New(opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("stdout metric exporter: %w", err)
	}

	readerOpts := []sdkmetric.PeriodicReaderOption{}
	if cfg.Interval > 0 {
		readerOpts = append(readerOpts, sdkmetric.WithInterval(cfg.Interval))
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, readerOpts...)),
		sdkmetric.WithResource(downloadResource()),
		// An allow-list, not a deny-list, and applied unconditionally rather
		// than per exporter. Written this way so that an attribute added later
		// is excluded until somebody decides otherwise — which is the opposite
		// of how the client attribute got in.
		sdkmetric.WithView(sdkmetric.NewView(
			sdkmetric.Instrument{Name: "epos.downloads"},
			sdkmetric.Stream{
				AttributeFilter: attribute.NewAllowKeysFilter(
					"repository", "verified", "version"),
			},
		)),
		// A view's filtered-out attributes may still ride along on exemplars,
		// which record the dropped measurement attributes — so the filter above
		// would be silently undone by an exemplar carrying what it removed.
		sdkmetric.WithExemplarFilter(exemplar.AlwaysOffFilter),
	)

	meter := provider.Meter("github.com/gaarutyunov/epos")
	counter, err := meter.Int64Counter(
		"epos.downloads",
		metric.WithDescription("Content blob fetches answered by epos-registry."),
		metric.WithUnit("{download}"),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("epos.downloads counter: %w", err)
	}

	shutdown := func(ctx context.Context) error {
		// Both, and the metric error is not allowed to hide the trace one: a
		// deploy that lost its last interval of spans should say so.
		metricErr := provider.Shutdown(ctx)
		if traceErr := shutdownTraces(ctx); traceErr != nil {
			return traceErr
		}
		return metricErr
	}
	return &Downloads{
		counter:          counter,
		tracer:           tracer,
		versionAttribute: cfg.VersionAttribute,
	}, shutdown, nil
}

// downloadResource identifies the process the download came from.
//
// service.name is what the rollup filters on, so it is not decoration: a
// resource without it produces spans the catalog's view never selects, and the
// symptom is a leaderboard of zeroes with everything apparently working.
func downloadResource() *resource.Resource {
	return resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(ServiceName),
	)
}

// Record adds one to epos.downloads.
//
// The counter is monotonic: SPEC.md 5.3 asks for a monotonic counter, and
// Int64Counter is one by construction — there is no decrement to call.
func (d *Downloads) Record(ctx context.Context, dl Download) {
	if d == nil {
		return
	}

	// One attribute set, built once, used by both emissions. This is what makes
	// SPEC.md 5.3's "one instrumentation path" true: the counter and the span
	// cannot describe different events, because there is one call and one list.
	attrs := d.attributes(dl)

	if d.counter != nil {
		d.counter.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
	d.record(ctx, attrs)
}

// attributes is the download's attribute set, for the counter and the span
// alike.
func (d *Downloads) attributes(dl Download) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("repository", dl.Repository),
		attribute.Bool("verified", dl.Verified),
	}
	if d.versionAttribute && dl.Version != "" {
		attrs = append(attrs, attribute.String("version", dl.Version))
	}
	return attrs
}
