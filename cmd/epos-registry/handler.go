package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gaarutyunov/epos/internal/artifact"
	"github.com/gaarutyunov/epos/internal/metrics"
	"github.com/gaarutyunov/epos/internal/upstream"
)

// eposVersionHeader is set on every response so a client can tell epos-registry
// from a plain registry without probing (SPEC.md 4.3).
const eposVersionHeader = "Epos-Version"

// eposDownloadHeader marks a download verified (SPEC.md 5.2). Defined once in
// internal/artifact so the CLI that sends it and the registry that reads it
// cannot drift apart.
const eposDownloadHeader = artifact.DownloadHeader

//go:generate go tool mockgen -source=handler.go -destination=mocks_test.go -package=main

// relayer performs an upstream request and copies the response.
type relayer interface {
	Relay(w http.ResponseWriter, r *http.Request) error
	// Target is the absolute upstream URL a request would go to, for the
	// write path, which redirects rather than relays (SPEC.md 4.5).
	Target(r *http.Request) string
}

// downloadRecorder counts a content blob fetch (5.1) and an accepted manifest
// PUT (5.4).
type downloadRecorder interface {
	Record(ctx context.Context, dl metrics.Download)
	RecordPublish(ctx context.Context, p metrics.Publish)
}

type handler struct {
	up        relayer
	downloads downloadRecorder
}

// newHandler returns the registry handler.
//
// The read surface of SPEC.md 4.1 is served here, including blob GET with the
// 4.2 transfer posture. _catalog (7) and the write path (4.5) are separate
// milestones. Content Management (DELETE) is never implemented.
//
// epos-registry holds no state (4.4): every request is relayed, nothing is
// cached, and two replicas behave identically. Counting does not change that —
// downloads go to an OTel counter, never to a store the replicas share.
func newHandler(version string, up relayer, downloads downloadRecorder) http.Handler {
	h := &handler{up: up, downloads: downloads}
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
		// 4.5: PUT is relayed rather than redirected. It always lands on the
		// configured host, it is the actual publish event, and manifests are
		// kilobytes -- so the body is read in flight for 5.4 with no extra
		// fetch.
		if r.Method != http.MethodGet && r.Method != http.MethodHead &&
			r.Method != http.MethodPut {
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
	case kindUploads:
		// 4.5: the upload session is redirected, not relayed. A 307 preserves
		// method and body, so the client re-issues against upstream and gets
		// upstream's Location natively.
		//
		// This sidesteps a real hazard rather than solving it: the spec lets
		// Location be absolute or relative at upstream's discretion, and the
		// two route oppositely through a fronting registry -- a relative one
		// relayed verbatim resolves against epos-registry and pulls every
		// upload byte through it. Redirecting the session means no Location
		// rewriting, no session mapping and no chunked-resume accounting.
		// Cross-repository mounts (?mount=&from=) go the same way.
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if h.up == nil {
			http.Error(w, "no upstream configured", http.StatusBadGateway)
			return
		}
		http.Redirect(w, r, h.up.Target(r), http.StatusTemporaryRedirect)
		return
	default:
		http.NotFound(w, r)
		return
	}

	if h.up == nil {
		http.Error(w, "no upstream configured", http.StatusBadGateway)
		return
	}

	// A manifest PUT is relayed with its body read in flight, so 5.4 can name
	// the artifact type without a second fetch. 4.5 makes this affordable:
	// manifests are kilobytes, which is also why the upload session is
	// redirected instead.
	var body bytes.Buffer
	if ref.kind == kindManifests && r.Method == http.MethodPut && r.Body != nil {
		r.Body = io.NopCloser(io.TeeReader(r.Body, &body))
	}

	// The status actually answered decides whether this was a download, so the
	// writer is wrapped to observe it (5.1).
	rec := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	if err := h.up.Relay(rec, r); err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}

	if ref.kind == kindBlobs {
		h.countDownload(r, rec.status, ref.name)
	}
	if ref.kind == kindManifests && r.Method == http.MethodPut {
		h.countPublish(r, rec.status, ref, body.Bytes())
	}
}

// countPublish records a manifest PUT upstream accepted (SPEC.md 5.4).
//
// 201 only: a publish is what upstream *accepted*, so a rejected PUT is not
// one. artifact_type comes from the body read in flight, which is why the
// manifest is relayed rather than redirected.
func (h *handler) countPublish(r *http.Request, status int, ref ociRef, body []byte) {
	if h.downloads == nil || status != http.StatusCreated {
		return
	}

	kind := "tag"
	if strings.Contains(ref.ref, ":") {
		kind = "digest"
	}

	var manifest struct {
		ArtifactType string `json:"artifactType"`
		Config       struct {
			MediaType string `json:"mediaType"`
		} `json:"config"`
	}
	_ = json.Unmarshal(body, &manifest)
	artifactType := manifest.ArtifactType
	if artifactType == "" {
		// An image manifest with no artifactType identifies itself by its
		// config media type, which is what the OCI spec says to fall back to.
		artifactType = manifest.Config.MediaType
	}

	h.downloads.RecordPublish(r.Context(), metrics.Publish{
		Repository:    ref.name,
		ArtifactType:  artifactType,
		ReferenceKind: kind,
	})
}

// countDownload records a content blob fetch (SPEC.md 5.1).
//
// Only a GET counts, and only when epos-registry answered it 307 or 200 — the
// two outcomes of the 4.2 transfer posture. A manifest GET or HEAD never
// reaches here: those are resolves, and the lock-file update check does one per
// dependency with no content fetch, which would otherwise dominate the numbers.
// A blob HEAD is a resolve for the same reason.
//
// The repository name identifies the skill; 5.1 is explicit that no manifest
// parsing is required.
func (h *handler) countDownload(r *http.Request, status int, repository string) {
	if h.downloads == nil || r.Method != http.MethodGet {
		return
	}
	if status != http.StatusOK && status != http.StatusTemporaryRedirect {
		return
	}

	version, verified := parseEposDownload(r.Header.Get(eposDownloadHeader))
	h.downloads.Record(r.Context(), metrics.Download{
		Repository: repository,
		Verified:   verified,
		Client:     r.Header.Get("User-Agent"),
		Version:    version,
	})
}

// parseEposDownload reads an Epos-Download header value (SPEC.md 5.2).
//
// The header is "<skill>@<version>". A value that is absent or malformed leaves
// the download unverified rather than failing the request: the header is a
// reporting hint from the client, not part of the OCI contract, and a bad one
// must not break a pull.
func parseEposDownload(value string) (version string, verified bool) {
	skill, version, found := strings.Cut(value, "@")
	if !found || skill == "" || version == "" {
		return "", false
	}
	return version, true
}

// statusWriter remembers the status code written through it.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// Flush keeps the wrapper transparent to a streamed blob response.
func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ReadFrom keeps net/http's zero-copy path available, so wrapping the writer
// does not start buffering blob bytes (SPEC.md 4.2).
func (s *statusWriter) ReadFrom(src io.Reader) (int64, error) {
	if rf, ok := s.ResponseWriter.(io.ReaderFrom); ok {
		return rf.ReadFrom(src)
	}
	return io.Copy(s.ResponseWriter, src)
}

// endpoint kinds of the read surface.
const (
	kindManifests = "manifests"
	kindTagsList  = "tags/list"
	kindReferrers = "referrers"
	kindBlobs     = "blobs"
	kindUploads   = "uploads"
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

	// The upload session: /v2/<name>/blobs/uploads/ , with or without the
	// trailing slash, and with any ?mount=&from= query already stripped by the
	// caller.
	for _, suffix := range []string{"/blobs/uploads/", "/blobs/uploads"} {
		if name, found := strings.CutSuffix(p, suffix); found && name != "" {
			return ociRef{name: name, kind: kindUploads}, true
		}
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
