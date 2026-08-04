package metrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The span is off unless asked for, and asking for something unimplemented says
// so rather than silently recording nothing.
func TestTheSpanIsOffByDefault(t *testing.T) {
	tracer, shutdown, err := newTracer(t.Context(), Config{})
	require.NoError(t, err)
	assert.Nil(t, tracer, "no traces exporter means no tracer, not a no-op one")
	require.NoError(t, shutdown(t.Context()))

	_, _, err = newTracer(t.Context(), Config{TracesExporter: "jaeger"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jaeger")
	assert.Contains(t, err.Error(), TracesExporterOTLP)
}

// 3.3: exporting must not block a request or fail one.
//
// A collector that is unreachable costs telemetry, never availability. Pointed
// at a dead address, Record still returns promptly and the caller — which in
// the registry is a request handler on the relay path — is unaffected.
func TestADeadCollectorCostsTelemetryAndNothingElse(t *testing.T) {
	// Port 1 on the loopback: nothing listens, and a connection is refused
	// rather than left hanging, so this measures the export path rather than
	// the operating system's timeout.
	downloads, shutdown, err := New(t.Context(), Config{
		Exporter:       ExporterNone,
		TracesExporter: TracesExporterOTLP,
		TracesEndpoint: "127.0.0.1:1",
		TracesInsecure: true,
		TracesTimeout:  100 * time.Millisecond,
	})
	require.NoError(t, err, "construction must not reach the collector")
	require.NotNil(t, downloads)

	done := make(chan struct{})
	go func() {
		for range 50 {
			downloads.Record(context.Background(), Download{
				Repository: "demo/hello", Verified: true,
			})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("recording a download blocked on an unreachable collector")
	}

	// Shutdown flushes, so it may report the failure — that is telemetry
	// reporting a telemetry problem, and it must not panic or hang.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

// The span reaches a collector, carries the name and attributes the rollup
// filters on, and carries no user agent.
//
// An httptest server standing in for the collector, because what is under test
// is what epos puts on the wire — not what ClickHouse does with it afterwards.
func TestTheDownloadSpanCarriesWhatTheRollupReads(t *testing.T) {
	var body atomic.Value
	var seen atomic.Int32

	collector := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			raw := make([]byte, 1<<20)
			n, _ := r.Body.Read(raw)
			body.Store(string(raw[:n]))
			seen.Add(1)
			w.WriteHeader(http.StatusOK)
		}))
	defer collector.Close()

	downloads, shutdown, err := New(t.Context(), Config{
		Exporter:         ExporterNone,
		TracesExporter:   TracesExporterOTLP,
		TracesEndpoint:   strings.TrimPrefix(collector.URL, "http://"),
		TracesInsecure:   true,
		VersionAttribute: true,
	})
	require.NoError(t, err)

	downloads.Record(context.Background(), Download{
		Repository: "demo/agent-skills/pdf",
		Verified:   true,
		Version:    "1.2.0",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, shutdown(ctx), "the shutdown flush is what delivers the last spans")

	require.Positive(t, seen.Load(), "no span reached the collector")
	payload, _ := body.Load().(string)

	// Protobuf, but the strings are plain in the encoding, so the assertions
	// are on what a reader of the rollup would look for.
	assert.Contains(t, payload, SpanName, "the rollup filters on the span name")
	assert.Contains(t, payload, ServiceName, "and on service.name")
	assert.Contains(t, payload, "demo/agent-skills/pdf")
	assert.Contains(t, payload, "repository")
	assert.Contains(t, payload, "verified")
	assert.Contains(t, payload, "1.2.0")
}

// The attribute that must never reach a store, asserted on the wire.
//
// There is no Client field to set any more, so this cannot be written as "set
// it and check it is dropped". What it checks instead is that nothing else puts
// a user agent on the span — which is the failure mode that matters, because it
// is what wrapping the relay in otelhttp would do.
func TestNoUserAgentReachesTheCollector(t *testing.T) {
	var body atomic.Value
	collector := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			raw := make([]byte, 1<<20)
			n, _ := r.Body.Read(raw)
			body.Store(string(raw[:n]))
			w.WriteHeader(http.StatusOK)
		}))
	defer collector.Close()

	downloads, shutdown, err := New(t.Context(), Config{
		Exporter:       ExporterNone,
		TracesExporter: TracesExporterOTLP,
		TracesEndpoint: strings.TrimPrefix(collector.URL, "http://"),
		TracesInsecure: true,
	})
	require.NoError(t, err)

	downloads.Record(context.Background(), Download{Repository: "demo/hello"})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, shutdown(ctx))

	payload, _ := body.Load().(string)
	for _, forbidden := range []string{"user_agent", "User-Agent", "client"} {
		assert.NotContains(t, payload, forbidden,
			"a caller-supplied user agent must never become a durable row")
	}
}
