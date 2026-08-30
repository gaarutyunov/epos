// Package upstream relays OCI Distribution API requests to the configured
// upstream registry (SPEC.md 4).
//
// epos-registry holds no durable state (SPEC.md 4.4): no manifest cache, no
// digest-to-role table, no shared store between replicas. Nothing in this
// package may introduce one.
package upstream

import (
	"context"
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
		http: &http.Client{CheckRedirect: neverFollow},
	}, nil
}

// neverFollow stops the client short of an upstream redirect, handing the 3xx
// back so Relay can pass it to the caller's client.
//
// This is the whole of SPEC.md 4.2's "must not forward the client's
// Authorization header to a redirect target". Do issues the upstream request
// with the client's headers copied verbatim, so following a redirect here would
// present that Authorization to whatever host upstream nominated — typically an
// object store, which accepts exactly one authentication mechanism and rejects a
// request carrying both a presigned URL and an Authorization header. The
// credential would leak to a third party and every redirected pull would 400.
//
// Not following also keeps blob bytes off epos-registry entirely: the client
// fetches them from the redirect target itself.
func neverFollow(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}

// versionCheckPath is the OCI Distribution API version check, the one endpoint
// every conformant registry serves (SPEC.md 4.1). It is what Ping probes.
const versionCheckPath = "/v2/"

// Ping reports whether the upstream registry is answering.
//
// It backs epos-registry's readiness probe. A relay that holds no state
// (SPEC.md 4.4) can serve nothing while its upstream is unreachable, and an
// instance in that condition should leave the load balancer's rotation without
// being restarted — which is what a failing readiness check does and a failing
// liveness check does not.
//
// Any status below 500 counts as answering, including 401: a registry that
// requires authentication rejects an unauthenticated version check and is
// nonetheless up, and epos-registry carries no credentials of its own to
// satisfy it with. What is being probed is reachability, not authorisation.
func (c *Client) Ping(ctx context.Context) error {
	target := *c.base
	target.Path = strings.TrimSuffix(c.base.Path, "/") + versionCheckPath
	target.RawQuery = ""

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("upstream %s: %w", target.Host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Drained so the connection returns to the pool: a probe runs on a timer
	// and would otherwise open a fresh one every time.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= http.StatusInternalServerError {
		return fmt.Errorf("upstream %s answered %s to %s",
			target.Host, resp.Status, versionCheckPath)
	}
	return nil
}

// Target is the absolute upstream URL for a request's path and query.
//
// The write path redirects the client here rather than relaying (SPEC.md 4.5),
// so it needs the URL as a string rather than a response.
func (c *Client) Target(r *http.Request) string {
	target := *c.base
	target.Path = strings.TrimSuffix(c.base.Path, "/") + r.URL.Path
	target.RawQuery = r.URL.RawQuery
	return target.String()
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

	// The body is forwarded, not dropped: the write path relays a manifest PUT
	// (SPEC.md 4.5), and a nil body would send upstream an empty manifest.
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = r.ContentLength

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
// Nothing is cached or recorded: the response is streamed straight through, so
// a blob is never buffered. An upstream 3xx arrives here unfollowed (see
// neverFollow) and is relayed with its Location untouched — SPEC.md 4.5 is
// explicit that epos-registry does no Location rewriting.
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
