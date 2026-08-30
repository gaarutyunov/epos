// Package metrics implements the OTel instruments of SPEC.md 5.
//
// One instrumentation path: the OpenTelemetry Go SDK, configured once by
// github.com/gaarutyunov/goga/telemetry in the composition root (5.3). This
// package owns the instrument and its attribute set and nothing else — it is
// handed a meter, it does not build one. Nothing here holds state that outlives
// a process — a counter lives in the exporter's pipeline, not in a store shared
// between replicas, so 4.4 still holds.
package metrics

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// ScopeName is the instrumentation scope epos.downloads is recorded under.
//
// It is epos's own module path rather than goga's, which is why the composition
// root asks the returned *telemetry.Telemetry for its MeterProvider and takes a
// meter from it instead of using the ready-made Telemetry.Meter: the latter is
// scoped to goga, and the scope is part of what an operator reads off the
// export.
const ScopeName = "github.com/gaarutyunov/epos"

// Config selects the attribute set.
type Config struct {
	// VersionAttribute adds the skill version to each download.
	//
	// Off by default and deliberately so: SPEC.md 5.3 calls out that
	// version-valued attributes accumulate without bound under a Prometheus
	// exporter, one time series per version per repository, forever.
	VersionAttribute bool
}

// Downloads records the epos.downloads counter of SPEC.md 5.1.
//
// The zero value is usable and records nothing, so a caller with metrics
// disabled needs no nil checks.
type Downloads struct {
	counter          metric.Int64Counter
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
	// Client is the request's User-Agent.
	Client string
	// Version is the version from Epos-Download, recorded only when
	// Config.VersionAttribute is on.
	Version string
}

// New builds the epos.downloads counter on meter.
//
// The meter comes from the caller because exporter selection, the resource and
// the reader are goga/telemetry's business, not this package's: a nil meter is
// a wiring mistake in the composition root, not a runtime condition, and is
// reported as an error rather than silently degraded to a no-op counter.
func New(meter metric.Meter, cfg Config) (*Downloads, error) {
	if meter == nil {
		return nil, fmt.Errorf("epos/metrics: a meter is required")
	}

	counter, err := meter.Int64Counter(
		"epos.downloads",
		metric.WithDescription("Content blob fetches answered by epos-registry."),
		metric.WithUnit("{download}"),
	)
	if err != nil {
		return nil, fmt.Errorf("epos/metrics: epos.downloads counter: %w", err)
	}

	return &Downloads{counter: counter, versionAttribute: cfg.VersionAttribute}, nil
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
