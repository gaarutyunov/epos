// Package proxy is the transparent, credential pass-through OCI registry proxy
// (SPEC §6). It stores no secrets and relays the client's own credentials to the
// upstream unchanged; the upstream enforces all authorization. It is the
// interception point for download statistics (SPEC §6.4).
package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"

	"github.com/gaarutyunov/epos/internal/stats"
)

// manifestGet matches GET /v2/<name>/manifests/<ref> — the countable pull event.
var manifestPath = regexp.MustCompile(`^/v2/(.+)/manifests/([^/]+)$`)

// Proxy relays /v2/ distribution requests to an upstream registry.
type Proxy struct {
	upstream *url.URL
	counter  *stats.Counter
	rp       *httputil.ReverseProxy

	// persistedCredentials is always zero: Epos stores no secrets (SPEC §6.1).
	// Exposed for assertions in the pass-through test.
	persistedCredentials int
}

// New builds a proxy in front of upstreamURL. counter may be nil.
func New(upstreamURL string, counter *stats.Counter) (*Proxy, error) {
	u, err := url.Parse(upstreamURL)
	if err != nil {
		return nil, err
	}
	p := &Proxy{upstream: u, counter: counter}
	rp := httputil.NewSingleHostReverseProxy(u)
	// Default director already forwards headers (including Authorization) and
	// the upstream's WWW-Authenticate challenge unchanged. We only rewrite Host.
	base := rp.Director
	rp.Director = func(req *http.Request) {
		base(req)
		req.Host = u.Host
	}
	if counter != nil {
		rp.ModifyResponse = func(resp *http.Response) error {
			p.observe(resp)
			return nil
		}
	}
	p.rp = rp
	return p, nil
}

// ServeHTTP relays the request to the upstream, counting manifest GETs.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.rp.ServeHTTP(w, r)
}

// observe counts a manifest GET pull event, excluding HEAD and blob GETs and
// distinguishing index vs image manifests to avoid double counting (SPEC §6.4).
func (p *Proxy) observe(resp *http.Response) {
	req := resp.Request
	if req == nil || p.counter == nil {
		return
	}
	if resp.StatusCode >= 400 {
		p.counter.CountError()
		return
	}
	if req.Method != http.MethodGet {
		return // exclude HEAD freshness checks
	}
	m := manifestPath.FindStringSubmatch(req.URL.Path)
	if m == nil {
		return // exclude blob GETs and tag listings
	}
	ct := resp.Header.Get("Content-Type")
	isIndex := strings.Contains(ct, "index") || strings.Contains(ct, "manifest.list")
	p.counter.CountManifestGet(p.upstream.Host, m[1], isIndex)
}

// PersistedCredentials returns the number of credentials the proxy has stored:
// always zero (SPEC §6.1). The pass-through path relays the client's own
// credentials without persisting them.
func (p *Proxy) PersistedCredentials() int { return p.persistedCredentials }
