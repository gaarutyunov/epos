package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/gaarutyunov/epos/internal/metrics"
)

const skillManifest = `{"schemaVersion":2,` +
	`"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
	`"artifactType":"application/vnd.agentskills.skill.v1",` +
	`"config":{"mediaType":"application/vnd.agentskills.skill.config.v1+json"},` +
	`"layers":[]}`

// SPEC.md 4.5: the upload session is redirected, not relayed, so the client
// re-issues against upstream and gets upstream's Location natively.
func TestUploadSessionIsRedirected(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"upload session", "/v2/demo/hello/blobs/uploads/"},
		{"without the trailing slash", "/v2/demo/hello/blobs/uploads"},
		// 4.5: cross-repository mounts are redirected identically.
		{"cross-repository mount", "/v2/demo/hello/blobs/uploads/?mount=sha256:abc&from=demo/other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			relay := NewMockrelayer(ctrl)
			relay.EXPECT().
				Target(gomock.Any()).
				Return("http://upstream.example/v2/demo/hello/blobs/uploads/").
				Times(1)
			// Redirected, never relayed: no upload byte may cross the proxy.
			relay.EXPECT().Relay(gomock.Any(), gomock.Any()).Times(0)

			rec := httptest.NewRecorder()
			newHandler("1.2.3", relay, nil).
				ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tt.path, nil))

			assert.Equal(t, http.StatusTemporaryRedirect, rec.Code,
				"307 preserves method and body, so the client can re-issue")
			assert.Equal(t, "http://upstream.example/v2/demo/hello/blobs/uploads/",
				rec.Header().Get("Location"))
		})
	}
}

// SPEC.md 5.4: a publish is a manifest PUT upstream accepts with 201.
func TestManifestPutIsRelayedAndCounted(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		status     int
		wantCounts int
		wantKind   string
	}{
		{"by tag", "/v2/demo/hello/manifests/1.0.0", http.StatusCreated, 1, "tag"},
		{"by digest", "/v2/demo/hello/manifests/sha256:abc", http.StatusCreated, 1, "digest"},

		// Not accepted upstream, so not a publish.
		{"rejected", "/v2/demo/hello/manifests/1.0.0", http.StatusBadRequest, 0, ""},
		{"unauthorized", "/v2/demo/hello/manifests/1.0.0", http.StatusUnauthorized, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			relay := NewMockrelayer(ctrl)
			relay.EXPECT().
				Relay(gomock.Any(), gomock.Any()).
				DoAndReturn(func(w http.ResponseWriter, r *http.Request) error {
					// Drain the body the way a real relay does, so the tee
					// downstream sees it.
					_, _ = io.Copy(io.Discard, r.Body)
					w.WriteHeader(tt.status)
					return nil
				}).
				Times(1)

			counter := NewMockdownloadRecorder(ctrl)
			if tt.wantCounts > 0 {
				counter.EXPECT().RecordPublish(gomock.Any(), metrics.Publish{
					Repository:    "demo/hello",
					ArtifactType:  "application/vnd.agentskills.skill.v1",
					ReferenceKind: tt.wantKind,
				}).Times(1)
			} else {
				counter.EXPECT().RecordPublish(gomock.Any(), gomock.Any()).Times(0)
			}
			counter.EXPECT().Record(gomock.Any(), gomock.Any()).AnyTimes()

			req := httptest.NewRequest(http.MethodPut, tt.path, strings.NewReader(skillManifest))
			newHandler("1.2.3", relay, counter).ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}

// The manifest reaches upstream intact: 4.5 relays the PUT, and reading the
// body for 5.4 must not consume it.
func TestManifestPutBodyReachesUpstream(t *testing.T) {
	ctrl := gomock.NewController(t)

	var seen string
	relay := NewMockrelayer(ctrl)
	relay.EXPECT().
		Relay(gomock.Any(), gomock.Any()).
		DoAndReturn(func(w http.ResponseWriter, r *http.Request) error {
			body, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			seen = string(body)
			w.WriteHeader(http.StatusCreated)
			return nil
		}).
		Times(1)

	counter := NewMockdownloadRecorder(ctrl)
	counter.EXPECT().RecordPublish(gomock.Any(), gomock.Any()).Times(1)
	counter.EXPECT().Record(gomock.Any(), gomock.Any()).AnyTimes()

	req := httptest.NewRequest(http.MethodPut, "/v2/demo/hello/manifests/1.0.0",
		strings.NewReader(skillManifest))
	newHandler("1.2.3", relay, counter).ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, skillManifest, seen, "upstream must receive the manifest unchanged")
}
