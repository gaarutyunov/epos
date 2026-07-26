//go:build integration

// Package integration runs the canonical Gherkin features (SPEC.md 13.3)
// against real containers (SPEC.md 13.2). Registries are real: no fakes, no
// in-memory registry substitutes, no mocked HTTP.
package integration

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// zotImage is the upstream registry under test. zot is OCI-conformance
// validated, supports referrers and htpasswd auth (SPEC.md 13.2).
const zotImage = "ghcr.io/project-zot/zot-linux-amd64:v2.1.18"

// world holds the per-scenario state. Nothing is shared between scenarios;
// each gets its own upstream and its own epos-registry.
type world struct {
	upstreamURL string
	registryURL string

	resp     *http.Response
	respBody []byte

	// digests of manifests pushed upstream, keyed by "<repo>:<tag>".
	pushed map[string]string
	// content layers pushed upstream, keyed by "<repo>:<tag>". The digest
	// identifies the blob to fetch; the bytes are what a correct fetch returns.
	layers map[string]blob
	// lastBlobRef is the skill whose content blob was fetched most recently.
	lastBlobRef string

	// registryCmd is the running epos-registry, kept so a scenario that swaps
	// the upstream can restart it rather than leaving two behind.
	registryCmd *exec.Cmd

	// blobTarget stands in for the host an upstream redirect nominates, and
	// records every request that reaches it (see startRedirectingUpstream).
	blobTarget     *httptest.Server
	targetMu       sync.Mutex
	targetRequests []http.Header

	// metrics collects the running epos-registry's stdout, which is where the
	// exporter writes epos.downloads (SPEC.md 5.3).
	metrics *metricsOutput
}

// blob is a content layer as pushed upstream.
type blob struct {
	digest string
	bytes  []byte
}

func (w *world) reset() {
	w.stopRegistry()
	w.upstreamURL = ""
	w.registryURL = ""
	w.resp = nil
	w.respBody = nil
	w.pushed = map[string]string{}
	w.layers = map[string]blob{}
	w.lastBlobRef = ""

	if w.blobTarget != nil {
		w.blobTarget.Close()
		w.blobTarget = nil
	}
	w.targetMu.Lock()
	w.targetRequests = nil
	w.targetMu.Unlock()

	w.metrics = nil
}

// startUpstream brings up a real zot registry.
func (w *world) startUpstream(ctx context.Context) error {
	req := testcontainers.ContainerRequest{
		Image:        zotImage,
		ExposedPorts: []string{"5000/tcp"},
		WaitingFor:   wait.ForHTTP("/v2/").WithPort("5000/tcp").WithStartupTimeout(2 * time.Minute),
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return fmt.Errorf("start zot: %w", err)
	}
	testcontainers.CleanupContainer(godogT, c)

	endpoint, err := c.PortEndpoint(ctx, "5000/tcp", "http")
	if err != nil {
		return fmt.Errorf("zot endpoint: %w", err)
	}
	w.upstreamURL = endpoint
	return nil
}

// startRegistry runs the epos-registry binary in front of the upstream,
// replacing any instance already running for this scenario.
//
// The binary is exercised as a black box — the same way a real client reaches
// it — rather than by importing its handler, which lives in package main.
func (w *world) startRegistry(ctx context.Context) error {
	w.stopRegistry()

	port, err := freePort()
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	if w.upstreamURL == "" {
		return fmt.Errorf("upstream is not running")
	}

	// SPEC.md 5.3 makes stdout the exporter for godog runs, so the counter is
	// read back out of the process's own output. The interval is short so a
	// scenario does not wait on the SDK's minute-long default.
	cmd := exec.CommandContext(ctx, registryBin,
		"-addr", addr,
		"-upstream", w.upstreamURL,
		"-metrics-interval", metricsInterval.String(),
	)
	w.metrics = &metricsOutput{}
	cmd.Stdout = w.metrics
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start epos-registry: %w", err)
	}
	w.registryCmd = cmd
	godogT.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	w.registryURL = "http://" + addr
	return waitForReady(ctx, w.registryURL+"/v2/")
}

func (w *world) stopRegistry() {
	if w.registryCmd == nil {
		return
	}
	_ = w.registryCmd.Process.Kill()
	_ = w.registryCmd.Wait()
	w.registryCmd = nil
	w.registryURL = ""
}

func (w *world) request(method, target string) error {
	return w.requestWithHeaders(method, target, nil)
}

func (w *world) requestWithHeaders(method, target string, headers map[string]string) error {
	if w.registryURL == "" {
		return fmt.Errorf("epos-registry is not running")
	}

	req, err := http.NewRequest(method, w.registryURL+target, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
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
	return nil
}

func (w *world) statusIs(want int) error {
	if w.resp == nil {
		return fmt.Errorf("no response recorded")
	}
	if w.resp.StatusCode != want {
		return fmt.Errorf("status = %d, want %d", w.resp.StatusCode, want)
	}
	return nil
}

func (w *world) hasHeader(name string) error {
	if w.resp == nil {
		return fmt.Errorf("no response recorded")
	}
	if w.resp.Header.Get(name) == "" {
		return fmt.Errorf("%s header is absent", name)
	}
	return nil
}

// --- helpers ---------------------------------------------------------------

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitForReady(ctx context.Context, url string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("epos-registry did not become ready at %s", url)
}

// --- suite -----------------------------------------------------------------

// godogT is the testing.T of the running suite. godog's step signatures do not
// carry one, and testcontainers' cleanup helpers need it.
var godogT *testing.T

// registryBin is the epos-registry binary built once for the whole suite.
var registryBin string

func TestRegistryReadPath(t *testing.T) {
	godogT = t
	registryBin = buildRegistry(t)

	w := &world{}

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				w.reset()
				return ctx, nil
			})

			sc.Given(`^an upstream registry$`, func(ctx context.Context) error {
				return w.startUpstream(ctx)
			})
			sc.Given(`^epos-registry is fronting it$`, func(ctx context.Context) error {
				return w.startRegistry(ctx)
			})
			sc.Given(`^the skill "([^"]+)" version "([^"]+)" is present upstream$`,
				func(ctx context.Context, name, version string) error {
					return w.pushSkill(ctx, name, version)
				})

			sc.Given(`^the upstream redirects blob requests$`, func(ctx context.Context) error {
				return w.startRedirectingUpstream(ctx)
			})
			sc.Given(`^the upstream serves blobs directly$`, w.upstreamServesBlobsDirectly)

			sc.When(`^a client requests "([A-Z]+) ([^"]+)"$`, w.request)
			sc.When(`^a client resolves the manifest "([^"]+)"$`, func(ref string) error {
				return w.resolveManifest(http.MethodGet, ref)
			})
			sc.When(`^a client issues HEAD for the manifest "([^"]+)"$`, func(ref string) error {
				return w.resolveManifest(http.MethodHead, ref)
			})
			sc.When(`^a client lists the tags of "([^"]+)"$`, w.listTags)
			sc.When(`^a client lists the referrers of the "([^"]+)" manifest digest$`, w.listReferrers)
			sc.When(`^a client fetches a content blob of "([^"]+)"$`, w.fetchContentBlob)
			sc.When(`^a client fetches a content blob of "([^"]+)" with an Authorization header$`,
				w.fetchContentBlobWithAuthorization)
			sc.When(`^a client fetches a content blob of "([^"]+)" sending "([^"]+)"$`,
				w.fetchContentBlobSending)
			sc.Then(`^the response status is (\d+)$`, w.statusIs)
			sc.Then(`^no blob bytes passed through epos-registry$`, w.noBlobBytesPassedThrough)
			sc.Then(`^the blob content is returned unchanged$`, w.blobContentUnchanged)
			sc.Then(`^the redirect target receives no "([^"]+)" header$`, w.redirectTargetSawNoHeader)
			sc.Then(`^the download count for "([^"]+)" increases by (\d+)$`, w.downloadCountIncreasesBy)
			sc.Then(`^the download count for "([^"]+)" is unchanged$`, w.downloadCountUnchanged)
			sc.Then(`^the recorded download is verified$`, func() error {
				return w.recordedDownloadIs(true)
			})
			sc.Then(`^the recorded download is unverified$`, func() error {
				return w.recordedDownloadIs(false)
			})
			sc.Then(`^the response has an "([^"]+)" header$`, w.hasHeader)
			sc.Then(`^the returned digest matches the digest upstream reports$`, w.digestMatchesUpstream)
			sc.Then(`^no response body is returned$`, w.noBodyReturned)
			sc.Then(`^the tag list contains "([^"]+)" and "([^"]+)"$`, w.tagListContains)
		},
		Options: &godog.Options{
			Format: "pretty,junit:junit.xml",
			Paths:  []string{"../../features"},
			// Scenarios whose behaviour is not implemented yet are tagged @wip
			// and excluded, so CI stays green while they remain visibly
			// pending. The implementing milestone drops the tag.
			Tags:     "~@wip",
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("registry read path suite failed")
	}
}

// buildRegistry compiles epos-registry once for the suite.
func buildRegistry(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "epos-registry")
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/epos-registry")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build epos-registry: %v", err)
	}
	return bin
}
