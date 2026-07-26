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
}

// Publish is one accepted manifest PUT (SPEC.md 5.4).
type Publish struct {
	// Repository is the OCI repository name, which identifies the skill.
	Repository string
	// ArtifactType is read from the relayed manifest body.
	ArtifactType string
	// ReferenceKind is "tag" or "digest".
	ReferenceKind string
}

// Downloads records the epos.downloads counter of SPEC.md 5.1.
//
// The zero value is usable and records nothing, so a caller with metrics
// disabled needs no nil checks.
type Downloads struct {
	counter          metric.Int64Counter
	publishes        metric.Int64Counter
	versionAttribute bool
}

// Download is one counted blob fetch.
type Download struct {
	// Repository is the OCI repository name, which identifies the skill —
	// SPEC.md 5.1 is explicit that no manifest parsing is required.
	Repository string
	// Verified is true iff the request carried a well-formed Epos-Download
	// header (SPEC.md 5.2).
	Verified bool
	// Client is the request's User-Agent.
	Client string
	// Version is the version from Epos-Download, recorded only when
	// Config.VersionAttribute is on.
	Version string
}

// New builds the meter provider and the epos.downloads counter.
//
// The returned shutdown function flushes pending metrics and must be called
// before the process exits, or the last interval's counts are lost.
func New(ctx context.Context, cfg Config) (*Downloads, func(context.Context) error, error) {
	name := cfg.Exporter
	if name == "" {
		name = ExporterStdout
	}

	if name == ExporterNone {
		return &Downloads{}, func(context.Context) error { return nil }, nil
	}
	if name != ExporterStdout {
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

	// 5.4: a publish is a manifest PUT upstream accepts with 201. Blob uploads
	// are deliberately not counted -- content addressing means a new version
	// reusing an existing layer uploads nothing, so upload volume does not
	// track publishing.
	publishes, err := meter.Int64Counter(
		"epos.publishes",
		metric.WithDescription("Manifest PUTs upstream accepted, relayed by epos-registry."),
		metric.WithUnit("{publish}"),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("epos.publishes counter: %w", err)
	}

	_ = ctx
	return &Downloads{
		counter:          counter,
		publishes:        publishes,
		versionAttribute: cfg.VersionAttribute,
	}, provider.Shutdown, nil
}

// Record adds one to epos.downloads.
//
// The counter is monotonic: SPEC.md 5.3 asks for a monotonic counter, and
// Int64Counter is one by construction — there is no decrement to call.
func (d *Downloads) Record(ctx context.Context, dl Download) {
	if d == nil || d.counter == nil {
		return
	}

	attrs := []attribute.KeyValue{
		attribute.String("repository", dl.Repository),
		attribute.Bool("verified", dl.Verified),
		attribute.String("client", dl.Client),
	}
	if d.versionAttribute && dl.Version != "" {
		attrs = append(attrs, attribute.String("version", dl.Version))
	}

	d.counter.Add(ctx, 1, metric.WithAttributes(attrs...))
}

// RecordPublish adds one to epos.publishes (SPEC.md 5.4).
//
// Publishes per repository answers "which skill changes most often" directly,
// which is why the repository is an attribute and the version is not.
func (d *Downloads) RecordPublish(ctx context.Context, p Publish) {
	if d == nil || d.publishes == nil {
		return
	}
	d.publishes.Add(ctx, 1, metric.WithAttributes(
		attribute.String("repository", p.Repository),
		attribute.String("artifact_type", p.ArtifactType),
		attribute.String("reference_kind", p.ReferenceKind),
	))
}
