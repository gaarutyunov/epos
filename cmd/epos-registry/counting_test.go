package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/gaarutyunov/epos/internal/metrics"
)

// relayAnswering builds a relayer mock that answers with a fixed status,
// standing in for the two outcomes of the SPEC.md 4.2 transfer posture.
func relayAnswering(ctrl *gomock.Controller, status int) *Mockrelayer {
	relay := NewMockrelayer(ctrl)
	relay.EXPECT().
		Relay(gomock.Any(), gomock.Any()).
		DoAndReturn(func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(status)
			return nil
		}).
		AnyTimes()
	return relay
}

// SPEC.md 5.1: a content blob GET answered 307 or 200 is a download; a manifest
// GET or HEAD is a resolve and is never counted.
func TestWhatCountsAsADownload(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		status int
		want   *metrics.Download // nil means nothing may be counted
	}{
		{"blob GET redirected", http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", http.StatusTemporaryRedirect,
			&metrics.Download{Repository: "demo/hello"}},
		{"blob GET streamed", http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", http.StatusOK,
			&metrics.Download{Repository: "demo/hello"}},
		{"deep repo name", http.MethodGet, "/v2/a/b/c/blobs/sha256:abc", http.StatusOK,
			&metrics.Download{Repository: "a/b/c"}},

		// Resolves, not downloads. The lock-file update check does one per
		// dependency with no content fetch and would dominate the numbers.
		{"manifest GET", http.MethodGet, "/v2/demo/hello/manifests/1.0.0", http.StatusOK, nil},
		{"manifest HEAD", http.MethodHead, "/v2/demo/hello/manifests/1.0.0", http.StatusOK, nil},
		{"blob HEAD", http.MethodHead, "/v2/demo/hello/blobs/sha256:abc", http.StatusOK, nil},
		{"tags list", http.MethodGet, "/v2/demo/hello/tags/list", http.StatusOK, nil},
		{"referrers", http.MethodGet, "/v2/demo/hello/referrers/sha256:abc", http.StatusOK, nil},

		// Nothing was served, so nothing was downloaded.
		{"blob not found upstream", http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", http.StatusNotFound, nil},
		{"blob unauthorized", http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", http.StatusUnauthorized, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			downloads := NewMockdownloadRecorder(ctrl)
			if tt.want != nil {
				downloads.EXPECT().Record(gomock.Any(), *tt.want).Times(1)
			} else {
				downloads.EXPECT().Record(gomock.Any(), gomock.Any()).Times(0)
			}

			h := newHandler("1.2.3", relayAnswering(ctrl, tt.status), downloads)
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, tt.path, nil))
		})
	}
}

// SPEC.md 5.2: the header marks a download verified. Stock oras does not send
// it, so the default is unverified.
func TestEposDownloadHeaderMarksVerified(t *testing.T) {
	tests := []struct {
		name        string
		header      string
		wantVerify  bool
		wantVersion string
	}{
		{"well formed", "demo/hello@1.0.0", true, "1.0.0"},
		{"absent", "", false, ""},
		{"no version", "demo/hello", false, ""},
		{"empty version", "demo/hello@", false, ""},
		{"empty skill", "@1.0.0", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			downloads := NewMockdownloadRecorder(ctrl)
			downloads.EXPECT().Record(gomock.Any(), metrics.Download{
				Repository: "demo/hello",
				Verified:   tt.wantVerify,
				Client:     "oras-go",
				Version:    tt.wantVersion,
			}).Times(1)

			h := newHandler("1.2.3", relayAnswering(ctrl, http.StatusOK), downloads)

			req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
			if tt.header != "" {
				req.Header.Set(eposDownloadHeader, tt.header)
			}
			req.Header.Set("User-Agent", "oras-go")
			h.ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}

// A malformed header is a reporting hint gone wrong, not a protocol violation:
// the pull must still succeed.
func TestMalformedEposDownloadStillServesTheBlob(t *testing.T) {
	ctrl := gomock.NewController(t)
	downloads := NewMockdownloadRecorder(ctrl)
	downloads.EXPECT().Record(gomock.Any(), gomock.Any()).AnyTimes()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
	req.Header.Set(eposDownloadHeader, "nonsense")

	newHandler("1.2.3", relayAnswering(ctrl, http.StatusOK), downloads).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// A registry built without a counter must still serve.
func TestCountingIsOptional(t *testing.T) {
	ctrl := gomock.NewController(t)

	rec := httptest.NewRecorder()
	newHandler("1.2.3", relayAnswering(ctrl, http.StatusOK), nil).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil))

	assert.Equal(t, http.StatusOK, rec.Code)
}
