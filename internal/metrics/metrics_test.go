package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// export is the shape stdoutmetric writes. The godog harness reads the same
// output out of the epos-registry process, so the fields asserted here are a
// contract, not an implementation detail.
type export struct {
	ScopeMetrics []struct {
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
func collect(t *testing.T, cfg Config, record func(*Downloads)) (points map[string]int64, monotonic bool) {
	t.Helper()

	var buf bytes.Buffer
	cfg.Out = &buf

	downloads, shutdown, err := New(context.Background(), cfg)
	require.NoError(t, err)
	record(downloads)

	// Shutdown flushes, so the test never waits on an interval.
	require.NoError(t, shutdown(context.Background()))

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
	return points, monotonic
}

func stringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestDownloadsCounterIsMonotonicAndCarriesItsAttributes(t *testing.T) {
	points, monotonic := collect(t, Config{}, func(d *Downloads) {
		d.Record(context.Background(), Download{
			Repository: "demo/hello", Verified: true, Client: "epos", Version: "1.0.0",
		})
		d.Record(context.Background(), Download{
			Repository: "demo/hello", Verified: true, Client: "epos", Version: "1.0.0",
		})
	})

	assert.True(t, monotonic, "SPEC.md 5.3 asks for a monotonic counter")
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

	off, _ := collect(t, Config{}, record)
	for key := range off {
		assert.NotContains(t, key, "version=",
			"version-valued attributes are unbounded in cardinality and off by default")
	}

	on, _ := collect(t, Config{VersionAttribute: true}, record)
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
	points, _ := collect(t, Config{}, func(d *Downloads) {
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

// The "none" exporter is how a deployment turns counting off; it must not be a
// nil pointer waiting to panic on the first blob fetch.
func TestNoneExporterRecordsNothingAndDoesNotPanic(t *testing.T) {
	downloads, shutdown, err := New(context.Background(), Config{Exporter: ExporterNone})
	require.NoError(t, err)
	downloads.Record(context.Background(), Download{Repository: "demo/hello"})
	assert.NoError(t, shutdown(context.Background()))
}

func TestUnknownExporterIsRejected(t *testing.T) {
	_, _, err := New(context.Background(), Config{Exporter: "carrier-pigeon"})
	assert.Error(t, err, "an unknown exporter must be rejected")
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
