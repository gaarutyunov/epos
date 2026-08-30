package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gaarutyunov/goga/serve"
	"github.com/gaarutyunov/goga/serve/servetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// serveConfig is the configuration the server assertions run against. The
// address is ignored: servetest binds a loopback port of its own and appends
// its own serve.WithAddr after these options.
func serveConfig() config {
	return config{addr: ":8080", upstreamURL: "http://zot:5000"}
}

// startServer runs epos-registry's real handler under epos-registry's real
// server options.
//
// Nothing is stubbed but the upstream: the handler is the one newRootCommand
// builds, and the options are the ones run passes to serve.New, so a property
// asserted here is a property of the command as it ships.
func startServer(t *testing.T, ready func(ctx context.Context) error) *servetest.Harness {
	t.Helper()
	ctrl := gomock.NewController(t)
	h := newHandler(Version, NewMockrelayer(ctrl), nil)
	return servetest.Start(t.Context(), t, h, serverOptions(serveConfig(), ready)...)
}

// The OCI Distribution API is traced, and traced once. Twice would mean epos
// had wrapped its own handler in otelhttp before handing it to goga — two
// nested server spans, which in a backend look like a slow service with a
// mysterious inner hop rather than like a bug.
func TestAPIRequestIsTracedExactlyOnce(t *testing.T) {
	h := startServer(t, nil)
	h.AssertTracedOnce("/v2/")
}

// SPEC.md 5.3 pays per span. A liveness probe every second is not a request
// the registry received, and goga/serve dispatches the operational endpoints
// before the instrumented handler is reached so that none of them can become
// one. There is no option that moves them inside; this asserts the wiring
// actually holds for epos's own server.
func TestOperationalEndpointsAreNotTraced(t *testing.T) {
	h := startServer(t, nil)
	h.AssertOpsPathsNotTraced()
}

// The registry previously set ReadHeaderTimeout on its own *http.Server. The
// bound survives the move to serve.WithReadHeaderTimeout, and is asserted on
// the wire rather than by reading the field back.
func TestReadHeaderTimeoutIsEnforced(t *testing.T) {
	h := startServer(t, nil)
	h.AssertHeaderTimeoutEnforced(readHeaderTimeout + 5*time.Second)
}

// A pull in flight when SIGTERM arrives finishes. This is what the old
// shutdown goroutine did by hand and what serve.WithShutdownGrace does now.
func TestInFlightRequestSurvivesTheDrain(t *testing.T) {
	servetest.AssertDrainsInFlightRequest(t.Context(), t,
		serverOptions(serveConfig(), nil)...)
}

// SPEC.md 4.4: epos-registry holds no state, so it can serve nothing while its
// upstream is unreachable. That is a readiness condition, not a liveness one —
// the instance leaves the rotation and keeps running.
func TestReadinessReportsTheUpstream(t *testing.T) {
	down := errors.New("connection refused")
	h := startServer(t, func(context.Context) error { return down })

	status, body := h.Get(serve.ReadyzPath)
	assert.Equal(t, http.StatusServiceUnavailable, status,
		"an unreachable upstream takes the instance out of rotation")
	assert.Contains(t, body, "upstream: "+down.Error(),
		"the probe body names the check that failed")

	status, _ = h.Get(serve.LivezPath)
	assert.Equal(t, http.StatusOK, status,
		"an unreachable upstream is not a reason to restart the process")
}

// SPEC.md 4.3's Epos-Version is set by epos's own middleware, which lives
// inside the application handler — and the operational endpoints are
// dispatched before it. The header is therefore a property of the OCI
// Distribution API surface, not of the port, and this pins that reading down
// so the next reader of 4.3 does not have to rediscover it.
//
// It cannot be arranged otherwise: serve.WithMiddleware does not see an
// operational request either, and re-registering a path on Server.Ops panics
// rather than replacing goga's handler.
func TestOperationalEndpointsCarryNoEposVersion(t *testing.T) {
	h := startServer(t, nil)

	resp, err := h.Client.Get(h.BaseURL + serve.LivezPath)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Empty(t, resp.Header.Get(eposVersionHeader),
		"an operational endpoint is not an OCI Distribution API response")
}

// The read surface is untouched by the adoption: goga/serve's port is
// http.Handler, so newHandler is handed over as it stands and every OCI path
// still routes exactly as its own unit tests say it does. What this adds is
// that the routing survives the wrapper — an operational path is dispatched
// away from the application, and every other path still reaches it.
func TestApplicationRoutingSurvivesTheOpsMux(t *testing.T) {
	ctrl := gomock.NewController(t)
	h := servetest.Start(t.Context(), t,
		newHandler(Version, NewMockrelayer(ctrl), nil),
		serverOptions(serveConfig(), nil)...)

	status, _ := h.Get("/v2/")
	assert.Equal(t, http.StatusOK, status, "the API version check still answers")

	status, _ = h.Get("/nope")
	assert.Equal(t, http.StatusNotFound, status,
		"a path that is neither operational nor OCI still reaches epos's 404")
}

// --ops-addr is the escape hatch for a deployment whose registry port is
// public: the probes and the metrics move to a listener of their own and stop
// answering on the API port entirely.
func TestOpsAddrMovesTheEndpointsOffTheAPIPort(t *testing.T) {
	cfg := serveConfig()
	cfg.opsAddr = opsAddr(t)

	ctrl := gomock.NewController(t)
	h := servetest.Start(t.Context(), t,
		newHandler(Version, NewMockrelayer(ctrl), nil), serverOptions(cfg, nil)...)

	status, _ := h.Get(serve.LivezPath)
	assert.Equal(t, http.StatusNotFound, status,
		"the API port answers /livez with epos's own 404 once the probes have moved")

	resp, err := h.Client.Get("http://" + cfg.opsAddr + serve.LivezPath)
	require.NoError(t, err, "the operational listener answers on its own address")
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// opsAddr reserves a loopback port and hands it straight back, which is how a
// second listener gets an address a test already knows. goga's listener
// deliberately hands out no address of its own.
func opsAddr(t *testing.T) string {
	t.Helper()
	l := httptest.NewUnstartedServer(nil)
	addr := l.Listener.Addr().String()
	require.NoError(t, l.Listener.Close())
	return addr
}
