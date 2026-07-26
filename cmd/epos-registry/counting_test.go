package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gaarutyunov/epos/internal/metrics"
)

// recordingCounter captures what the handler decided to count.
type recordingCounter struct {
	got []metrics.Download
}

func (c *recordingCounter) Record(_ context.Context, dl metrics.Download) {
	c.got = append(c.got, dl)
}

// statusRelay answers with a fixed status, standing in for the two outcomes of
// the SPEC.md 4.2 transfer posture.
type statusRelay struct{ status int }

func (s *statusRelay) Relay(w http.ResponseWriter, _ *http.Request) error {
	w.WriteHeader(s.status)
	return nil
}

// SPEC.md 5.1: a content blob GET answered 307 or 200 is a download; a manifest
// GET or HEAD is a resolve and is never counted.
func TestWhatCountsAsADownload(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		status     int
		wantCounts int
		wantRepo   string
	}{
		{"blob GET redirected", http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", http.StatusTemporaryRedirect, 1, "demo/hello"},
		{"blob GET streamed", http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", http.StatusOK, 1, "demo/hello"},
		{"deep repo name", http.MethodGet, "/v2/a/b/c/blobs/sha256:abc", http.StatusOK, 1, "a/b/c"},

		// Resolves, not downloads. The lock-file update check does one per
		// dependency with no content fetch and would dominate the numbers.
		{"manifest GET", http.MethodGet, "/v2/demo/hello/manifests/1.0.0", http.StatusOK, 0, ""},
		{"manifest HEAD", http.MethodHead, "/v2/demo/hello/manifests/1.0.0", http.StatusOK, 0, ""},
		{"blob HEAD", http.MethodHead, "/v2/demo/hello/blobs/sha256:abc", http.StatusOK, 0, ""},
		{"tags list", http.MethodGet, "/v2/demo/hello/tags/list", http.StatusOK, 0, ""},
		{"referrers", http.MethodGet, "/v2/demo/hello/referrers/sha256:abc", http.StatusOK, 0, ""},

		// Nothing was served, so nothing was downloaded.
		{"blob not found upstream", http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", http.StatusNotFound, 0, ""},
		{"blob unauthorized", http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", http.StatusUnauthorized, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter := &recordingCounter{}
			h := newHandler("1.2.3", &statusRelay{status: tt.status}, counter)
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, tt.path, nil))

			if len(counter.got) != tt.wantCounts {
				t.Fatalf("recorded %d download(s), want %d: %+v",
					len(counter.got), tt.wantCounts, counter.got)
			}
			if tt.wantCounts > 0 && counter.got[0].Repository != tt.wantRepo {
				t.Errorf("repository = %q, want %q", counter.got[0].Repository, tt.wantRepo)
			}
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
			counter := &recordingCounter{}
			h := newHandler("1.2.3", &statusRelay{status: http.StatusOK}, counter)

			req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
			if tt.header != "" {
				req.Header.Set(eposDownloadHeader, tt.header)
			}
			req.Header.Set("User-Agent", "oras-go")
			h.ServeHTTP(httptest.NewRecorder(), req)

			if len(counter.got) != 1 {
				t.Fatalf("recorded %d download(s), want 1", len(counter.got))
			}
			got := counter.got[0]
			if got.Verified != tt.wantVerify {
				t.Errorf("verified = %v, want %v", got.Verified, tt.wantVerify)
			}
			if got.Version != tt.wantVersion {
				t.Errorf("version = %q, want %q", got.Version, tt.wantVersion)
			}
			if got.Client != "oras-go" {
				t.Errorf("client = %q, want %q", got.Client, "oras-go")
			}
		})
	}
}

// A malformed header is a reporting hint gone wrong, not a protocol violation:
// the pull must still succeed.
func TestMalformedEposDownloadStillServesTheBlob(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil)
	req.Header.Set(eposDownloadHeader, "nonsense")

	newHandler("1.2.3", &statusRelay{status: http.StatusOK}, &recordingCounter{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// A registry built without a counter must still serve.
func TestCountingIsOptional(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler("1.2.3", &statusRelay{status: http.StatusOK}, nil).ServeHTTP(rec,
		httptest.NewRequest(http.MethodGet, "/v2/demo/hello/blobs/sha256:abc", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
