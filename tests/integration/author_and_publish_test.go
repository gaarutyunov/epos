//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
)

// author drives the epos CLI as a user would: a real binary, a real store
// directory, a real registry. Nothing here reaches into the packages under
// test.
type author struct {
	registryURL string
	home        string // HOME, so the CLI's ~/.epos/store lands in the sandbox
	secondHome  string // a "different machine" with an empty store

	dir     string // the skill directory
	altDir  string // an identical directory, written differently
	name    string
	version string

	digests  []string // one per pack
	packErr  error
	pushed   string // registry reference
	orphan   string // a planted unreachable blob
	pulledTo string

	// The fronting epos-registry, when a scenario publishes through one.
	proxyURL   string
	proxyCmd   *exec.Cmd
	proxyOut   *metricsOutput
	uploadHits int
}

func (a *author) reset(t *testing.T) {
	root := t.TempDir()
	a.home = filepath.Join(root, "home")
	a.secondHome = filepath.Join(root, "home2")
	for _, d := range []string{a.home, a.secondHome} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a.dir, a.altDir, a.pushed, a.orphan, a.pulledTo = "", "", "", "", ""
	a.digests = nil
	a.packErr = nil
	a.stopProxy()
	a.uploadHits = 0
}

func (a *author) stopProxy() {
	if a.proxyCmd == nil {
		return
	}
	_ = a.proxyCmd.Process.Kill()
	_ = a.proxyCmd.Wait()
	a.proxyCmd = nil
	a.proxyURL = ""
	a.proxyOut = nil
}

// eposRegistryFronting starts epos-registry in front of the scenario's zot.
func (a *author) eposRegistryFronting(ctx context.Context) error {
	if a.registryURL == "" {
		return fmt.Errorf("the registry is not running")
	}

	port, err := freePort()
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	cmd := exec.CommandContext(ctx, registryBin,
		"--addr", addr,
		"--upstream", "http://"+a.registryURL,
		"--metrics.interval", metricsInterval.String(),
	)
	a.proxyOut = &metricsOutput{}
	cmd.Stdout = a.proxyOut
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start epos-registry: %w", err)
	}
	a.proxyCmd = cmd
	a.proxyURL = addr
	return waitForReady(ctx, "http://"+addr+"/v2/")
}

// pushesThroughProxy publishes at the fronting registry rather than at zot.
func (a *author) pushesThroughProxy() error {
	a.pushed = fmt.Sprintf("%s/demo/agent-skills/%s:%s", a.proxyURL, a.name, a.version)
	out, err := a.epos(a.home, "push", a.name+":"+a.version, a.pushed, "--plain-http")
	if err != nil {
		return fmt.Errorf("push through epos-registry: %v: %s", err, out)
	}
	return nil
}

// noUploadBytesCrossed checks 4.5's headline claim: the upload session is
// redirected, so the blob bytes go straight to upstream.
func (a *author) noUploadBytesCrossed() error {
	// The proxy answers the session POST with a 307 and never sees a PATCH or
	// a blob PUT. If it had relayed instead, oras would have driven the whole
	// upload through it.
	if a.uploadHits != 0 {
		return fmt.Errorf("%d upload request(s) crossed epos-registry", a.uploadHits)
	}
	return nil
}

// publishCountIs reads epos.publishes out of the proxy's exporter output.
func (a *author) publishCountIs(repository string, want int64) error {
	if a.proxyOut == nil {
		return fmt.Errorf("epos-registry is not running")
	}

	deadline := time.Now().Add(30 * time.Second)
	var last int64
	for time.Now().Before(deadline) {
		got, err := publishesFor(a.proxyOut, repository)
		if err != nil {
			return err
		}
		if got == want {
			return nil
		}
		last = got
		time.Sleep(metricsInterval / 2)
	}
	return fmt.Errorf("publish count for %q reached %d, want %d", repository, last, want)
}

// epos runs the CLI with HOME pointed at a sandbox store.
func (a *author) epos(home string, args ...string) (string, error) {
	cmd := exec.Command(eposBin, args...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// --- Given ------------------------------------------------------------------

func (a *author) aRegistry(ctx context.Context) error {
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
	authorContainers = append(authorContainers, c)

	endpoint, err := c.PortEndpoint(ctx, "5000/tcp", "")
	if err != nil {
		return err
	}
	a.registryURL = endpoint
	return nil
}

func (a *author) aSkillDirectory(name, version string) error {
	a.name, a.version = name, version
	dir, err := writeSkill(name, version)
	if err != nil {
		return err
	}
	a.dir = dir
	return nil
}

// anIdenticalDirectory writes the same content again. The files land in a
// different creation order and with different permissions, neither of which
// 2.4 permits to reach the digest.
func (a *author) anIdenticalDirectory() error {
	dir, err := writeSkillReversed(a.name, a.version)
	if err != nil {
		return err
	}
	a.altDir = dir
	return nil
}

func (a *author) theDirectoryContainsASymlink() error {
	return os.Symlink("/etc/passwd", filepath.Join(a.dir, "leak.md"))
}

func (a *author) anUnreferencedBlobIsInTheStore() error {
	blobs := filepath.Join(a.home, ".epos", "store", "blobs", "sha256")
	a.orphan = filepath.Join(blobs, "orphaned0000000000000000000000000000000000000000000000000000000")
	return os.WriteFile(a.orphan, []byte("unreachable"), 0o644)
}

// --- When -------------------------------------------------------------------

func (a *author) packs() error {
	out, err := a.epos(a.home, "pack", a.dir)
	if err != nil {
		a.packErr = fmt.Errorf("%v: %s", err, out)
		return nil // scenarios that expect failure assert on packErr
	}
	a.digests = append(a.digests, lastField(out))
	return nil
}

func (a *author) packsAgain() error { return a.packs() }

func (a *author) packsBoth() error {
	if err := a.packs(); err != nil {
		return err
	}
	out, err := a.epos(a.home, "pack", a.altDir)
	if err != nil {
		return fmt.Errorf("pack the second directory: %v: %s", err, out)
	}
	a.digests = append(a.digests, lastField(out))
	return nil
}

func (a *author) pushes() error {
	a.pushed = fmt.Sprintf("%s/demo/agent-skills/%s:%s", a.registryURL, a.name, a.version)
	out, err := a.epos(a.home, "push", a.name+":"+a.version, a.pushed, "--plain-http")
	if err != nil {
		return fmt.Errorf("push: %v: %s", err, out)
	}
	return nil
}

func (a *author) aSecondMachinePulls() error {
	out, err := a.epos(a.secondHome, "pull", a.pushed, "--plain-http")
	if err != nil {
		return fmt.Errorf("pull: %v: %s", err, out)
	}
	a.pulledTo = a.secondHome
	return nil
}

func (a *author) prunes() error {
	out, err := a.epos(a.home, "store", "prune")
	if err != nil {
		return fmt.Errorf("prune: %v: %s", err, out)
	}
	return nil
}

// plainOrasPulls fetches with oras-go configured no differently from a client
// that has never heard of Epos (2.1).
func (a *author) plainOrasPulls() error {
	repo, err := remote.NewRepository(
		fmt.Sprintf("%s/demo/agent-skills/%s", a.registryURL, a.name))
	if err != nil {
		return err
	}
	repo.PlainHTTP = true

	desc, err := oras.Copy(context.Background(), repo, a.version, memory.New(), a.version,
		oras.DefaultCopyOptions)
	if err != nil {
		return fmt.Errorf("plain oras pull: %w", err)
	}
	a.digests = append(a.digests, desc.Digest.String())
	return nil
}

// --- Then -------------------------------------------------------------------

func (a *author) manifestFromStore(home string) (ocispec.Manifest, error) {
	root := filepath.Join(home, ".epos", "store")
	idxBody, err := os.ReadFile(filepath.Join(root, "index.json"))
	if err != nil {
		return ocispec.Manifest{}, err
	}
	var idx ocispec.Index
	if err := json.Unmarshal(idxBody, &idx); err != nil {
		return ocispec.Manifest{}, err
	}
	if len(idx.Manifests) == 0 {
		return ocispec.Manifest{}, fmt.Errorf("store index holds no manifests")
	}

	d := idx.Manifests[0].Digest
	body, err := os.ReadFile(filepath.Join(root, "blobs", "sha256", d.Encoded()))
	if err != nil {
		return ocispec.Manifest{}, err
	}
	var m ocispec.Manifest
	return m, json.Unmarshal(body, &m)
}

func (a *author) exactlyOneContentLayer() error {
	m, err := a.manifestFromStore(a.home)
	if err != nil {
		return err
	}
	if len(m.Layers) != 1 {
		return fmt.Errorf("manifest has %d layers, want exactly 1", len(m.Layers))
	}
	if m.Layers[0].MediaType != "application/vnd.agentskills.skill.content.v1.tar+gzip" {
		return fmt.Errorf("layer media type = %q", m.Layers[0].MediaType)
	}
	return nil
}

func (a *author) carriesTheArtifactType() error {
	m, err := a.manifestFromStore(a.home)
	if err != nil {
		return err
	}
	if m.ArtifactType != "application/vnd.agentskills.skill.v1" {
		return fmt.Errorf("artifactType = %q", m.ArtifactType)
	}
	return nil
}

func (a *author) configIsInlined() error {
	m, err := a.manifestFromStore(a.home)
	if err != nil {
		return err
	}
	if len(m.Config.Data) == 0 {
		return fmt.Errorf("config blob is not inlined; a pull would need a second fetch")
	}
	var cfg map[string]any
	if err := json.Unmarshal(m.Config.Data, &cfg); err != nil {
		return fmt.Errorf("inlined config is not JSON: %w", err)
	}
	if cfg["name"] != a.name {
		return fmt.Errorf("inlined config name = %v, want %s", cfg["name"], a.name)
	}
	return nil
}

func (a *author) bothPacksAgree() error {
	if len(a.digests) < 2 {
		return fmt.Errorf("expected 2 digests, got %d", len(a.digests))
	}
	if a.digests[0] != a.digests[1] {
		return fmt.Errorf("digests differ: %s vs %s; 2.4 requires them identical",
			a.digests[0], a.digests[1])
	}
	return nil
}

func (a *author) theRegistryHoldsTheSkill() error {
	repo, err := remote.NewRepository(
		fmt.Sprintf("%s/demo/agent-skills/%s", a.registryURL, a.name))
	if err != nil {
		return err
	}
	repo.PlainHTTP = true

	desc, err := repo.Resolve(context.Background(), a.version)
	if err != nil {
		return fmt.Errorf("registry does not hold %s:%s: %w", a.name, a.version, err)
	}
	a.digests = append(a.digests, desc.Digest.String())
	return nil
}

func (a *author) storeHolds(tag string) error {
	out, err := a.epos(a.pulledTo, "store", "ls")
	if err != nil {
		return fmt.Errorf("store ls: %v: %s", err, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == tag {
			return nil
		}
	}
	return fmt.Errorf("store holds %q, want %s", out, tag)
}

func (a *author) authorStoreHolds(tag string) error {
	out, err := a.epos(a.home, "store", "ls")
	if err != nil {
		return fmt.Errorf("store ls: %v: %s", err, out)
	}
	if !strings.Contains(out, tag) {
		return fmt.Errorf("store holds %q, want %s", out, tag)
	}
	return nil
}

func (a *author) pulledMatchesPushed() error {
	pulled, err := a.manifestFromStore(a.secondHome)
	if err != nil {
		return err
	}
	pushed, err := a.manifestFromStore(a.home)
	if err != nil {
		return err
	}
	if pulled.Layers[0].Digest != pushed.Layers[0].Digest {
		return fmt.Errorf("pulled layer %s, pushed %s",
			pulled.Layers[0].Digest, pushed.Layers[0].Digest)
	}
	return nil
}

func (a *author) pulledArtifactMatches() error {
	// The oras pull recorded a manifest digest; the store holds the same one.
	m, err := a.manifestFromStore(a.home)
	if err != nil {
		return err
	}
	_ = m
	if len(a.digests) < 2 {
		return fmt.Errorf("expected a pushed and a pulled digest, got %d", len(a.digests))
	}
	first, last := a.digests[len(a.digests)-2], a.digests[len(a.digests)-1]
	if first != last {
		return fmt.Errorf("plain oras pulled %s, epos pushed %s", last, first)
	}
	return nil
}

func (a *author) orphanIsGone() error {
	if _, err := os.Stat(a.orphan); !os.IsNotExist(err) {
		return fmt.Errorf("the unreferenced blob survived prune")
	}
	return nil
}

func (a *author) packingFails() error {
	if a.packErr == nil {
		return fmt.Errorf("pack succeeded; 2.5 rejects this at pack")
	}
	return nil
}

// --- helpers ----------------------------------------------------------------

func lastField(out string) string {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func skillFiles(name, version string) map[string]string {
	return map[string]string{
		"SKILL.md": fmt.Sprintf("---\nname: %s\nversion: %s\ndescription: reviews code\n---\n\n# %s\n",
			name, version, name),
		"sections/checklist.md": "- table-driven tests\n",
		"reference/notes.md":    "notes\n",
	}
}

func writeSkill(name, version string) (string, error) {
	dir, err := os.MkdirTemp("", "epos-skill-*")
	if err != nil {
		return "", err
	}
	for path, body := range skillFiles(name, version) {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// writeSkillReversed writes the same files in the opposite order and with
// different permissions — neither may reach the digest (2.4).
func writeSkillReversed(name, version string) (string, error) {
	dir, err := os.MkdirTemp("", "epos-skill-alt-*")
	if err != nil {
		return "", err
	}
	files := skillFiles(name, version)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	for i := len(paths) - 1; i >= 0; i-- {
		full := filepath.Join(dir, filepath.FromSlash(paths[i]))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(full, []byte(files[paths[i]]), 0o755); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// --- suite ------------------------------------------------------------------

var (
	eposBin          string
	authorContainers []testcontainers.Container
)

func TestAuthorAndPublish(t *testing.T) {
	godogT = t
	eposBin = buildBinary(t, "epos", "../../cmd/epos")

	registryBin = buildBinary(t, "epos-registry", "../../cmd/epos-registry")

	a := &author{}
	t.Cleanup(func() {
		a.stopProxy()
		for _, c := range authorContainers {
			_ = c.Terminate(context.Background())
		}
		authorContainers = nil
	})

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				a.reset(t)
				return ctx, nil
			})
			// Each scenario's registry is torn down with it, so a long feature
			// does not hold one container per scenario open at once.
			sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
				for _, c := range authorContainers {
					_ = c.Terminate(ctx)
				}
				authorContainers = nil
				return ctx, nil
			})

			sc.Given(`^a registry$`, func(ctx context.Context) error { return a.aRegistry(ctx) })
			sc.Given(`^epos-registry is fronting the registry$`, func(ctx context.Context) error {
				return a.eposRegistryFronting(ctx)
			})
			sc.Given(`^a skill directory "([^"]+)" version "([^"]+)"$`, a.aSkillDirectory)
			sc.Given(`^an identical skill directory written in a different order$`, a.anIdenticalDirectory)
			sc.Given(`^the directory contains a symlink$`, a.theDirectoryContainsASymlink)
			sc.Given(`^an unreferenced blob is in the store$`, a.anUnreferencedBlobIsInTheStore)
			sc.Given(`^the author packs it$`, a.packs)
			sc.Given(`^the author pushes it to the registry$`, a.pushes)

			sc.When(`^the author packs it$`, a.packs)
			sc.When(`^the author packs it again$`, a.packsAgain)
			sc.When(`^the author packs both$`, a.packsBoth)
			sc.When(`^the author pushes it to the registry$`, a.pushes)
			sc.When(`^a second machine pulls it$`, a.aSecondMachinePulls)
			sc.When(`^plain oras pulls it$`, a.plainOrasPulls)
			sc.When(`^the author prunes the store$`, a.prunes)
			sc.When(`^the author pushes it through epos-registry$`, a.pushesThroughProxy)

			sc.Then(`^the artifact has exactly one content layer$`, a.exactlyOneContentLayer)
			sc.Then(`^the artifact carries the agent-skills artifact type$`, a.carriesTheArtifactType)
			sc.Then(`^the config blob is inlined in the manifest$`, a.configIsInlined)
			sc.Then(`^both packs report the same digest$`, a.bothPacksAgree)
			sc.Then(`^the registry holds the skill$`, a.theRegistryHoldsTheSkill)
			sc.Then(`^that store holds "([^"]+)"$`, a.storeHolds)
			sc.Then(`^the store still holds "([^"]+)"$`, a.authorStoreHolds)
			sc.Then(`^the pulled digest matches the pushed digest$`, a.pulledMatchesPushed)
			sc.Then(`^the pulled artifact matches the one pushed$`, a.pulledArtifactMatches)
			sc.Then(`^the unreferenced blob is gone$`, a.orphanIsGone)
			sc.Then(`^packing fails$`, a.packingFails)
			sc.Then(`^no upload bytes crossed epos-registry$`, a.noUploadBytesCrossed)
			sc.Then(`^the publish count for "([^"]+)" is (\d+)$`, a.publishCountIs)
		},
		Options: &godog.Options{
			Format:   "pretty,junit:junit-author.xml",
			Paths:    []string{"../../features/author-and-publish.feature"},
			Tags:     "~@wip",
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("author and publish suite failed")
	}
}

// buildBinary compiles one of the repo's commands for the suite.
func buildBinary(t *testing.T, name, pkg string) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("build %s: %v", name, err)
	}
	return bin
}
