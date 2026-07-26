package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestBaseEndpointReturnsOK(t *testing.T) {
	ctrl := gomock.NewController(t)
	rec := httptest.NewRecorder()
	newHandler("1.2.3", NewMockrelayer(ctrl), nil).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/", nil))

	assert.Equal(t, http.StatusOK, rec.Code, "GET /v2/")
}

// SPEC.md 4.3: the header is set on all responses, not just successful ones.
func TestEposVersionHeaderOnEveryResponse(t *testing.T) {
	for _, path := range []string{"/v2/", "/v2/demo/hello/manifests/1.0.0", "/nope"} {
		t.Run(path, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			rec := httptest.NewRecorder()
			newHandler("1.2.3", relayAnswering(ctrl, http.StatusOK), nil).
				ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			assert.Equal(t, "1.2.3", rec.Header().Get(eposVersionHeader), eposVersionHeader)
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

		// The write path has its own tests; these are the methods 4.5 still
		// does not serve.
		{"blob PUT is not served", http.MethodPut, "/v2/demo/hello/blobs/sha256:abc", false, http.StatusMethodNotAllowed},
		{"blob DELETE is never served", http.MethodDelete, "/v2/demo/hello/blobs/sha256:abc", false, http.StatusMethodNotAllowed},
		{"upload session GET is not served", http.MethodGet, "/v2/demo/hello/blobs/uploads/", false, http.StatusMethodNotAllowed},
		{"DELETE is never served", http.MethodDelete, "/v2/demo/hello/manifests/1.0.0", false, http.StatusMethodNotAllowed},
		{"unknown path", http.MethodGet, "/v2/demo/hello/whatever", false, http.StatusNotFound},
		{"outside /v2/", http.MethodGet, "/healthz", false, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			relay := NewMockrelayer(ctrl)
			want := 0
			if tt.wantRelayed {
				want = 1
			}
			relay.EXPECT().
				Relay(gomock.Any(), gomock.Any()).
				DoAndReturn(func(w http.ResponseWriter, _ *http.Request) error {
					w.WriteHeader(http.StatusOK)
					return nil
				}).
				Times(want)

			downloads := NewMockdownloadRecorder(ctrl)
			downloads.EXPECT().Record(gomock.Any(), gomock.Any()).AnyTimes()

			rec := httptest.NewRecorder()
			newHandler("1.2.3", relay, downloads).
				ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			assert.Equal(t, tt.wantStatus, rec.Code)
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
			require.Equal(t, tt.wantOK, ok)
			if !ok {
				return
			}
			assert.Equal(t, tt.wantName, got.name, "name")
			assert.Equal(t, tt.wantKind, got.kind, "kind")
			assert.Equal(t, tt.wantRef, got.ref, "ref")
		})
	}
}
