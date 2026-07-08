package frontend

import (
	"encoding/json"
	"net/http"
	"sync"
)

// Server serves the single-page federated listing and its JSON API (SPEC §12.3).
// The catalog is swappable so a background refresh loop can replace the in-memory
// index without downtime (SPEC §12.2).
type Server struct {
	mu      sync.RWMutex
	catalog *Catalog
}

// NewServer wraps a catalog in an HTTP server.
func NewServer(catalog *Catalog) *Server { return &Server{catalog: catalog} }

// SetCatalog atomically replaces the served catalog (periodic refresh, §12.2).
func (s *Server) SetCatalog(c *Catalog) {
	s.mu.Lock()
	s.catalog = c
	s.mu.Unlock()
}

func (s *Server) current() *Catalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog
}

// Handler returns the HTTP handler: GET /api/skills?keyword=&registry= returns
// the filtered listing as JSON; GET / serves the SPA shell.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		f := Filter{Keyword: r.URL.Query().Get("keyword"), Registry: r.URL.Query().Get("registry")}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.current().Filter(f))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	})
	return mux
}

const indexHTML = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>Epos — Skill Registry</title></head>
<body>
<h1>Epos</h1>
<input id="q" placeholder="filter skills by keyword">
<ul id="list"></ul>
<script>
async function load() {
  const q = document.getElementById('q').value;
  const r = await fetch('/api/skills?keyword=' + encodeURIComponent(q));
  const skills = await r.json();
  const ul = document.getElementById('list');
  ul.innerHTML = '';
  for (const s of skills) {
    const li = document.createElement('li');
    li.textContent = s.name + ' ' + s.version + ' — ' + s.description + ' (' + s.downloads + ' downloads)';
    ul.appendChild(li);
  }
}
document.getElementById('q').addEventListener('input', load);
load();
</script>
</body>
</html>
`
