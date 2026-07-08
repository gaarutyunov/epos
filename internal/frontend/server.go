package frontend

import (
	"encoding/json"
	"net/http"
)

// Server serves the single-page federated listing and its JSON API (SPEC §12.3).
type Server struct {
	catalog *Catalog
}

// NewServer wraps a catalog in an HTTP server.
func NewServer(catalog *Catalog) *Server { return &Server{catalog: catalog} }

// Handler returns the HTTP handler: GET /api/skills?keyword=&registry= returns
// the filtered listing as JSON; GET / serves the SPA shell.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/skills", func(w http.ResponseWriter, r *http.Request) {
		f := Filter{Keyword: r.URL.Query().Get("keyword"), Registry: r.URL.Query().Get("registry")}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.catalog.Filter(f))
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
