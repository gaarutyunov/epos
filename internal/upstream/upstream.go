// Package upstream relays OCI Distribution API requests to the configured
// upstream registry (SPEC.md 4).
//
// epos-registry holds no durable state (SPEC.md 4.4): no manifest cache, no
// digest-to-role table, no shared store between replicas. Nothing in this
// package may introduce one.
package upstream

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client relays requests to a single upstream registry.
type Client struct {
	base *url.URL
	http *http.Client
}

// New returns a Client relaying to baseURL.
//
// Redirects are never followed. Upstream 3xx responses are handed back to the
// caller so they can be relayed to the client, which is what keeps blob bytes
// from crossing epos-registry (SPEC.md 4.2).
func New(baseURL string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse upstream url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("upstream url %q needs a scheme and host", baseURL)
	}

	return &Client{
		base: u,
		http: &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// hopByHop headers are connection-scoped and must not be relayed.
var hopByHop = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// Do issues r's method and path against the upstream and returns the response.
//
// The caller owns closing the body.
func (c *Client) Do(r *http.Request) (*http.Response, error) {
	target := *c.base
	target.Path = strings.TrimSuffix(c.base.Path, "/") + r.URL.Path
	target.RawQuery = r.URL.RawQuery

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), nil)
	if err != nil {
		return nil, err
	}

	copyHeader(req.Header, r.Header)
	for _, h := range hopByHop {
		req.Header.Del(h)
	}
	// Host is carried by the URL; relaying the client's would confuse upstream.
	req.Header.Del("Host")

	return c.http.Do(req)
}

// Relay performs r against the upstream and copies the response to w.
//
// Nothing is cached or recorded: the response is streamed straight through.
func (c *Client) Relay(w http.ResponseWriter, r *http.Request) error {
	resp, err := c.Do(r)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	dst := w.Header()
	copyHeader(dst, resp.Header)
	for _, h := range hopByHop {
		dst.Del(h)
	}

	w.WriteHeader(resp.StatusCode)

	// A HEAD response has no body; io.Copy is a no-op but harmless.
	if _, err := io.Copy(w, resp.Body); err != nil {
		return fmt.Errorf("relay body: %w", err)
	}
	return nil
}

func copyHeader(dst, src http.Header) {
	for k, vs := range src {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}
