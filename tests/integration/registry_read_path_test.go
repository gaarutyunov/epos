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

	// replicaURL and replicaCmd are the second epos-registry of the SPEC.md 4.4
	// scenario, fronting the same upstream as the first.
	replicaURL string
	replicaCmd *exec.Cmd

	// statuses records the status of each request a multi-request scenario
	// makes, so "both requests succeed" can be asserted over all of them.
	statuses []int

	// pulled is what a stock oras client fetched through epos-registry.
	pulled *pulledArtifact

	// blobTarget stands in for the host an upstream redirect nominates, and
	// records every request that reaches it (see startRedirectingUpstream).
	blobTarget     *httptest.Server
	targetMu       sync.Mutex
	targetRequests []http.Header

	// metrics collects the running epos-registry's stdout, which is where the
	// exporter writes epos.downloads (SPEC.md 5.3).
	metrics *metricsOutput

	// containers started for the current scenario, torn down when it ends.
	containers []testcontainers.Container
}

// track registers a container for teardown at the end of the scenario.
//
// Deliberately not testcontainers.CleanupContainer(godogT, …): that hangs the
// cleanup off the *suite's* testing.T, so every scenario's containers stay up
// until the whole run finishes. Each scenario starts at least one registry, so
// the disk cost grows with the scenario count and the suite eventually dies of
// "no space left on device" — first on a laptop, later in CI.
func (w *world) track(c testcontainers.Container) {
	w.containers = append(w.containers, c)
}

func (w *world) stopContainers() {
	for _, c := range w.containers {
		if err := c.Terminate(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "terminate container: %v\n", err)
		}
	}
	w.containers = nil
}

// blob is a content layer as pushed upstream.
type blob struct {
	digest string
	bytes  []byte
}

func (w *world) reset() {
	w.stopRegistry()
	w.stopContainers()
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
	w.statuses = nil
	w.pulled = nil
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
	w.track(c)

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

	url, cmd, metrics, err := w.spawnRegistry(ctx)
	if err != nil {
		return err
	}
	w.registryURL, w.registryCmd, w.metrics = url, cmd, metrics
	return nil
}

// startReplica brings up a second epos-registry in front of the same upstream,
// leaving the first running.
//
// SPEC.md 4.4 has no shared store between replicas, so "a second replica" is
// simply another process pointed at the same upstream — if that were not true,
// this scenario is where it would show.
func (w *world) startReplica(ctx context.Context) error {
	if w.registryURL == "" {
		return fmt.Errorf("the first epos-registry is not running")
	}

	url, cmd, _, err := w.spawnRegistry(ctx)
	if err != nil {
		return fmt.Errorf("start second replica: %w", err)
	}
	w.replicaURL, w.replicaCmd = url, cmd
	return nil
}

// spawnRegistry starts one epos-registry process against the current upstream
// and waits for it to answer.
func (w *world) spawnRegistry(ctx context.Context) (string, *exec.Cmd, *metricsOutput, error) {
	if w.upstreamURL == "" {
		return "", nil, nil, fmt.Errorf("upstream is not running")
	}

	port, err := freePort()
	if err != nil {
		return "", nil, nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// SPEC.md 5.3 makes stdout the exporter for godog runs, so the counter is
	// read back out of the process's own output. The interval is short so a
	// scenario does not wait on the SDK's minute-long default.
	cmd := exec.CommandContext(ctx, registryBin,
		"--addr", addr,
		"--upstream", w.upstreamURL,
		"--metrics.interval", metricsInterval.String(),
	)
	out := &metricsOutput{}
	cmd.Stdout = out
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return "", nil, nil, fmt.Errorf("start epos-registry: %w", err)
	}

	url := "http://" + addr
	if err := waitForReady(ctx, url+"/v2/"); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", nil, nil, err
	}
	return url, cmd, out, nil
}

func (w *world) stopRegistry() {
	if w.replicaCmd != nil {
		_ = w.replicaCmd.Process.Kill()
		_ = w.replicaCmd.Wait()
		w.replicaCmd = nil
		w.replicaURL = ""
	}
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
	// reset() tears down the previous scenario's containers, which leaves the
	// last scenario's still up when the suite ends.
	t.Cleanup(func() {
		w.stopRegistry()
		w.stopContainers()
	})

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
			sc.Given(`^a second epos-registry replica is fronting the same upstream$`,
				func(ctx context.Context) error { return w.startReplica(ctx) })

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
			sc.When(`^oras pulls "([^"]+)" through epos-registry$`, w.orasPullsThrough)
			sc.When(`^a client resolves the manifest "([^"]+)" against the (first|second) replica$`,
				w.resolveManifestAgainst)
			sc.When(`^a client fetches a content blob of "([^"]+)" against the (first|second) replica$`,
				w.fetchContentBlobAgainst)
			sc.Then(`^the response status is (\d+)$`, w.statusIs)
			sc.Then(`^the pulled artifact matches the one pushed upstream$`, w.pulledArtifactMatchesUpstream)
			sc.Then(`^both requests succeed$`, w.bothRequestsSucceed)
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
