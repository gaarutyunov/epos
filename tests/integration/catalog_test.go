//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
)

// catalogNamespace is the namespace the catalog enumerates.
const catalogNamespace = "demo/agent-skills"

// catalogSkills is what the Background publishes.
//
// Three of them, deliberately: two ordinary skills so a listing, a ranking and
// an "unknown is not zero" row all have something to work with, and one whose
// document is hostile — because the security boundary is asserted on the served
// page and not only on the renderer's own output.
var catalogSkills = []struct {
	repository string
	name       string
	version    string
	body       string
}{
	{
		repository: catalogNamespace + "/pdf",
		name:       "pdf",
		version:    "1.0.0",
		body:       "# Extracting from PDFs\n\nPull **text** out of a PDF.\n",
	},
	{
		repository: catalogNamespace + "/reviewer",
		name:       "reviewer",
		version:    "0.4.0",
		body:       "# Reviewing a change\n\nRead the diff twice.\n",
	},
	{
		repository: catalogNamespace + "/hostile",
		name:       "hostile",
		version:    "1.0.0",
		body: "# Hostile\n\n<script>alert(1)</script>\n" +
			"<img src=x onerror=\"alert(1)\">\n\n[click](javascript:alert(1))\n",
	},
}

// browser drives the catalog the way a reader does: a real registry, a real
// epos-registry with the catalog enabled, and real HTTP.
type browser struct {
	eposHome    string
	upstreamURL string
	registryURL string
	registryCmd *exec.Cmd
	countsFile  string
	exportDir   string

	containers []testcontainers.Container

	// upstreamProxy records what the catalog asked the registry for, so "no
	// request reaches the registry" is asserted over the calls actually made.
	upstreamProxy *httptest.Server
	requestsMu    sync.Mutex
	requests      []string

	body   string
	status int
	// pages keeps a served page for a later comparison against the export.
	pages map[string]string
}

func (b *browser) reset(t *testing.T) {
	b.stopRegistry()
	b.stopContainers()
	if b.upstreamProxy != nil {
		b.upstreamProxy.Close()
		b.upstreamProxy = nil
	}
	*b = browser{eposHome: t.TempDir(), pages: map[string]string{}}
}

func (b *browser) stopRegistry() {
	if b.registryCmd == nil || b.registryCmd.Process == nil {
		return
	}
	_ = b.registryCmd.Process.Kill()
	_ = b.registryCmd.Wait()
	b.registryCmd = nil
	b.registryURL = ""
}

func (b *browser) stopContainers() {
	for _, c := range b.containers {
		_ = c.Terminate(context.Background())
	}
	b.containers = nil
}

// --- Given ------------------------------------------------------------------

func (b *browser) aRegistryHoldingPublishedSkills(ctx context.Context) error {
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
	b.containers = append(b.containers, c)

	endpoint, err := c.PortEndpoint(ctx, "5000/tcp", "")
	if err != nil {
		return err
	}
	b.upstreamURL = endpoint

	for _, s := range catalogSkills {
		if err := b.publish(ctx, s.repository, s.name, s.version, s.body); err != nil {
			return err
		}
	}
	return b.startUpstreamProxy()
}

// publish packs a skill with the real CLI and copies it into the registry.
func (b *browser) publish(ctx context.Context, repository, name, version, body string) error {
	dir, err := os.MkdirTemp("", "epos-catalog-*")
	if err != nil {
		return err
	}
	document := fmt.Sprintf("---\nname: %s\nversion: %s\ndescription: the %s skill\nlicense: MIT\n---\n\n%s",
		name, version, name, body)
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(document), 0o600); err != nil {
		return err
	}

	cmd := exec.Command(catalogEposBin, "pack", dir)
	cmd.Env = append(os.Environ(), eposHomeEnv+"="+b.eposHome)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("pack %s: %v: %s", name, err, out)
	}

	src, err := oci.New(storeDir(b.eposHome))
	if err != nil {
		return err
	}
	repo, err := remote.NewRepository(b.upstreamURL + "/" + repository)
	if err != nil {
		return err
	}
	repo.PlainHTTP = true

	_, err = oras.Copy(ctx, src, name+":"+version, repo, version, oras.DefaultCopyOptions)
	return err
}

// startUpstreamProxy records every request the catalog makes of the registry.
func (b *browser) startUpstreamProxy() error {
	target, err := url.Parse("http://" + strings.TrimPrefix(b.upstreamURL, "http://"))
	if err != nil {
		return err
	}
	proxy := &httputil.ReverseProxy{Rewrite: func(pr *httputil.ProxyRequest) { pr.SetURL(target) }}

	b.upstreamProxy = httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			b.requestsMu.Lock()
			b.requests = append(b.requests, r.Method+" "+r.URL.Path)
			b.requestsMu.Unlock()
			proxy.ServeHTTP(w, r)
		}))
	return nil
}

func (b *browser) catalogIsEnabled(ctx context.Context) error {
	return b.startRegistry(ctx, nil)
}

func (b *browser) aStatisticsSourceHolding(ctx context.Context, pulls int, repository string) error {
	if err := b.writeCounts(repository, int64(pulls)); err != nil {
		return err
	}
	// Restarted rather than reconfigured: the source is chosen at startup, and
	// this step is what makes the difference between "no counts" and "counts"
	// observable in the same suite.
	return b.startRegistry(ctx, []string{
		"--catalog.stats-source", "file",
		"--catalog.stats-file", b.countsFile,
		// Zero, exactly: query on every request, rather than a test that sleeps.
		"--catalog.stats-ttl", "0s",
	})
}

func (b *browser) writeCounts(repository string, verified int64) error {
	if b.countsFile == "" {
		dir, err := os.MkdirTemp("", "epos-counts-*")
		if err != nil {
			return err
		}
		b.countsFile = filepath.Join(dir, "counts.json")
	}
	body, err := json.Marshal(map[string]any{
		"captured_at": time.Now().UTC().Format(time.RFC3339),
		"rows": map[string]any{
			repository: map[string]int64{"verified": verified, "unverified": verified * 3},
		},
	})
	if err != nil {
		return err
	}
	return os.WriteFile(b.countsFile, body, 0o600)
}

func (b *browser) startRegistry(ctx context.Context, extra []string) error {
	b.stopRegistry()

	port, err := freePort()
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	args := []string{
		"--addr", addr,
		"--upstream", b.upstreamProxy.URL,
		"--metrics.exporter", "none",
		"--catalog.enabled",
		"--catalog.namespace", catalogNamespace,
	}
	cmd := exec.CommandContext(ctx, catalogRegistryBin, append(args, extra...)...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start epos-registry: %w", err)
	}

	b.registryURL = "http://" + addr
	if err := waitForReady(ctx, b.registryURL+"/v2/"); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return err
	}
	b.registryCmd = cmd
	return nil
}

// --- When -------------------------------------------------------------------

func (b *browser) get(path string) error {
	resp, err := http.Get(b.registryURL + path) //nolint:noctx // the suite's own server, and a hang fails the test either way
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	b.body, b.status = string(body), resp.StatusCode
	b.pages[path] = b.body
	return nil
}

func (b *browser) theHomePageIsRequested() error        { return b.get("/") }
func (b *browser) theHomePageIsRequestedAgain() error   { return b.get("/") }
func (b *browser) thePageForIsRequested(r string) error { return b.get("/skills/" + r + "/") }

func (b *browser) theStatisticsSourceIsChangedTo(pulls int, repository string) error {
	return b.writeCounts(repository, int64(pulls))
}

func (b *browser) eachDistributionAPIEndpointIsRequested() error {
	for _, path := range []string{
		"/v2/",
		"/v2/_catalog",
		"/v2/" + catalogNamespace + "/pdf/tags/list",
		"/v2/" + catalogNamespace + "/pdf/manifests/1.0.0",
	} {
		if err := b.get(path); err != nil {
			return err
		}
		if b.status != http.StatusOK {
			return fmt.Errorf("%s answered %d, want 200", path, b.status)
		}
	}
	return nil
}

func (b *browser) aPageForAnUnlistedRepositoryIsRequested() error {
	b.requestsMu.Lock()
	b.requests = nil
	b.requestsMu.Unlock()
	return b.get("/skills/somebody/else/")
}

func (b *browser) theCatalogIsExported(ctx context.Context) error {
	b.exportDir = filepath.Join(os.TempDir(), fmt.Sprintf("epos-export-%d", time.Now().UnixNano()))

	args := []string{
		"catalog", "export",
		"--upstream", b.upstreamProxy.URL,
		"--catalog.namespace", catalogNamespace,
		"--out", b.exportDir,
	}
	if b.countsFile != "" {
		args = append(args, "--catalog.stats-source", "file", "--catalog.stats-file", b.countsFile)
	}

	cmd := exec.CommandContext(ctx, catalogRegistryBin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("export: %v: %s", err, out)
	}
	return nil
}

// --- Then -------------------------------------------------------------------

func (b *browser) thePageListsEveryPublishedSkill() error {
	for _, s := range catalogSkills {
		if !strings.Contains(b.body, s.repository) {
			return fmt.Errorf("the home page does not list %s", s.repository)
		}
	}
	return nil
}

func (b *browser) everySkillLinksToAPageOfItsOwn() error {
	// Snapshotted, because following a link overwrites b.body: asserting the
	// second skill's href against the first skill's detail page is a test that
	// fails for a reason having nothing to do with the catalog.
	home := b.body

	for _, s := range catalogSkills {
		href := `href="/skills/` + s.repository + `/"`
		if !strings.Contains(home, href) {
			return fmt.Errorf("the home page carries no %s", href)
		}
		if err := b.get("/skills/" + s.repository + "/"); err != nil {
			return err
		}
		if b.status != http.StatusOK {
			return fmt.Errorf("%s answered %d, want 200", s.repository, b.status)
		}
	}
	return nil
}

func (b *browser) thePageCarriesTheDocumentAsMarkup() error {
	if !strings.Contains(b.body, "<h1>Extracting from PDFs</h1>") {
		return fmt.Errorf("the document was not rendered as markup")
	}
	if !strings.Contains(b.body, "<strong>text</strong>") {
		return fmt.Errorf("the document's emphasis was not rendered")
	}
	return nil
}

func (b *browser) thePageDoesNotCarryTheFrontmatter() error {
	if strings.Contains(b.body, "license: MIT") {
		return fmt.Errorf("the frontmatter was rendered into the document")
	}
	return nil
}

func (b *browser) thePageCarriesNothingExecutable() error {
	for _, forbidden := range []string{"<script>alert", "onerror=", "javascript:"} {
		if strings.Contains(b.body, forbidden) {
			return fmt.Errorf("the rendered page carries %q", forbidden)
		}
	}
	return nil
}

func (b *browser) everyEndpointIsAnsweredByTheRelay() error {
	// Answered above; the assertion here is that none of them came back as a
	// catalog page, which is what a catalog route shadowing /v2/ would look
	// like.
	for path, body := range b.pages {
		if !strings.HasPrefix(path, "/v2/") {
			continue
		}
		if strings.Contains(body, "<!doctype html>") {
			return fmt.Errorf("%s was answered by the catalog", path)
		}
	}
	return nil
}

func (b *browser) aPullThroughEposRegistryStillSucceeds(ctx context.Context) error {
	repo, err := remote.NewRepository(
		strings.TrimPrefix(b.registryURL, "http://") + "/" + catalogNamespace + "/pdf")
	if err != nil {
		return err
	}
	repo.PlainHTTP = true

	desc, err := repo.Resolve(ctx, "1.0.0")
	if err != nil {
		return fmt.Errorf("resolve through epos-registry: %w", err)
	}
	if desc.Digest == "" {
		return fmt.Errorf("the manifest resolved to no digest")
	}
	return nil
}

func (b *browser) theCatalogAnswers404() error {
	if b.status != http.StatusNotFound {
		return fmt.Errorf("the catalog answered %d, want 404", b.status)
	}
	return nil
}

func (b *browser) noRequestForThatRepositoryReachesTheRegistry() error {
	b.requestsMu.Lock()
	defer b.requestsMu.Unlock()

	for _, r := range b.requests {
		if strings.Contains(r, "somebody/else") {
			return fmt.Errorf("the catalog fetched %s: a path is not an instruction to fetch", r)
		}
	}
	return nil
}

func (b *browser) thePageShowsPullsFor(pulls int, repository string) error {
	row, err := rowFor(b.body, repository)
	if err != nil {
		return err
	}
	if !strings.Contains(row, fmt.Sprintf(">%d<", pulls)) {
		return fmt.Errorf("the row for %s does not show %d pulls: %s", repository, pulls, row)
	}
	return nil
}

func (b *browser) thePageCarriesNoPullColumn() error {
	if strings.Contains(b.body, "label&#34;:&#34;Pulls") {
		return fmt.Errorf("the home page has a pull column with no statistics source")
	}
	if strings.Contains(b.body, "Most pulled") {
		return fmt.Errorf("the home page claims a ranking with no statistics source")
	}
	return nil
}

func (b *browser) theRowShowsAnUnknownCount(repository string) error {
	row, err := rowFor(b.body, repository)
	if err != nil {
		return err
	}
	if !strings.Contains(row, "—") {
		return fmt.Errorf("the row for %s does not show an unknown count: %s", repository, row)
	}
	if strings.Contains(row, ">0<") {
		return fmt.Errorf("the row for %s renders zero, which is not what is known", repository)
	}
	return nil
}

// rowFor cuts one table row out of a rendered page.
func rowFor(page, repository string) (string, error) {
	start := strings.Index(page, `href="/skills/`+repository+`/"`)
	if start < 0 {
		return "", fmt.Errorf("the page has no row for %s", repository)
	}
	rest := page[start:]
	end := strings.Index(rest, "</a>")
	if end < 0 {
		return "", fmt.Errorf("the row for %s is not closed", repository)
	}
	return rest[:end], nil
}

func (b *browser) theExportedHomePageIsIdenticalToTheServedOne() error {
	if err := b.get("/"); err != nil {
		return err
	}
	exported, err := os.ReadFile(filepath.Join(b.exportDir, "index.html"))
	if err != nil {
		return err
	}
	if string(exported) != b.body {
		return fmt.Errorf("the exported home page differs from the served one; " +
			"one renderer and two drivers means identical bytes")
	}
	return nil
}

func (b *browser) theExportCarriesAPageForEverySkill() error {
	for _, s := range catalogSkills {
		page := filepath.Join(b.exportDir, filepath.FromSlash("skills/"+s.repository+"/index.html"))
		if _, err := os.Stat(page); err != nil {
			return fmt.Errorf("the export has no page for %s: %w", s.repository, err)
		}
	}
	return nil
}

func (b *browser) theExportCarriesTheAssets() error {
	for _, name := range []string{
		"assets/app.css", "assets/app.js", "assets/ga-ui-kit.css", "assets/ga-ui-kit.min.js",
	} {
		if _, err := os.Stat(filepath.Join(b.exportDir, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("the export has no %s: %w", name, err)
		}
	}
	return nil
}

// --- suite ------------------------------------------------------------------

var (
	catalogEposBin     string
	catalogRegistryBin string
)

func TestBrowseTheCatalog(t *testing.T) {
	catalogEposBin = buildBinary(t, "epos", "../../cmd/epos")
	catalogRegistryBin = buildBinary(t, "epos-registry", "../../cmd/epos-registry")

	b := &browser{}
	t.Cleanup(func() {
		b.stopRegistry()
		b.stopContainers()
		if b.upstreamProxy != nil {
			b.upstreamProxy.Close()
		}
		if b.exportDir != "" {
			_ = os.RemoveAll(b.exportDir)
		}
	})

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				b.reset(t)
				return ctx, nil
			})

			sc.Given(`^a registry holding published skills$`, b.aRegistryHoldingPublishedSkills)
			sc.Given(`^epos-registry is fronting it with the catalog enabled$`, b.catalogIsEnabled)
			sc.Given(`^a statistics source holding (\d+) verified pulls of "([^"]+)"$`,
				b.aStatisticsSourceHolding)

			sc.When(`^the catalog's home page is requested$`, b.theHomePageIsRequested)
			sc.When(`^the catalog's home page is requested again$`, b.theHomePageIsRequestedAgain)
			sc.When(`^the page for "([^"]+)" is requested$`, b.thePageForIsRequested)
			sc.When(`^the statistics source is changed to (\d+) verified pulls$`,
				func(pulls int) error {
					return b.theStatisticsSourceIsChangedTo(pulls, catalogNamespace+"/pdf")
				})
			sc.When(`^each distribution API endpoint is requested$`,
				b.eachDistributionAPIEndpointIsRequested)
			sc.When(`^a page for a repository the catalog does not list is requested$`,
				b.aPageForAnUnlistedRepositoryIsRequested)
			sc.When(`^the catalog is exported to a directory$`, b.theCatalogIsExported)

			sc.Then(`^the page lists every published skill$`, b.thePageListsEveryPublishedSkill)
			sc.Then(`^every skill links to a page of its own$`, b.everySkillLinksToAPageOfItsOwn)
			sc.Then(`^the page carries the skill's SKILL.md rendered as markup$`,
				b.thePageCarriesTheDocumentAsMarkup)
			sc.Then(`^the page does not carry the frontmatter$`, b.thePageDoesNotCarryTheFrontmatter)
			sc.Then(`^the page carries no script tag and no event handler$`,
				b.thePageCarriesNothingExecutable)
			sc.Then(`^every one of them is answered by the relay$`, b.everyEndpointIsAnsweredByTheRelay)
			sc.Then(`^a pull through epos-registry still succeeds$`,
				b.aPullThroughEposRegistryStillSucceeds)
			sc.Then(`^the catalog answers 404$`, b.theCatalogAnswers404)
			sc.Then(`^no request for that repository reaches the registry$`,
				b.noRequestForThatRepositoryReachesTheRegistry)
			sc.Then(`^the page shows (\d+) pulls for "([^"]+)"$`, b.thePageShowsPullsFor)
			sc.Then(`^the page carries no pull column$`, b.thePageCarriesNoPullColumn)
			sc.Then(`^the row for "([^"]+)" shows an unknown count$`, b.theRowShowsAnUnknownCount)
			sc.Then(`^the exported home page is byte-identical to the served one$`,
				b.theExportedHomePageIsIdenticalToTheServedOne)
			sc.Then(`^the exported directory carries a page for every skill$`,
				b.theExportCarriesAPageForEverySkill)
			sc.Then(`^the exported directory carries the stylesheet and the script$`,
				b.theExportCarriesTheAssets)
		},
		Options: &godog.Options{
			Format:   "pretty,junit:junit-catalog.xml",
			Paths:    []string{"../../features/browse-the-catalog.feature"},
			Tags:     "~@wip",
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("browse the catalog suite failed")
	}
}
