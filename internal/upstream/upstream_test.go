package upstream

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// recorder is a stand-in for the host an upstream redirect nominates — an
// object store or CDN. It records what reached it so a test can assert that
// epos-registry never did.
type recorder struct {
	mu   sync.Mutex
	got  []http.Header
	body []byte
}

func (rec *recorder) server(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		rec.got = append(rec.got, r.Header.Clone())
		rec.mu.Unlock()
		_, _ = w.Write(rec.body)
	}))
	t.Cleanup(s.Close)
	return s
}

func (rec *recorder) requests() []http.Header {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]http.Header(nil), rec.got...)
}

// SPEC.md 4.2: the upstream 307 is relayed to the client, and epos-registry
// does not chase it. The credential the client sent must never reach the
// redirect target, and no blob bytes may cross epos-registry.
func TestRelayPassesUpstreamRedirectThroughWithoutFollowingIt(t *testing.T) {
	target := &recorder{body: []byte("blob bytes that must not cross epos-registry")}
	targetSrv := target.server(t)

	// Bodyless, as a real registry answers: http.Redirect would add a courtesy
	// HTML body that no registry sends.
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", targetSrv.URL+"/blob")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(upstreamSrv.Close)

	c, err := New(upstreamSrv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
	req.Header.Set("Authorization", "Basic c2VjcmV0")
	rec := httptest.NewRecorder()

	if err := c.Relay(rec, req); err != nil {
		t.Fatalf("Relay: %v", err)
	}

	if rec.Code != http.StatusTemporaryRedirect {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
	}
	if got, want := rec.Header().Get("Location"), targetSrv.URL+"/blob"; got != want {
		t.Errorf("Location = %q, want %q — the redirect is relayed unrewritten", got, want)
	}
	if n := rec.Body.Len(); n != 0 {
		t.Errorf("relayed %d body bytes, want 0 — blob bytes must not cross epos-registry", n)
	}
	if reqs := target.requests(); len(reqs) != 0 {
		t.Errorf("redirect target received %d request(s) from epos-registry, want 0: %v", len(reqs), reqs)
	}
}

// The degraded case of SPEC.md 4.2: some registries answer blob GET with the
// bytes, and those are streamed through unchanged.
func TestRelayStreamsWhenUpstreamDoesNotRedirect(t *testing.T) {
	want := []byte("SKILL.md for demo/hello 1.0.0")

	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(want)
	}))
	t.Cleanup(upstreamSrv.Close)

	c, err := New(upstreamSrv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	if err := c.Relay(rec, httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)); err != nil {
		t.Fatalf("Relay: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != string(want) {
		t.Errorf("body = %q, want %q", got, want)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}
}

// The configured upstream is not a redirect target: SPEC.md 4.5 relays the
// client's credential to it, and a private registry is unreadable otherwise.
func TestRelayForwardsAuthorizationToTheConfiguredUpstream(t *testing.T) {
	// The upstream echoes what it saw, so the assertion reads it out of the
	// relayed response rather than out of a variable the handler goroutine
	// wrote.
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Header.Get("Authorization")))
	}))
	t.Cleanup(upstreamSrv.Close)

	c, err := New(upstreamSrv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
	req.Header.Set("Authorization", "Basic c2VjcmV0")
	rec := httptest.NewRecorder()
	if err := c.Relay(rec, req); err != nil {
		t.Fatalf("Relay: %v", err)
	}

	if got, want := rec.Body.String(), "Basic c2VjcmV0"; got != want {
		t.Errorf("upstream saw Authorization = %q, want %q", got, want)
	}
}

// Hop-by-hop headers are connection-scoped and must not be relayed in either
// direction.
func TestRelayDropsHopByHopHeaders(t *testing.T) {
	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Proxy-Authenticate", "Basic")
		_, _ = w.Write([]byte(r.Header.Get("Proxy-Authorization")))
	}))
	t.Cleanup(upstreamSrv.Close)

	c, err := New(upstreamSrv.URL)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
	req.Header.Set("Proxy-Authorization", "Basic c2VjcmV0")
	rec := httptest.NewRecorder()
	if err := c.Relay(rec, req); err != nil {
		t.Fatalf("Relay: %v", err)
	}

	if v := rec.Body.String(); v != "" {
		t.Errorf("upstream saw Proxy-Authorization = %q, want it dropped", v)
	}
	if v := rec.Header().Get("Proxy-Authenticate"); v != "" {
		t.Errorf("client saw Proxy-Authenticate = %q, want it dropped", v)
	}
}
