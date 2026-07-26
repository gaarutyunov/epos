package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubRelay records what it was asked to relay so routing can be asserted
// without an upstream. The container-backed behaviour lives in the godog suite.
type stubRelay struct {
	calls   int
	lastURL string
}

func (s *stubRelay) Relay(w http.ResponseWriter, r *http.Request) error {
	s.calls++
	s.lastURL = r.URL.Path
	w.WriteHeader(http.StatusOK)
	return nil
}

func TestBaseEndpointReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler("1.2.3", &stubRelay{}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("GET /v2/ status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// SPEC.md 4.3: the header is set on all responses, not just successful ones.
func TestEposVersionHeaderOnEveryResponse(t *testing.T) {
	for _, path := range []string{"/v2/", "/v2/demo/hello/manifests/1.0.0", "/nope"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newHandler("1.2.3", &stubRelay{}, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if got := rec.Header().Get(eposVersionHeader); got != "1.2.3" {
				t.Errorf("%s header = %q, want %q", eposVersionHeader, got, "1.2.3")
			}
		})
	}
}

// The read surface is relayed; everything else is not served yet.
func TestRoutingOfReadSurface(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		path        string
		wantRelayed bool
		wantStatus  int
	}{
		{"manifest by tag", http.MethodGet, "/v2/demo/hello/manifests/1.0.0", true, http.StatusOK},
		{"manifest by HEAD", http.MethodHead, "/v2/demo/hello/manifests/1.0.0", true, http.StatusOK},
		{"manifest by digest", http.MethodGet, "/v2/demo/hello/manifests/sha256:abc", true, http.StatusOK},
		{"tags list", http.MethodGet, "/v2/demo/hello/tags/list", true, http.StatusOK},
		{"referrers", http.MethodGet, "/v2/demo/hello/referrers/sha256:abc", true, http.StatusOK},
		{"single segment repo", http.MethodGet, "/v2/hello/tags/list", true, http.StatusOK},
		{"deep repo name", http.MethodGet, "/v2/a/b/c/manifests/latest", true, http.StatusOK},
		{"blob by digest", http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", true, http.StatusOK},
		{"blob by HEAD", http.MethodHead, "/v2/demo/hello/blobs/sha256:abc", true, http.StatusOK},

		// Not this milestone: the write path is 4.5.
		{"blob upload session is not served", http.MethodPost, "/v2/demo/hello/blobs/uploads/", false, http.StatusNotFound},
		{"blob PUT is not served", http.MethodPut, "/v2/demo/hello/blobs/sha256:abc", false, http.StatusMethodNotAllowed},
		{"blob DELETE is never served", http.MethodDelete, "/v2/demo/hello/blobs/sha256:abc", false, http.StatusMethodNotAllowed},
		{"manifest PUT is not served", http.MethodPut, "/v2/demo/hello/manifests/1.0.0", false, http.StatusMethodNotAllowed},
		{"DELETE is never served", http.MethodDelete, "/v2/demo/hello/manifests/1.0.0", false, http.StatusMethodNotAllowed},
		{"unknown path", http.MethodGet, "/v2/demo/hello/whatever", false, http.StatusNotFound},
		{"outside /v2/", http.MethodGet, "/healthz", false, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &stubRelay{}
			rec := httptest.NewRecorder()
			newHandler("1.2.3", stub, nil).ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if got := stub.calls > 0; got != tt.wantRelayed {
				t.Errorf("relayed = %v, want %v", got, tt.wantRelayed)
			}
			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

// Repository names may contain slashes, so the endpoint marker has to be found
// from the right rather than by counting path segments.
func TestParseRef(t *testing.T) {
	tests := []struct {
		path     string
		wantName string
		wantKind string
		wantRef  string
		wantOK   bool
	}{
		{"/v2/demo/hello/manifests/1.0.0", "demo/hello", kindManifests, "1.0.0", true},
		{"/v2/hello/manifests/1.0.0", "hello", kindManifests, "1.0.0", true},
		{"/v2/a/b/c/d/manifests/latest", "a/b/c/d", kindManifests, "latest", true},
		{"/v2/demo/hello/tags/list", "demo/hello", kindTagsList, "", true},
		{"/v2/demo/hello/referrers/sha256:abc", "demo/hello", kindReferrers, "sha256:abc", true},
		{"/v2/demo/hello/blobs/sha256:abc", "demo/hello", kindBlobs, "sha256:abc", true},

		// A repository legitimately named "manifests" must still split on the
		// rightmost marker.
		{"/v2/manifests/manifests/1.0.0", "manifests", kindManifests, "1.0.0", true},

		{"/v2/", "", "", "", false},
		{"/v2/demo/hello", "", "", "", false},
		{"/v2/manifests/1.0.0", "", "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := parseRef(tt.path)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.name != tt.wantName || got.kind != tt.wantKind || got.ref != tt.wantRef {
				t.Errorf("got {name:%q kind:%q ref:%q}, want {name:%q kind:%q ref:%q}",
					got.name, got.kind, got.ref, tt.wantName, tt.wantKind, tt.wantRef)
			}
		})
	}
}
