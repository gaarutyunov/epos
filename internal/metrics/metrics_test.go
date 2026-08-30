package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// export is the shape stdoutmetric writes. The godog harness reads the same
// output out of the epos-registry process, so the fields asserted here are a
// contract, not an implementation detail.
type export struct {
	ScopeMetrics []struct {
		Scope struct {
			Name string `json:"Name"`
		} `json:"Scope"`
		Metrics []struct {
			Name string `json:"Name"`
			Data struct {
				IsMonotonic bool `json:"IsMonotonic"`
				DataPoints  []struct {
					Value      int64 `json:"Value"`
					Attributes []struct {
						Key   string `json:"Key"`
						Value struct {
							Value any `json:"Value"`
						} `json:"Value"`
					} `json:"Attributes"`
				} `json:"DataPoints"`
			} `json:"Data"`
		} `json:"Metrics"`
	} `json:"ScopeMetrics"`
}

// collect runs recordings through a real exporter and returns the downloads
// data points it emitted, keyed by their attribute set.
//
// The provider is built here rather than by the package under test: since the
// goga/telemetry adoption, exporter selection belongs to the composition root
// and this package only owns the instrument. What the test exercises is
// therefore the same thing production does — a counter on a meter somebody else
// configured.
func collect(t *testing.T, cfg Config, record func(*Downloads)) (points map[string]int64, monotonic bool, scope string) {
	t.Helper()

	var buf bytes.Buffer
	exporter, err := stdoutmetric.New(stdoutmetric.WithWriter(&buf))
	require.NoError(t, err)
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter)),
	)

	downloads, err := New(provider.Meter(ScopeName), cfg)
	require.NoError(t, err)
	record(downloads)

	// Shutdown flushes, so the test never waits on an interval.
	require.NoError(t, provider.Shutdown(context.Background()))

	points = map[string]int64{}
	dec := json.NewDecoder(&buf)
	for dec.More() {
		var e export
		require.NoError(t, dec.Decode(&e), "decode exporter output")
		for _, sm := range e.ScopeMetrics {
			for _, m := range sm.Metrics {
				if m.Name != "epos.downloads" {
					continue
				}
				monotonic = m.Data.IsMonotonic
				scope = sm.Scope.Name
				for _, dp := range m.Data.DataPoints {
					key := ""
					for _, a := range dp.Attributes {
						key += a.Key + "="
						key += stringify(a.Value.Value) + ";"
					}
					points[key] += dp.Value
				}
			}
		}
	}
	return points, monotonic, scope
}

func stringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestDownloadsCounterIsMonotonicAndCarriesItsAttributes(t *testing.T) {
	points, monotonic, scope := collect(t, Config{}, func(d *Downloads) {
		d.Record(context.Background(), Download{
			Repository: "demo/hello", Verified: true, Client: "epos", Version: "1.0.0",
		})
		d.Record(context.Background(), Download{
			Repository: "demo/hello", Verified: true, Client: "epos", Version: "1.0.0",
		})
	})

	assert.True(t, monotonic, "SPEC.md 5.3 asks for a monotonic counter")
	assert.Equal(t, ScopeName, scope,
		"the counter stays on epos's own instrumentation scope, not goga's")
	require.Len(t, points, 1)
	for key, value := range points {
		assert.EqualValues(t, 2, value)
		for _, want := range []string{`repository="demo/hello"`, `verified=true`, `client="epos"`} {
			assert.Contains(t, key, want)
		}
	}
}

// SPEC.md 5.3: version-valued attributes accumulate without bound under a
// Prometheus exporter, so they are off unless explicitly enabled.
func TestVersionAttributeIsOffByDefault(t *testing.T) {
	record := func(d *Downloads) {
		d.Record(context.Background(), Download{
			Repository: "demo/hello", Client: "oras-go", Version: "1.0.0",
		})
	}

	off, _, _ := collect(t, Config{}, record)
	for key := range off {
		assert.NotContains(t, key, "version=",
			"version-valued attributes are unbounded in cardinality and off by default")
	}

	on, _, _ := collect(t, Config{VersionAttribute: true}, record)
	found := false
	for key := range on {
		if contains(key, `version="1.0.0"`) {
			found = true
		}
	}
	assert.True(t, found, "VersionAttribute on, but no version was recorded: %v", on)
}

// Verified and unverified downloads of the same skill are distinct series, so
// SPEC.md 5.2's split is readable off the counter.
func TestVerifiedAndUnverifiedAreSeparateSeries(t *testing.T) {
	points, _, _ := collect(t, Config{}, func(d *Downloads) {
		d.Record(context.Background(), Download{Repository: "demo/hello", Verified: true, Client: "epos"})
		d.Record(context.Background(), Download{Repository: "demo/hello", Verified: false, Client: "oras-go"})
		d.Record(context.Background(), Download{Repository: "demo/hello", Verified: false, Client: "oras-go"})
	})

	require.Len(t, points, 2, "verified and unverified must be distinct series")
	for key, value := range points {
		want := int64(1)
		if contains(key, "verified=false") {
			want = 2
		}
		assert.Equal(t, want, value, "count for %q", key)
	}
}

// A meter provider with no reader — what `--metrics.exporter none` now produces
// through goga/telemetry — must record nothing and must not panic on the first
// blob fetch.
func TestARecordWithNoReaderConfiguredDoesNotPanic(t *testing.T) {
	downloads, err := New(sdkmetric.NewMeterProvider().Meter(ScopeName), Config{})
	require.NoError(t, err)
	assert.NotPanics(t, func() {
		downloads.Record(context.Background(), Download{Repository: "demo/hello"})
	})
}

// The zero value records nothing rather than dereferencing a nil counter.
func TestZeroValueRecordsNothing(t *testing.T) {
	var downloads *Downloads
	assert.NotPanics(t, func() {
		downloads.Record(context.Background(), Download{Repository: "demo/hello"})
	})
	assert.NotPanics(t, func() {
		(&Downloads{}).Record(context.Background(), Download{Repository: "demo/hello"})
	})
}

// A nil meter is a wiring mistake in the composition root, and is reported as
// one rather than yielding a counter that silently records nowhere.
func TestNilMeterIsRejected(t *testing.T) {
	_, err := New(nil, Config{})
	assert.Error(t, err)
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
