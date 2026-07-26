package main

import "net/http"

// eposVersionHeader is set on every response so a client can tell epos-registry
// from a plain registry without probing (SPEC.md 4.3).
const eposVersionHeader = "Epos-Version"

// newHandler returns the registry handler.
//
// Only the GET /v2/ handshake is implemented at this stage; every other path
// 404s. epos-registry holds no state of any kind (SPEC.md 4.4).
func newHandler(version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v2/", handleBase)

	return withEposVersion(version, mux)
}

// withEposVersion sets Epos-Version on all responses, including errors.
func withEposVersion(version string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(eposVersionHeader, version)
		next.ServeHTTP(w, r)
	})
}

// handleBase answers the OCI Distribution API version check.
func handleBase(w http.ResponseWriter, r *http.Request) {
	// Anything below /v2/ is not implemented yet.
	if r.URL.Path != "/v2/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{}"))
}
