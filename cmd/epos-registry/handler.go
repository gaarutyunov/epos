package main

import (
	"net/http"
	"strings"

	"github.com/gaarutyunov/epos/internal/upstream"
)

// eposVersionHeader is set on every response so a client can tell epos-registry
// from a plain registry without probing (SPEC.md 4.3).
const eposVersionHeader = "Epos-Version"

// relayer performs an upstream request and copies the response.
type relayer interface {
	Relay(w http.ResponseWriter, r *http.Request) error
}

type handler struct {
	up relayer
}

// newHandler returns the registry handler.
//
// The read surface of SPEC.md 4.1 is served here, including blob GET with the
// 4.2 transfer posture. _catalog (7) and the write path (4.5) are separate
// milestones. Content Management (DELETE) is never implemented.
//
// epos-registry holds no state (4.4): every request is relayed, nothing is
// cached, and two replicas behave identically.
func newHandler(version string, up relayer) http.Handler {
	h := &handler{up: up}
	return withEposVersion(version, http.HandlerFunc(h.route))
}

// withEposVersion sets Epos-Version on all responses, including errors.
func withEposVersion(version string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(eposVersionHeader, version)
		next.ServeHTTP(w, r)
	})
}

func (h *handler) route(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/v2/") {
		http.NotFound(w, r)
		return
	}

	// The API version check.
	if r.URL.Path == "/v2/" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte("{}"))
		}
		return
	}

	ref, ok := parseRef(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	switch ref.kind {
	case kindManifests:
		// PUT is the write path (4.5), a later milestone.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	case kindBlobs:
		// 4.2: the blob is relayed, which for a redirecting upstream means the
		// 307 reaches the client and the bytes never cross epos-registry.
		//
		// HEAD is served alongside GET: 4.1 lists only the GET, but the Pull
		// conformance category it gates on exercises the blob HEAD too, and
		// relaying it is the same code path.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	case kindTagsList, kindReferrers:
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}

	if h.up == nil {
		http.Error(w, "no upstream configured", http.StatusBadGateway)
		return
	}

	if err := h.up.Relay(w, r); err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
}

// endpoint kinds of the read surface.
const (
	kindManifests = "manifests"
	kindTagsList  = "tags/list"
	kindReferrers = "referrers"
	kindBlobs     = "blobs"
)

type ociRef struct {
	name string // repository name; may contain slashes
	kind string
	ref  string // tag, digest, or empty for tags/list
}

// parseRef splits an OCI Distribution path into repository name, endpoint kind
// and reference.
//
// Repository names may contain slashes ("demo/hello"), so the name sits in the
// middle of the path and net/http's wildcards cannot express it. The split is
// done on the last occurrence of the endpoint marker.
func parseRef(path string) (ociRef, bool) {
	p := strings.TrimPrefix(path, "/v2/")
	if p == "" {
		return ociRef{}, false
	}

	if name, found := strings.CutSuffix(p, "/"+kindTagsList); found && name != "" {
		return ociRef{name: name, kind: kindTagsList}, true
	}

	for _, kind := range []string{kindManifests, kindReferrers, kindBlobs} {
		marker := "/" + kind + "/"
		i := strings.LastIndex(p, marker)
		if i <= 0 {
			continue
		}
		ref := p[i+len(marker):]
		if ref == "" || strings.Contains(ref, "/") {
			continue
		}
		return ociRef{name: p[:i], kind: kind, ref: ref}, true
	}

	return ociRef{}, false
}

// compile-time assertion that the upstream client satisfies relayer.
var _ relayer = (*upstream.Client)(nil)
