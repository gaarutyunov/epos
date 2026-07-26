package upstream

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
	req.Header.Set("Authorization", "Basic c2VjcmV0")
	rec := httptest.NewRecorder()

	require.NoError(t, c.Relay(rec, req))

	assert.Equal(t, http.StatusTemporaryRedirect, rec.Code)
	assert.Equal(t, targetSrv.URL+"/blob", rec.Header().Get("Location"),
		"the redirect is relayed unrewritten")
	assert.Zero(t, rec.Body.Len(), "blob bytes must not cross epos-registry")
	assert.Empty(t, target.requests(),
		"epos-registry must not contact the redirect target at all")
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
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	require.NoError(t, c.Relay(rec, httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)))

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, string(want), rec.Body.String())
	assert.Equal(t, "application/octet-stream", rec.Header().Get("Content-Type"))
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
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
	req.Header.Set("Authorization", "Basic c2VjcmV0")
	rec := httptest.NewRecorder()
	require.NoError(t, c.Relay(rec, req))

	assert.Equal(t, "Basic c2VjcmV0", rec.Body.String(),
		"the configured upstream is not a redirect target; 4.5 relays the credential to it")
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
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
	req.Header.Set("Proxy-Authorization", "Basic c2VjcmV0")
	rec := httptest.NewRecorder()
	require.NoError(t, c.Relay(rec, req))

	assert.Empty(t, rec.Body.String(), "Proxy-Authorization must not reach upstream")
	assert.Empty(t, rec.Header().Get("Proxy-Authenticate"), "Proxy-Authenticate must not reach the client")
}
