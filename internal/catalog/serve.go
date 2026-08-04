package catalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gaarutyunov/epos/internal/registry"
)

// documentTimeout bounds one detail page's content-layer fetch. A slow registry
// costs that page, never the process.
const documentTimeout = 30 * time.Second

// Server serves the catalog on the listener that already answers /v2/.
type Server struct {
	renderer *Renderer
	client   registry.Client
	stats    Stats
	assets   map[string][]byte

	// documents caches rendered SKILL.md keyed by manifest digest.
	//
	// The key is immutable, so there is no invalidation path to get wrong: a
	// digest names bytes. This is the *content* cache and it is unrelated to
	// the counts TTL — the index and the document are fixed for a process, the
	// numbers are not (D3b, D4e).
	mu        sync.Mutex
	documents map[string]Skill
}

// NewServer builds the handler for a catalog.
func NewServer(renderer *Renderer, client registry.Client, stats Stats) (*Server, error) {
	assets, err := Assets()
	if err != nil {
		return nil, err
	}
	return &Server{
		renderer:  renderer,
		client:    client,
		stats:     stats,
		assets:    assets,
		documents: map[string]Skill{},
	}, nil
}

// ErrReservedBasePath refuses a base path that would shadow the distribution
// API.
//
// Refused at startup rather than discovered in production: /v2/ is matched
// before any catalog route, and an operator who mounts the catalog on top of it
// has configured a registry that cannot serve. Saying so while the process is
// still starting is the difference between a config error and an outage.
var ErrReservedBasePath = errors.New("/v2/ is the distribution API and cannot be a catalog base path")

// CheckBasePath rejects a base path under /v2/.
func CheckBasePath(basePath string) error {
	p := normaliseBasePath(basePath)
	if p == "/v2/" || strings.HasPrefix(p, "/v2/") {
		return fmt.Errorf("%w: --catalog.base-path %s", ErrReservedBasePath, basePath)
	}
	return nil
}

// Handler wraps the registry's handler, answering catalog paths and passing
// everything else through.
//
// The relay is matched first and is never answered from anything the catalog
// holds. That ordering is a requirement rather than an accident: a catalog
// mounted at / must still hand /v2/ to the registry, and nothing the catalog
// caches may make a distribution API request slower or different. Nothing here
// is written to disk either — the index is in memory, rebuilt at startup and
// shared with nothing, which is what keeps SPEC.md 4.4's amendment narrow.
func (s *Server) Handler(relay http.Handler) http.Handler {
	base := s.renderer.BasePath()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First, always, unconditionally.
		if strings.HasPrefix(r.URL.Path, "/v2/") || r.URL.Path == "/v2" {
			relay.ServeHTTP(w, r)
			return
		}
		if !strings.HasPrefix(r.URL.Path, base) {
			relay.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.serve(w, r, strings.TrimPrefix(r.URL.Path, base))
	})
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request, rest string) {
	if body, ok := s.assets[strings.TrimPrefix(rest, "/")]; ok {
		w.Header().Set("Content-Type", contentTypeOf(rest))
		w.Header().Set("Cache-Control", "public, max-age=300")
		_, _ = w.Write(body)
		return
	}

	route, ok := s.route(rest)
	if !ok {
		http.NotFound(w, r)
		return
	}

	var document *Skill
	if route.Kind == PageDetail {
		document = s.document(r.Context(), route.Repository)
	}

	// Counts are read per request, behind the TTL cache and a timeout. That is
	// what makes "pull the skill, reload, the number moved" true; fixing the
	// index at startup says nothing about the numbers.
	counts := StatsOrNil(r.Context(), s.stats, func(err error) {
		log.Printf("epos-registry: the catalog could not read its counts: %v", err)
	})

	var page bytes.Buffer
	if err := s.renderer.Render(&page, route, counts, document); err != nil {
		log.Printf("epos-registry: the catalog could not render %s: %v", r.URL.Path, err)
		http.Error(w, "the page could not be rendered", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page.Bytes())
}

// route resolves a path against the index built at startup.
//
// Anything else is a 404 with no registry request (D3b). Without this a URL
// path is an instruction to fetch an arbitrary repository from the configured
// registry — an unauthenticated proxy wearing a catalog, and now one bolted to
// a registry, so the stakes are higher than they were.
func (s *Server) route(rest string) (Route, bool) {
	rest = strings.Trim(rest, "/")
	switch rest {
	case "":
		return Route{Kind: PageHome, Path: ""}, true
	case "catalog":
		return Route{Kind: PageList, Path: "catalog/"}, true
	case "tools":
		return Route{Kind: PageTools, Path: "tools/"}, true
	}

	repository, ok := strings.CutPrefix(rest, "skills/")
	if !ok {
		return Route{}, false
	}
	if _, found := s.renderer.Catalog().Lookup(repository); !found {
		return Route{}, false
	}
	return Route{Kind: PageDetail, Path: "skills/" + repository + "/", Repository: repository}, true
}

// document fetches and renders a skill's SKILL.md, once per digest.
//
// Returned rather than written back into the index, so nothing shared is
// mutated on the request path. The result always renders a page, including the
// case where the document could not be read — that is a page saying so, not a
// failure (D3d).
func (s *Server) document(ctx context.Context, repository string) *Skill {
	skill, ok := s.renderer.Catalog().Lookup(repository)
	if !ok {
		return nil
	}

	s.mu.Lock()
	cached, hit := s.documents[skill.Digest]
	s.mu.Unlock()
	if hit {
		return &cached
	}

	fetchCtx, cancel := context.WithTimeout(ctx, documentTimeout)
	defer cancel()
	loaded := LoadDocument(fetchCtx, s.client, skill)

	s.mu.Lock()
	// Bounded by construction: the index is fixed at startup, so the cache
	// holds at most one entry per skill and cannot grow past it.
	s.documents[skill.Digest] = loaded
	s.mu.Unlock()
	return &loaded
}

func contentTypeOf(name string) string {
	switch {
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
