package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBaseEndpointReturnsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler("1.2.3").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("GET /v2/ status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// SPEC.md 4.3: the header is set on all responses, not just successful ones.
func TestEposVersionHeaderOnEveryResponse(t *testing.T) {
	for _, path := range []string{"/v2/", "/v2/some/repo/manifests/latest", "/nope"} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			newHandler("1.2.3").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			if got := rec.Header().Get(eposVersionHeader); got != "1.2.3" {
				t.Errorf("%s header = %q, want %q", eposVersionHeader, got, "1.2.3")
			}
		})
	}
}

func TestUnimplementedPathsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	newHandler("1.2.3").ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v2/some/repo/tags/list", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("unimplemented path status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
