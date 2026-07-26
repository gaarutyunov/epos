//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// redirectingRegistryImage is the second upstream the blob scenarios need.
//
// SPEC.md 4.2 has epos-registry relay an upstream 307, but zot answers every
// blob GET with the bytes and never redirects, so the redirect posture cannot
// be exercised against it. distribution is the OCI reference implementation and
// is as real a registry as zot: it is configured here with its own `redirect`
// storage middleware, which makes it issue a genuine 307 for blob GET. Nothing
// is faked — the registry serves the real API over the wire, and the 307 it
// emits is the one a registry fronting an object store emits.
const redirectingRegistryImage = "registry:3.1.1"

// startRedirectingUpstream swaps the scenario's upstream for a registry that
// redirects blob requests, re-pushes what was already published, and restarts
// epos-registry in front of it.
//
// The redirect points at a target this test owns so the request that reaches it
// can be inspected — an object store would be equally real but would not let a
// test see whether a credential arrived.
func (w *world) startRedirectingUpstream(ctx context.Context) error {
	if w.upstreamURL == "" {
		return fmt.Errorf("upstream is not running")
	}

	w.blobTarget = httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		w.targetMu.Lock()
		w.targetRequests = append(w.targetRequests, r.Header.Clone())
		w.targetMu.Unlock()
	}))
	godogT.Cleanup(w.blobTarget.Close)

	// `middleware` is a top-level key: nesting it under `storage` makes
	// distribution read it as a second storage backend and refuse to start.
	config := fmt.Sprintf(`version: 0.1
log:
  level: warn
storage:
  filesystem:
    rootdirectory: /var/lib/registry
middleware:
  storage:
    - name: redirect
      options:
        baseurl: %s/
http:
  addr: :5000
`, w.blobTarget.URL)

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        redirectingRegistryImage,
			ExposedPorts: []string{"5000/tcp"},
			Files: []testcontainers.ContainerFile{{
				Reader:            strings.NewReader(config),
				ContainerFilePath: "/config.yml",
				FileMode:          0o644,
			}},
			Cmd:        []string{"/config.yml"},
			WaitingFor: wait.ForHTTP("/v2/").WithPort("5000/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		return fmt.Errorf("start redirecting registry: %w", err)
	}
	w.track(c)

	endpoint, err := c.PortEndpoint(ctx, "5000/tcp", "http")
	if err != nil {
		return fmt.Errorf("redirecting registry endpoint: %w", err)
	}

	// Re-publish everything the scenario put on the previous upstream, so the
	// steps that follow find the same skills where they now look.
	republish := make([]string, 0, len(w.pushed))
	for ref := range w.pushed {
		republish = append(republish, ref)
	}
	w.upstreamURL = endpoint
	for _, ref := range republish {
		name, version, ok := strings.Cut(ref, ":")
		if !ok {
			return fmt.Errorf("reference %q is not <name>:<version>", ref)
		}
		if err := w.pushSkill(ctx, name, version); err != nil {
			return fmt.Errorf("re-push %s to the redirecting upstream: %w", ref, err)
		}
	}

	return w.startRegistry(ctx)
}

// upstreamServesBlobsDirectly is the degraded case of SPEC.md 4.2. zot — the
// upstream the Background starts — answers blob GET with the bytes and never
// redirects, so this states the precondition rather than arranging it.
func (w *world) upstreamServesBlobsDirectly() error {
	if w.upstreamURL == "" {
		return fmt.Errorf("upstream is not running")
	}
	return nil
}

// fetchContentBlob fetches a skill's content layer through epos-registry
// without following a redirect, so the scenario observes what epos-registry
// itself answered.
func (w *world) fetchContentBlob(ref string) error {
	return w.blobRequest(ref, nil, false)
}

// fetchContentBlobWithAuthorization performs a credentialed pull all the way
// through, following the redirect, so the request that lands on the redirect
// target can be inspected.
func (w *world) fetchContentBlobWithAuthorization(ref string) error {
	return w.blobRequest(ref, map[string]string{"Authorization": "Basic ZGVtbzpzM2NyZXQ="}, true)
}

func (w *world) blobRequest(ref string, headers map[string]string, followRedirects bool) error {
	if w.registryURL == "" {
		return fmt.Errorf("epos-registry is not running")
	}

	name, _, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("reference %q is not <name>:<version>", ref)
	}
	layer, ok := w.layers[ref]
	if !ok {
		return fmt.Errorf("no content layer recorded for %s", ref)
	}

	req, err := http.NewRequest(http.MethodGet, w.registryURL+"/v2/"+name+"/blobs/"+layer.digest, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{}
	if followRedirects {
		// A conformant OCI client presents its registry credential to the
		// registry and to nothing else, so it drops the header when the
		// redirect takes it elsewhere. net/http would normally do this, but it
		// compares hostnames only: epos-registry and the redirect target are
		// both on 127.0.0.1 here and differ by port alone, so it keeps the
		// header and the client has to drop it itself.
		//
		// That leaves epos-registry as the only thing that could still put a
		// credential on the target: it would have to have chased the redirect
		// and relayed the client's headers along with it.
		client.CheckRedirect = func(req *http.Request, _ []*http.Request) error {
			req.Header.Del("Authorization")
			return nil
		}
	} else {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	w.resp = resp
	w.respBody = body
	w.lastBlobRef = ref
	return nil
}

// noBlobBytesPassedThrough checks the redirect really moved the transfer off
// epos-registry: the client got a Location on another host and no body.
func (w *world) noBlobBytesPassedThrough() error {
	if w.resp == nil {
		return fmt.Errorf("no response recorded")
	}
	if len(w.respBody) != 0 {
		return fmt.Errorf("epos-registry returned %d body bytes on the redirect path, want 0",
			len(w.respBody))
	}

	location := w.resp.Header.Get("Location")
	if location == "" {
		return fmt.Errorf("redirect carries no Location header")
	}
	target, err := url.Parse(location)
	if err != nil {
		return fmt.Errorf("parse Location %q: %w", location, err)
	}
	registry, err := url.Parse(w.registryURL)
	if err != nil {
		return fmt.Errorf("parse registry url %q: %w", w.registryURL, err)
	}
	if target.Host == registry.Host {
		return fmt.Errorf("Location %q points back at epos-registry, so the bytes would cross it",
			location)
	}
	return nil
}

func (w *world) blobContentUnchanged() error {
	layer, ok := w.layers[w.lastBlobRef]
	if !ok {
		return fmt.Errorf("no content layer recorded for %q", w.lastBlobRef)
	}
	if !bytes.Equal(w.respBody, layer.bytes) {
		return fmt.Errorf("blob body = %q, want %q", w.respBody, layer.bytes)
	}
	return nil
}

// redirectTargetSawNoHeader asserts SPEC.md 4.2's security-critical rule. An
// object store accepts exactly one authentication mechanism and rejects a
// request carrying both a presigned URL and an Authorization header, so a
// credential reaching the target both leaks it to a third party and breaks the
// pull. epos-registry never contacts the target at all; had it followed the
// redirect it would have relayed the client's headers there, and that request
// would show up here.
func (w *world) redirectTargetSawNoHeader(name string) error {
	if w.blobTarget == nil {
		return fmt.Errorf("no redirect target is running")
	}

	w.targetMu.Lock()
	got := append([]http.Header(nil), w.targetRequests...)
	w.targetMu.Unlock()

	if len(got) == 0 {
		return fmt.Errorf("the redirect target received no request at all, so nothing was proven")
	}
	for i, h := range got {
		if v := h.Get(name); v != "" {
			return fmt.Errorf("request %d to the redirect target carried %s: %q", i+1, name, v)
		}
	}
	return nil
}
