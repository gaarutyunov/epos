//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
)

// skillNamespace is what the discovery commands are pointed at. The registry
// also holds a repository outside it, so the SPEC.md 7.2 step-2 filter has
// something to exclude.
const skillNamespace = "demo/agent-skills"

// publishedSkills is what the Background puts in the registry.
//
// Two versions of one skill so --versions has something to enumerate, distinct
// descriptions so search has something to match on, and one repository outside
// the skill namespace.
var publishedSkills = []struct {
	repository  string
	name        string
	version     string
	description string
}{
	{skillNamespace + "/pdf", "pdf", "1.0.0", "extracts text from PDF files"},
	{skillNamespace + "/pdf", "pdf", "1.1.0", "extracts text from PDF files"},
	{skillNamespace + "/reviewer", "reviewer", "1.0.0", "reviews code changes"},
	{"other/toolbox", "toolbox", "1.0.0", "not a skill at all"},
}

// discoverer drives epos list and epos search the way a user does: real
// binaries, a real store directory, a real zot behind a real epos-registry.
type discoverer struct {
	// eposHome is EPOS_HOME for the machine that packs and enumerates, and
	// pullerHome is a second machine whose store starts empty. Nothing here
	// moves HOME: EPOS_HOME redirects epos and only epos, whereas HOME is read
	// by everything else in the process too — and on Windows is not even the
	// variable os.UserHomeDir consults.
	//
	// The second home is what makes "a direct pull still works" mean anything:
	// the Background packs every skill into the first one, so a pull asserted
	// against that store would pass without pulling at all.
	eposHome   string
	pullerHome string

	upstreamURL string // real zot
	containers  []testcontainers.Container

	// noCatalog fronts zot and answers /v2/_catalog with a 404, leaving every
	// other request to the real registry.
	//
	// SPEC.md 7.1's "a registry without _catalog" is a configuration, not an
	// implementation: _catalog sits outside the Content Discovery conformance
	// category and hosted registries disable it. No OCI registry that can be
	// run in a container ships with it off, so the only way to reach that
	// configuration is to switch the one endpoint off in front of one. Nothing
	// else is intercepted — which is exactly what makes the "a direct pull
	// still works" scenario meaningful, because that pull is answered by zot.
	noCatalog *httptest.Server

	registryURL string
	registryCmd *exec.Cmd

	// proxy is what the CLI is pointed at: it forwards to epos-registry and
	// records the requests the client made, which is how laziness is asserted
	// on requests rather than on output shape (SPEC.md 7.2).
	proxy        *httptest.Server
	proxyTarget  atomic.Pointer[url.URL]
	requestsMu   sync.Mutex
	requests     []string
	proxyStarted bool

	// out is the combined output of the last epos run, and err its exit.
	out string
	err error

	resp     *http.Response
	respBody []byte
}

func (d *discoverer) reset(t *testing.T) {
	d.stopRegistry()
	d.stopContainers()

	if d.noCatalog != nil {
		d.noCatalog.Close()
		d.noCatalog = nil
	}

	root := t.TempDir()
	d.eposHome = filepath.Join(root, "epos")
	d.pullerHome = filepath.Join(root, "epos2")
	for _, dir := range []string{d.eposHome, d.pullerHome} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	d.upstreamURL = ""
	d.out, d.err = "", nil
	d.resp, d.respBody = nil, nil
	d.forgetRequests()
}

func (d *discoverer) stopContainers() {
	for _, c := range d.containers {
		if err := c.Terminate(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "terminate container: %v\n", err)
		}
	}
	d.containers = nil
}

func (d *discoverer) stopRegistry() {
	if d.registryCmd == nil {
		return
	}
	_ = d.registryCmd.Process.Kill()
	_ = d.registryCmd.Wait()
	d.registryCmd = nil
	d.registryURL = ""
}

func (d *discoverer) forgetRequests() {
	d.requestsMu.Lock()
	defer d.requestsMu.Unlock()
	d.requests = nil
}

func (d *discoverer) recordedRequests() []string {
	d.requestsMu.Lock()
	defer d.requestsMu.Unlock()
	return append([]string(nil), d.requests...)
}

// epos runs the CLI against the first machine's store.
func (d *discoverer) epos(args ...string) error {
	return d.eposIn(d.eposHome, args...)
}

// eposIn runs the CLI with its root at eposHome, recording the run's output and
// exit for the Then steps.
func (d *discoverer) eposIn(eposHome string, args ...string) error {
	cmd := exec.Command(discoverEposBin, args...)
	cmd.Env = append(os.Environ(), eposHomeEnv+"="+eposHome)
	out, err := cmd.CombinedOutput()
	d.out, d.err = strings.TrimRight(string(out), "\n"), err
	return nil
}

// --- Given ------------------------------------------------------------------

func (d *discoverer) aRegistryHoldingPublishedSkills(ctx context.Context) error {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        zotImage,
			ExposedPorts: []string{"5000/tcp"},
			WaitingFor: wait.ForHTTP("/v2/").WithPort("5000/tcp").
				WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		return fmt.Errorf("start zot: %w", err)
	}
	d.containers = append(d.containers, c)

	endpoint, err := c.PortEndpoint(ctx, "5000/tcp", "")
	if err != nil {
		return err
	}
	d.upstreamURL = endpoint

	for _, s := range publishedSkills {
		if err := d.publish(ctx, s.repository, s.name, s.version, s.description); err != nil {
			return err
		}
	}
	return nil
}

// publish packs a skill with the epos CLI and copies it to the registry.
//
// Copied with oras-go rather than pushed by epos, because Epos has no write
// path (SPEC.md 4.5): a skill reaches the registry through whatever client
// already holds the user's credentials. Packing it with the real CLI is what
// puts the frontmatter-derived annotations on the manifest that discovery then
// reads back (7.2 step 4).
func (d *discoverer) publish(ctx context.Context, repository, name, version, description string) error {
	dir, err := writeDiscoverableSkill(name, version, description)
	if err != nil {
		return err
	}
	if err := d.epos("pack", dir); err != nil {
		return err
	}
	if d.err != nil {
		return fmt.Errorf("pack %s:%s: %v: %s", name, version, d.err, d.out)
	}

	src, err := oci.New(storeDir(d.eposHome))
	if err != nil {
		return fmt.Errorf("open the author's store: %w", err)
	}
	repo, err := remote.NewRepository(d.upstreamURL + "/" + repository)
	if err != nil {
		return err
	}
	repo.PlainHTTP = true

	if _, err := oras.Copy(ctx, src, name+":"+version, repo, version,
		oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("publish %s:%s: %w", repository, version, err)
	}
	return nil
}

func (d *discoverer) eposRegistryIsFrontingIt(ctx context.Context) error {
	if err := d.startRegistry(ctx, d.upstreamURL); err != nil {
		return err
	}
	return d.startProxy()
}

// upstreamWithoutCatalog puts zot behind a front that does not serve _catalog,
// and restarts epos-registry against it.
func (d *discoverer) upstreamWithoutCatalog(ctx context.Context) error {
	if d.upstreamURL == "" {
		return fmt.Errorf("upstream is not running")
	}

	target, err := url.Parse("http://" + strings.TrimPrefix(d.upstreamURL, "http://"))
	if err != nil {
		return err
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) { pr.SetURL(target) }}

	d.noCatalog = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/_catalog" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(
				`{"errors":[{"code":"UNSUPPORTED","message":"catalog is not supported"}]}`))
			return
		}
		proxy.ServeHTTP(w, r)
	}))

	return d.startRegistry(ctx, d.noCatalog.URL)
}

// startRegistry runs epos-registry in front of upstream, replacing whatever was
// already running for this scenario.
func (d *discoverer) startRegistry(ctx context.Context, upstream string) error {
	d.stopRegistry()

	if upstream == "" {
		return fmt.Errorf("upstream is not running")
	}
	if !strings.HasPrefix(upstream, "http") {
		upstream = "http://" + upstream
	}

	port, err := freePort()
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd := exec.CommandContext(ctx, discoverRegistryBin,
		"--addr", addr,
		"--upstream", upstream,
		// Discovery does not touch the counter, and a stdout exporter would
		// only add noise to a failing scenario's output.
		"--metrics.exporter", "none",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start epos-registry: %w", err)
	}

	registryURL := "http://" + addr
	if err := waitForReady(ctx, registryURL+"/v2/"); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}

	d.registryURL, d.registryCmd = registryURL, cmd

	// The proxy outlives an epos-registry restart, so it is re-pointed rather
	// than rebuilt: the CLI's --registry must stay the same address across the
	// "the upstream does not implement _catalog" step.
	parsed, err := url.Parse(registryURL)
	if err != nil {
		return err
	}
	d.proxyTarget.Store(parsed)
	return nil
}

// startProxy puts a recording pass-through in front of epos-registry.
func (d *discoverer) startProxy() error {
	if d.proxyStarted {
		return nil
	}

	proxy := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) {
		if target := d.proxyTarget.Load(); target != nil {
			pr.SetURL(target)
		}
	}}
	d.proxy = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.requestsMu.Lock()
		d.requests = append(d.requests, r.Method+" "+r.URL.Path)
		d.requestsMu.Unlock()
		proxy.ServeHTTP(w, r)
	}))
	d.proxyStarted = true
	return nil
}

// --- When -------------------------------------------------------------------

// registryFlag is the address the CLI is given: the recording proxy, as
// host:port. A registry host carrying a port is the normal case, and the
// reference parser must not read the port as a tag.
func (d *discoverer) registryFlag() string {
	return strings.TrimPrefix(d.proxy.URL, "http://")
}

func (d *discoverer) listsTheSkills() error {
	d.forgetRequests()
	return d.epos("list",
		"--registry", d.registryFlag(), "--namespace", skillNamespace, "--plain-http")
}

func (d *discoverer) listsTheSkillsWithVersions() error {
	d.forgetRequests()
	return d.epos("list", "--versions",
		"--registry", d.registryFlag(), "--namespace", skillNamespace, "--plain-http")
}

func (d *discoverer) searchesFor(query string) error {
	d.forgetRequests()
	return d.epos("search", query,
		"--registry", d.registryFlag(), "--namespace", skillNamespace, "--plain-http")
}

// secondMachinePullsDirectly is the reference path of SPEC.md 7.1, which needs
// no catalog. It goes straight at epos-registry: a direct pull is not discovery
// and has no business being routed through the enumeration path.
//
// It runs against the second machine's empty store, so what the store holds
// afterwards is what the pull put there and not what the Background packed.
func (d *discoverer) secondMachinePullsDirectly(ref string) error {
	repository, version, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("reference %q is not <repository>:<version>", ref)
	}
	host := strings.TrimPrefix(d.registryURL, "http://")
	return d.eposIn(d.pullerHome, "pull", host+"/"+repository+":"+version, "--plain-http")
}

func (d *discoverer) requestsTheCatalogThroughEposRegistry() error {
	if d.registryURL == "" {
		return fmt.Errorf("epos-registry is not running")
	}

	resp, err := http.Get(d.registryURL + "/v2/_catalog")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	d.resp, d.respBody = resp, body
	return nil
}

// --- Then -------------------------------------------------------------------

func (d *discoverer) succeeded() error {
	if d.err != nil {
		return fmt.Errorf("the command failed: %v: %s", d.err, d.out)
	}
	return nil
}

func (d *discoverer) failed() error {
	if d.err == nil {
		return fmt.Errorf("the command succeeded and printed %q; SPEC.md 7.1 requires a non-zero exit", d.out)
	}
	var exit *exec.ExitError
	if !errors.As(d.err, &exit) {
		return fmt.Errorf("the command did not exit with a status: %v", d.err)
	}
	if exit.ExitCode() == 0 {
		return fmt.Errorf("exit code is 0, want non-zero")
	}
	return nil
}

func (d *discoverer) listingContains(want string) error {
	if err := d.succeeded(); err != nil {
		return err
	}
	if !strings.Contains(d.out, want) {
		return fmt.Errorf("the listing does not mention %q; it is:\n%s", want, d.out)
	}
	return nil
}

func (d *discoverer) listingDoesNotContain(unwanted string) error {
	if err := d.succeeded(); err != nil {
		return err
	}
	if strings.Contains(d.out, unwanted) {
		return fmt.Errorf("the listing mentions %q; it is:\n%s", unwanted, d.out)
	}
	return nil
}

func (d *discoverer) listingIsEmpty() error {
	if strings.TrimSpace(d.out) != "" {
		return fmt.Errorf("the listing is %q, want nothing", d.out)
	}
	return nil
}

// listingCarriesNameAndDescription checks step 4 actually read the annotations
// rather than leaving the columns blank.
func (d *discoverer) listingCarriesNameAndDescription() error {
	if err := d.succeeded(); err != nil {
		return err
	}

	want := skillNamespace + "/pdf:1.0.0\tpdf\textracts text from PDF files"
	for _, line := range strings.Split(d.out, "\n") {
		if strings.TrimSpace(line) == want {
			return nil
		}
	}
	return fmt.Errorf("no row reads %q; the listing is:\n%s", want, d.out)
}

func (d *discoverer) listingIsOrdered() error {
	if err := d.succeeded(); err != nil {
		return err
	}

	var refs []string
	for _, line := range strings.Split(d.out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		refs = append(refs, strings.Split(line, "\t")[0])
	}

	want := []string{
		skillNamespace + "/pdf:1.0.0",
		skillNamespace + "/pdf:1.1.0",
		skillNamespace + "/reviewer:1.0.0",
	}
	if len(refs) != len(want) {
		return fmt.Errorf("the listing has %d rows, want %d:\n%s", len(refs), len(want), d.out)
	}
	for i := range want {
		if refs[i] != want[i] {
			return fmt.Errorf("row %d is %q, want %q; the listing is:\n%s",
				i+1, refs[i], want[i], d.out)
		}
	}
	if !sort.StringsAreSorted(refs) {
		return fmt.Errorf("the listing is not sorted:\n%s", d.out)
	}
	return nil
}

func (d *discoverer) catalogWasRequested() error {
	for _, req := range d.recordedRequests() {
		if req == "GET /v2/_catalog" {
			return nil
		}
	}
	return fmt.Errorf("the catalog was never requested; the client made %v", d.recordedRequests())
}

// noRepositoryWasAskedForItsTags is the laziness assertion of SPEC.md 7.2. It
// is over the requests the client made, not over what it printed: a list that
// fetched everything and printed only the names would satisfy the output and
// break the requirement.
func (d *discoverer) noRepositoryWasAskedForItsTags() error {
	for _, req := range d.recordedRequests() {
		if strings.HasSuffix(req, "/tags/list") {
			return fmt.Errorf("the client issued %q; without --versions it must stop after the "+
				"namespace filter. All requests: %v", req, d.recordedRequests())
		}
	}
	return nil
}

func (d *discoverer) noManifestWasResolved() error {
	for _, req := range d.recordedRequests() {
		if strings.Contains(req, "/manifests/") {
			return fmt.Errorf("the client issued %q; without --versions no manifest is resolved. "+
				"All requests: %v", req, d.recordedRequests())
		}
	}
	return nil
}

func (d *discoverer) everyRepositoryWasAskedForItsTags() error {
	asked := map[string]bool{}
	for _, req := range d.recordedRequests() {
		if strings.HasSuffix(req, "/tags/list") {
			asked[req] = true
		}
	}

	for _, want := range []string{skillNamespace + "/pdf", skillNamespace + "/reviewer"} {
		if !asked["GET /v2/"+want+"/tags/list"] {
			return fmt.Errorf("%s was never asked for its tags; the client made %v",
				want, d.recordedRequests())
		}
	}
	if asked["GET /v2/other/toolbox/tags/list"] {
		return fmt.Errorf("a repository outside %s was asked for its tags", skillNamespace)
	}
	return nil
}

func (d *discoverer) errorSaysCatalogUnsupported() error {
	const want = "does not support catalog enumeration"
	if !strings.Contains(d.out, want) {
		return fmt.Errorf("the error does not say %q; it says:\n%s", want, d.out)
	}
	return nil
}

func (d *discoverer) responseStatusIs(want int) error {
	if d.resp == nil {
		return fmt.Errorf("no response recorded")
	}
	if d.resp.StatusCode != want {
		return fmt.Errorf("status = %d, want %d (body: %s)", d.resp.StatusCode, want, d.respBody)
	}
	return nil
}

func (d *discoverer) relayedCatalogContains(want string) error {
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.Unmarshal(d.respBody, &catalog); err != nil {
		return fmt.Errorf("parse the relayed catalog %q: %w", d.respBody, err)
	}
	for _, repository := range catalog.Repositories {
		if repository == want {
			return nil
		}
	}
	return fmt.Errorf("the relayed catalog is %v, want it to hold %q", catalog.Repositories, want)
}

// pullerStoreHolds reads the second machine's store, which held nothing before
// the pull.
func (d *discoverer) pullerStoreHolds(tag string) error {
	out := d.out
	if err := d.eposIn(d.pullerHome, "store", "ls"); err != nil {
		return err
	}
	if d.err != nil {
		return fmt.Errorf("store ls: %v: %s", d.err, d.out)
	}
	listed := d.out
	d.out = out

	for _, line := range strings.Split(listed, "\n") {
		if strings.TrimSpace(line) == tag {
			return nil
		}
	}
	return fmt.Errorf("the store holds %q, want %s", listed, tag)
}

// --- helpers ----------------------------------------------------------------

// writeDiscoverableSkill writes a skill whose frontmatter carries the
// description a search will match on.
func writeDiscoverableSkill(name, version, description string) (string, error) {
	dir, err := os.MkdirTemp("", "epos-discover-*")
	if err != nil {
		return "", err
	}
	body := fmt.Sprintf("---\nname: %s\nversion: %s\ndescription: %s\n---\n\n# %s\n",
		name, version, description, name)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o600); err != nil {
		return "", err
	}
	return dir, nil
}

// --- suite ------------------------------------------------------------------

var (
	discoverEposBin     string
	discoverRegistryBin string
)

func TestDiscoverAndSearch(t *testing.T) {
	discoverEposBin = buildBinary(t, "epos", "../../cmd/epos")
	discoverRegistryBin = buildBinary(t, "epos-registry", "../../cmd/epos-registry")

	d := &discoverer{}
	t.Cleanup(func() {
		d.stopRegistry()
		d.stopContainers()
		if d.noCatalog != nil {
			d.noCatalog.Close()
		}
		if d.proxy != nil {
			d.proxy.Close()
		}
	})

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				d.reset(t)
				return ctx, nil
			})

			sc.Given(`^a registry holding published skills$`, d.aRegistryHoldingPublishedSkills)
			sc.Given(`^epos-registry is fronting it$`, d.eposRegistryIsFrontingIt)
			sc.Given(`^the upstream does not implement _catalog$`, d.upstreamWithoutCatalog)

			sc.When(`^the author lists the skills$`, d.listsTheSkills)
			sc.When(`^the author lists the skills with versions$`, d.listsTheSkillsWithVersions)
			sc.When(`^the author searches for "([^"]*)"$`, d.searchesFor)
			sc.When(`^a second machine pulls "([^"]+)" directly$`, d.secondMachinePullsDirectly)
			sc.When(`^a client requests the catalog through epos-registry$`,
				d.requestsTheCatalogThroughEposRegistry)

			sc.Then(`^the listing contains "([^"]+)"$`, d.listingContains)
			sc.Then(`^the listing does not contain "([^"]+)"$`, d.listingDoesNotContain)
			sc.Then(`^the listing is empty$`, d.listingIsEmpty)
			sc.Then(`^the listing carries the skill name and description$`,
				d.listingCarriesNameAndDescription)
			sc.Then(`^the listing is ordered by repository and then version$`, d.listingIsOrdered)
			sc.Then(`^the registry catalog was requested$`, d.catalogWasRequested)
			sc.Then(`^no repository was asked for its tags$`, d.noRepositoryWasAskedForItsTags)
			sc.Then(`^no manifest was resolved$`, d.noManifestWasResolved)
			sc.Then(`^every repository was asked for its tags$`, d.everyRepositoryWasAskedForItsTags)
			sc.Then(`^the command succeeded$`, d.succeeded)
			sc.Then(`^the command failed$`, d.failed)
			sc.Then(`^the pull succeeded$`, d.succeeded)
			sc.Then(`^the error says catalog enumeration is unsupported$`,
				d.errorSaysCatalogUnsupported)
			sc.Then(`^the response status is (\d+)$`, d.responseStatusIs)
			sc.Then(`^the relayed catalog contains "([^"]+)"$`, d.relayedCatalogContains)
			sc.Then(`^that machine's store holds "([^"]+)"$`, d.pullerStoreHolds)
		},
		Options: &godog.Options{
			Format:   "pretty,junit:junit-discover.xml",
			Paths:    []string{"../../features/discover-and-search.feature"},
			Tags:     "~@wip",
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("discover and search suite failed")
	}
}
