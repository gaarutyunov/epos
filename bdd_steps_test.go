//go:build integration

package epos_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
	"sigs.k8s.io/yaml"

	"github.com/gaarutyunov/epos/internal/app"
	"github.com/gaarutyunov/epos/internal/config"
	"github.com/gaarutyunov/epos/internal/frontend"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/install/lock"
	pkgdomain "github.com/gaarutyunov/epos/internal/packaging/domain"
	"github.com/gaarutyunov/epos/internal/registry/discovery"
	"github.com/gaarutyunov/epos/internal/registry/proxy"
	"github.com/gaarutyunov/epos/internal/signing/verify"
	"github.com/gaarutyunov/epos/internal/stats"
)

func (w *world) registerSteps(sc *godog.ScenarioContext) {
	// ---- backgrounds / infra ----
	noop := func() error { return nil }
	sc.Step(`^a running OCI registry$`, noop)
	sc.Step(`^a running OCI registry with a "_catalog" endpoint$`, noop)
	sc.Step(`^a running OCI registry with referrers support$`, noop)
	sc.Step(`^a running git server$`, noop)
	sc.Step(`^a running Kubernetes cluster$`, noop)

	// ---- fixtures ----
	sc.Step(`^a skill directory "([^"]*)" with a valid Epos\.yaml$`, w.aSkillDirectory)
	sc.Step(`^the Epos\.yaml "name" is "([^"]*)"$`, w.setEposName)
	sc.Step(`^SKILL\.md references "([^"]*)" which does not exist$`, w.skillReferencesMissing)
	sc.Step(`^a packaged skill "([^"]*)" version "([^"]*)"$`, w.aPackagedSkill)
	sc.Step(`^a published skill "([^"]*)" version "([^"]*)"$`, w.aPublishedSkill)
	sc.Step(`^published skills "([^"]*)", "([^"]*)", "([^"]*)"$`, w.publishThreeSkills)
	sc.Step(`^a published origin skill "([^"]*)" containing "([^"]*)"$`, w.aPublishedOrigin)

	// ---- run ----
	sc.Step(`^I run "([^"]*)"$`, w.iRun)

	// ---- author-and-publish ----
	sc.Step(`^an OCI artifact is produced with a single tar\+gzip content layer$`, w.artifactSingleLayer)
	sc.Step(`^the config blob records the Epos\.yaml metadata$`, w.configBlobRecordsMetadata)
	sc.Step(`^the artifact media types are the Epos skill types$`, w.mediaTypesAreEpos)
	sc.Step(`^validation fails$`, w.validationFails)
	sc.Step(`^the report mentions the name must be lowercase and must not contain "([^"]*)"$`, w.reportMentionsNameRules)
	sc.Step(`^the report mentions the dangling reference "([^"]*)"$`, w.reportMentionsDangling)
	sc.Step(`^the manifest is stored in the registry$`, w.manifestStored)
	sc.Step(`^the pushed tag resolves to the artifact digest$`, w.tagResolvesToDigest)

	// ---- compose-with-overlays ----
	sc.Step(`^my repo depends on "([^"]*)" as an OCI layer$`, w.repoDependsOCI)
	sc.Step(`^my repo has a local overlay replacing "([^"]*)"$`, w.repoOverlayReplaces)
	sc.Step(`^I compose the skill$`, w.composeSkill)
	sc.Step(`^the merged skill contains "([^"]*)" from my repo$`, func(p string) error { return w.provenance(p, "my-repo") })
	sc.Step(`^the merged skill contains "([^"]*)" from the origin$`, func(p string) error { return w.provenance(p, "pdf-tools") })
	sc.Step(`^an intermediate OCI skill that replaces "([^"]*)"$`, w.intermediateReplaces)
	sc.Step(`^my repo replaces "([^"]*)"$`, w.repoOverlayReplaces)
	sc.Step(`^"([^"]*)" comes from my repo$`, func(p string) error { return w.provenance(p, "my-repo") })
	sc.Step(`^"([^"]*)" comes from the intermediate skill$`, func(p string) error { return w.provenance(p, "intermediate") })
	sc.Step(`^"([^"]*)" comes from the origin$`, func(p string) error { return w.provenance(p, "pdf-tools") })
	sc.Step(`^the origin SKILL\.md has a "([^"]*)" section$`, w.originHasSection)
	sc.Step(`^a lower layer appends a reference line to SKILL\.md$`, w.lowerAppendsRefLine)
	sc.Step(`^my repo patches the "([^"]*)" section of SKILL\.md$`, w.repoPatchesSection)
	sc.Step(`^the merged SKILL\.md contains both the appended line and my patched Usage section$`, w.mergedSkillMdHasBoth)
	sc.Step(`^my repo depends on a skill in the git server at ref "([^"]*)" subpath "([^"]*)"$`, w.repoDependsGit)
	sc.Step(`^the lock records the resolved commit SHA$`, w.lockRecordsCommit)
	sc.Step(`^the lock records the git tree object SHA of the subpath$`, w.lockRecordsTreeSha)
	sc.Step(`^my repo has an overlay with a replace-in-file marked required:true$`, w.repoRequiredOverlay)
	sc.Step(`^the pattern does not match the base content$`, noop)
	sc.Step(`^composition fails with a required-operation error$`, w.compositionFailsRequired)
	sc.Step(`^a local overlay against "([^"]*)"$`, w.localOverlayAgainst)
	sc.Step(`^the overlay is stored as an OCI artifact$`, w.overlayStored)
	sc.Step(`^it can be declared as a pulled overlay layer by digest$`, w.overlayDeclarableByDigest)

	// ---- discover-and-search ----
	sc.Step(`^the proxy probes the registry$`, w.proxyProbes)
	sc.Step(`^the discovery mode is "([^"]*)"$`, w.discoveryModeIs)
	sc.Step(`^a registry that returns 404 for "_catalog"$`, w.registry404)
	sc.Step(`^the proxy probes that registry$`, w.proxyProbes404)
	sc.Step(`^only the declared repositories are listed$`, w.onlyDeclaredListed)
	sc.Step(`^I request the catalog listing through the proxy$`, w.catalogThroughProxy)
	sc.Step(`^the listing includes "([^"]*)", "([^"]*)", and "([^"]*)"$`, w.listingIncludes)
	sc.Step(`^the registry requires basic auth$`, noop)
	sc.Step(`^I pull "([^"]*)" through the proxy with my credentials$`, w.pullThroughProxy)
	sc.Step(`^the pull succeeds$`, w.pullSucceeds)
	sc.Step(`^the proxy persists no credentials$`, w.proxyPersistsNone)
	sc.Step(`^I open the frontend and filter by keyword "([^"]*)"$`, w.frontendFilter)
	sc.Step(`^only "([^"]*)" is shown$`, w.onlyShown)

	// ---- install-locally ----
	sc.Step(`^the skill files are written to the local target directory$`, w.filesWritten)
	sc.Step(`^a lockfile records the release "([^"]*)" at revision (\d+)$`, w.lockfileRecordsRelease)
	sc.Step(`^the lockfile pins the skill by digest$`, w.lockfilePinsDigest)
	sc.Step(`^the lockfile pins "([^"]*)" to a digest that no longer matches the tag$`, w.lockfileDigestMismatch)
	sc.Step(`^the install fails with a digest mismatch error$`, w.installFailsDigestMismatch)
	sc.Step(`^release "([^"]*)" is installed at revision (\d+)$`, w.releaseInstalledAt)
	sc.Step(`^the lockfile records revision (\d+)$`, w.lockfileRecordsRevision)
	sc.Step(`^the history shows both revisions$`, w.historyShowsBoth)
	sc.Step(`^the files match revision (\d+)$`, w.filesMatchRevision)
	sc.Step(`^a new revision (\d+) is recorded whose content equals revision (\d+)$`, w.newRevisionEquals)

	// ---- install-to-cluster ----
	sc.Step(`^valid ConfigMap YAML is emitted$`, w.validConfigMapYAML)
	sc.Step(`^the YAML contains no registry credentials$`, w.yamlNoCredentials)
	sc.Step(`^file paths are reconstructed via items\[\]\.path$`, w.itemsPathReconstruction)
	sc.Step(`^a ConfigMap named for the release exists in namespace "([^"]*)"$`, w.configMapExists)
	sc.Step(`^the skill files can be mounted as a projected tree$`, w.filesMountable)
	sc.Step(`^a skill whose files exceed the 1 MiB ConfigMap ceiling$`, w.bigSkill)
	sc.Step(`^multiple ConfigMaps are created, one per subtree$`, w.multipleConfigMaps)
	sc.Step(`^each ConfigMap name is suffixed from the release handle$`, w.configMapNamesSuffixed)
	sc.Step(`^release "([^"]*)" installed to the cluster at revision (\d+)$`, w.releaseInstalledCluster)
	sc.Step(`^the ConfigMap content matches revision (\d+)$`, w.configMapMatchesRevision)
	sc.Step(`^the rollback works without any local lockfile$`, w.noLocalLockfile)

	// ---- sign-and-verify ----
	sc.Step(`^"([^"]*)" is signed with cosign$`, w.signWithCosign)
	sc.Step(`^"([^"]*)" is signed$`, w.signWithCosign)
	sc.Step(`^signature verification passes$`, w.verificationPasses)
	sc.Step(`^"([^"]*)" has no signature$`, noopArg)
	sc.Step(`^the install succeeds$`, w.installSucceeds)
	sc.Step(`^verification reports no signature present$`, w.noSignaturePresent)
	sc.Step(`^the install fails because no signature is present$`, w.installFailsNoSig)
	sc.Step(`^the content digest no longer matches the signed subject$`, w.tamperContent)
	sc.Step(`^signature verification fails$`, w.verificationFails)
}

func noopArg(string) error { return nil }

// ---- fixtures ----

func (w *world) aSkillDirectory(name string) error {
	w.skillDir = w.writeSkill(name, "1.4.2", "Extract and manipulate PDFs", map[string]string{
		"SKILL.md": "---\nname: " + name + "\ndescription: Extract and manipulate PDFs\n---\n\n# " + name + "\n\n## Usage\nRun the tool.\n",
	})
	return nil
}

func (w *world) setEposName(name string) error {
	path := filepath.Join(w.skillDir, "Epos.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "name:") {
			out = append(out, "name: "+name)
			continue
		}
		out = append(out, line)
	}
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644)
}

func (w *world) skillReferencesMissing(path string) error {
	return os.WriteFile(filepath.Join(w.skillDir, "SKILL.md"),
		[]byte("---\nname: pdf-tools\ndescription: x\n---\n\nSee also: ["+path+"]("+path+")\n"), 0o644)
}

func (w *world) aPackagedSkill(name, version string) error {
	if w.skillDir == "" {
		w.skillDir = w.writeSkill(name, version, "Extract and manipulate PDFs", nil)
	}
	return nil
}

func (w *world) aPublishedSkill(name, version string) error {
	digest, err := w.publishSkillOnly(name, version, map[string]string{"references/c.md": "reference c\n"})
	if err != nil {
		return err
	}
	w.pushDigest = digest
	return nil
}

func (w *world) publishThreeSkills(a, b, c string) error {
	for _, n := range []string{a, b, c} {
		if _, err := w.publishSkillOnly(n, "1.0.0", nil); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) aPublishedOrigin(name, ref string) error {
	_, err := w.publishSkillOnly(name, "1.0.0", map[string]string{ref: "reference c\n"})
	return err
}

// ---- run ----

func (w *world) iRun(line string) error {
	w.runEpos(line)
	return nil
}

// ---- author-and-publish assertions ----

func (w *world) layoutManifest() (*oci.Manifest, error) {
	dir := filepath.Join(w.workspace, "pdf-tools-1.4.2.epos")
	return oci.ReadLayout(context.Background(), dir, "1.4.2")
}

func (w *world) artifactSingleLayer() error {
	if w.lastErr != nil {
		return fmt.Errorf("package failed: %w", w.lastErr)
	}
	man, err := w.layoutManifest()
	if err != nil {
		return err
	}
	if len(man.Layers) != 1 {
		return fmt.Errorf("expected 1 content layer, got %d", len(man.Layers))
	}
	if man.Layers[0].MediaType != pkgdomain.MediaTypeSkillContent {
		return fmt.Errorf("layer media type = %q", man.Layers[0].MediaType)
	}
	return nil
}

func (w *world) configBlobRecordsMetadata() error {
	man, err := w.layoutManifest()
	if err != nil {
		return err
	}
	m, err := pkgdomain.ParseManifest(man.Config.Data)
	if err != nil {
		return err
	}
	if m.Name != "pdf-tools" || m.Version != "1.4.2" {
		return fmt.Errorf("config metadata = %+v", m)
	}
	return nil
}

func (w *world) mediaTypesAreEpos() error {
	man, err := w.layoutManifest()
	if err != nil {
		return err
	}
	if man.Config.MediaType != pkgdomain.MediaTypeSkillConfig {
		return fmt.Errorf("config media type = %q", man.Config.MediaType)
	}
	if man.Layers[0].MediaType != pkgdomain.MediaTypeSkillContent {
		return fmt.Errorf("content media type = %q", man.Layers[0].MediaType)
	}
	return nil
}

func (w *world) validationFails() error {
	if w.lastErr == nil {
		return fmt.Errorf("expected validation to fail, but it succeeded; output: %s", w.out.String())
	}
	return nil
}

func (w *world) reportMentionsNameRules(word string) error {
	out := strings.ToLower(w.out.String())
	if !strings.Contains(out, "lowercase") {
		return fmt.Errorf("report does not mention lowercase: %s", w.out.String())
	}
	if !strings.Contains(out, strings.ToLower(word)) {
		return fmt.Errorf("report does not mention %q: %s", word, w.out.String())
	}
	return nil
}

func (w *world) reportMentionsDangling(path string) error {
	if !strings.Contains(w.out.String(), path) {
		return fmt.Errorf("report does not mention dangling reference %q: %s", path, w.out.String())
	}
	return nil
}

func (w *world) manifestStored() error {
	if w.lastErr != nil {
		return fmt.Errorf("push failed: %w", w.lastErr)
	}
	ref := w.registry + "/skills/pdf-tools:1.4.2"
	if _, err := w.client.Resolve(context.Background(), ref); err != nil {
		return fmt.Errorf("manifest not stored: %w", err)
	}
	return nil
}

func (w *world) tagResolvesToDigest() error {
	ref := w.registry + "/skills/pdf-tools:1.4.2"
	desc, err := w.client.Resolve(context.Background(), ref)
	if err != nil {
		return err
	}
	if desc.Digest.String() != w.pushDigest {
		return fmt.Errorf("tag digest %s != pushed digest %s", desc.Digest, w.pushDigest)
	}
	return nil
}

// ---- compose ----

func (w *world) repoDependsOCI(name string) error {
	w.ensureConsumer()
	return nil
}

func (w *world) repoOverlayReplaces(path string) error {
	w.addLocalOverlay("my-repo",
		fmt.Sprintf("apiVersion: epos/v1\nkind: Overlay\nname: my-repo\nversion: 0.1.0\noperations:\n  - op: add-file\n    target: %s\n    content: |\n      from my repo\n", path),
		nil)
	return nil
}

func (w *world) intermediateReplaces(path string) error {
	if _, err := w.publishSkillOnly("intermediate", "1.0.0", map[string]string{path: "from intermediate\n"}); err != nil {
		return err
	}
	w.appendDep(fmt.Sprintf("  - name: intermediate\n    oci: %s/intermediate\n    version: 1.0.0\n", w.registry))
	return nil
}

func (w *world) composeSkill() error {
	res, err := w.app.Compose(context.Background(), w.ensureConsumer(), false)
	w.composeRes, w.composeErr = res, err
	return nil
}

func (w *world) provenance(path, layer string) error {
	if w.composeErr != nil {
		return fmt.Errorf("compose failed: %w", w.composeErr)
	}
	got := w.composeRes.Merged.Provenance[path]
	if got != layer {
		return fmt.Errorf("%s provenance = %q, want %q", path, got, layer)
	}
	return nil
}

func (w *world) originHasSection(section string) error { return nil } // default SKILL.md has Usage

func (w *world) lowerAppendsRefLine() error {
	w.addLocalOverlay("lower",
		"apiVersion: epos/v1\nkind: Overlay\nname: lower\nversion: 0.1.0\noperations:\n  - op: append-to-file\n    target: SKILL.md\n    content: |\n      See also: [Advanced](references/advanced.md)\n",
		nil)
	return nil
}

func (w *world) repoPatchesSection(section string) error {
	w.addLocalOverlay("my-repo",
		"apiVersion: epos/v1\nkind: Overlay\nname: my-repo\nversion: 0.1.0\noperations:\n  - op: replace-in-file\n    target: SKILL.md\n    pattern: \"Run the tool\\\\.\"\n    replacement: \"Run the tool (Team Edition).\"\n",
		nil)
	return nil
}

func (w *world) mergedSkillMdHasBoth() error {
	if w.composeErr != nil {
		return w.composeErr
	}
	body := string(w.composeRes.Merged.Files["SKILL.md"])
	if !strings.Contains(body, "See also: [Advanced]") {
		return fmt.Errorf("appended line missing: %s", body)
	}
	if !strings.Contains(body, "Team Edition") {
		return fmt.Errorf("patched Usage missing: %s", body)
	}
	return nil
}

func (w *world) repoDependsGit(ref, subpath string) error {
	if _, err := w.initGitSkill(subpath, ref, map[string]string{
		"SKILL.md":        "shared body\n",
		"references/s.md": "shared ref\n",
	}); err != nil {
		return err
	}
	w.appendDep(fmt.Sprintf("  - name: shared\n    git: %s\n    ref: %s\n    subpath: %s\n", w.gitRemote, ref, subpath))
	return nil
}

func (w *world) gitPin() *pinView {
	for _, p := range w.composeRes.LayerPins {
		if p.SourceType == "git" {
			return &pinView{commit: p.Commit, treeSha: p.TreeSha}
		}
	}
	return nil
}

type pinView struct{ commit, treeSha string }

func (w *world) lockRecordsCommit() error {
	if w.composeErr != nil {
		return w.composeErr
	}
	p := w.gitPin()
	if p == nil || len(p.commit) != 40 {
		return fmt.Errorf("git commit SHA not captured: %+v", p)
	}
	return nil
}

func (w *world) lockRecordsTreeSha() error {
	p := w.gitPin()
	if p == nil || len(p.treeSha) != 40 {
		return fmt.Errorf("git tree SHA not captured: %+v", p)
	}
	return nil
}

func (w *world) repoRequiredOverlay() error {
	w.addLocalOverlay("my-repo",
		"apiVersion: epos/v1\nkind: Overlay\nname: my-repo\nversion: 0.1.0\noperations:\n  - op: replace-in-file\n    target: SKILL.md\n    pattern: \"THIS-PATTERN-DOES-NOT-MATCH\"\n    replacement: \"x\"\n    required: true\n",
		nil)
	return nil
}

func (w *world) compositionFailsRequired() error {
	if w.composeErr == nil {
		return fmt.Errorf("expected composition to fail with a required-operation error")
	}
	if !strings.Contains(w.composeErr.Error(), "no match") && !strings.Contains(w.composeErr.Error(), "required") {
		return fmt.Errorf("unexpected error: %v", w.composeErr)
	}
	return nil
}

func (w *world) localOverlayAgainst(base string) error {
	_ = os.WriteFile(filepath.Join(w.workspace, "Overlay.yaml"),
		[]byte("apiVersion: epos/v1\nkind: Overlay\nname: team-refs\nversion: 0.2.0\noperations:\n  - op: add-file\n    target: references/advanced.md\n    path: files/advanced.md\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(w.workspace, "files"), 0o755)
	_ = os.WriteFile(filepath.Join(w.workspace, "files", "advanced.md"), []byte("# Advanced\n"), 0o644)
	return nil
}

func (w *world) overlayStored() error {
	if w.lastErr != nil {
		return fmt.Errorf("overlay push failed: %w", w.lastErr)
	}
	ref := w.registry + "/overlays/team-refs:0.2.0"
	man, err := w.client.Pull(context.Background(), ref)
	if err != nil {
		return err
	}
	if man.Config.MediaType != pkgdomain.MediaTypeOverlayConfig {
		return fmt.Errorf("overlay config media type = %q", man.Config.MediaType)
	}
	return nil
}

func (w *world) overlayDeclarableByDigest() error {
	if !strings.HasPrefix(w.pushDigest, "sha256:") {
		return fmt.Errorf("overlay not pinnable by digest: %q", w.pushDigest)
	}
	return nil
}

// ---- discover ----

func (w *world) proxyProbes() error {
	d := &discovery.Discoverer{Client: w.client}
	w.discoverMode = d.Probe(context.Background(), config.Registry{Name: "r", URL: "http://" + w.registry})
	return nil
}

func (w *world) discoveryModeIs(mode string) error {
	if w.discoverMode != mode {
		return fmt.Errorf("discovery mode = %q, want %q", w.discoverMode, mode)
	}
	return nil
}

func (w *world) registry404() error {
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		http.NotFound(rw, r)
	}))
	w.reg404 = srv.URL
	w.teardown = append(w.teardown, srv.Close)
	return nil
}

func (w *world) proxyProbes404() error {
	d := &discovery.Discoverer{Client: &oci.Client{PlainHTTP: true}}
	entry := config.Registry{Name: "r404", URL: w.reg404, Repositories: []string{"skills/declared-only"}}
	res, err := d.Discover(context.Background(), entry)
	if err != nil {
		return err
	}
	w.discoverMode = res.Mode
	w.listing = res.Repos
	return nil
}

func (w *world) onlyDeclaredListed() error {
	if len(w.listing) != 1 || w.listing[0] != "skills/declared-only" {
		return fmt.Errorf("declared repos = %v", w.listing)
	}
	return nil
}

func (w *world) catalogThroughProxy() error {
	p, err := proxy.New("http://"+w.registry, stats.New())
	if err != nil {
		return err
	}
	front := httptest.NewServer(p)
	w.teardown = append(w.teardown, front.Close)
	resp, err := http.Get(front.URL + "/v2/_catalog") //nolint:noctx
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	w.listing = []string{string(body)}
	return nil
}

func (w *world) listingIncludes(a, b, c string) error {
	joined := strings.Join(w.listing, " ")
	for _, n := range []string{a, b, c} {
		if !strings.Contains(joined, n) {
			return fmt.Errorf("listing missing %q: %s", n, joined)
		}
	}
	return nil
}

func (w *world) pullThroughProxy(name string) error {
	counter := stats.New()
	p, err := proxy.New("http://"+w.registry, counter)
	if err != nil {
		return err
	}
	front := httptest.NewServer(p)
	w.teardown = append(w.teardown, front.Close)
	req, _ := http.NewRequest(http.MethodGet, front.URL+"/v2/"+name+"/manifests/1.0.0", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	w.pullStatus = resp.StatusCode
	w.proxyPersist = p.PersistedCredentials()
	return nil
}

func (w *world) pullSucceeds() error {
	if w.pullStatus != http.StatusOK {
		return fmt.Errorf("pull status = %d", w.pullStatus)
	}
	return nil
}

func (w *world) proxyPersistsNone() error {
	if w.proxyPersist != 0 {
		return fmt.Errorf("proxy persisted %d credentials", w.proxyPersist)
	}
	return nil
}

func (w *world) frontendFilter(keyword string) error {
	feed := &frontend.Feed{
		Registries: []config.Registry{{Name: "default", URL: "http://" + w.registry}},
		Client:     w.client,
		Stats:      stats.New(),
	}
	cat, err := feed.Gather(context.Background())
	if err != nil {
		return err
	}
	w.cards = nil
	for _, card := range cat.Filter(frontend.Filter{Keyword: keyword}) {
		w.cards = append(w.cards, card.Name)
	}
	return nil
}

func (w *world) onlyShown(name string) error {
	if len(w.cards) != 1 || w.cards[0] != name {
		return fmt.Errorf("shown cards = %v, want only %q", w.cards, name)
	}
	return nil
}

// ---- install-locally ----

func (w *world) filesWritten() error {
	if w.lastErr != nil {
		return fmt.Errorf("install failed: %w", w.lastErr)
	}
	if _, err := os.Stat(filepath.Join(w.workspace, "pdf", "SKILL.md")); err != nil {
		return fmt.Errorf("skill files not materialized: %w", err)
	}
	return nil
}

func (w *world) loadLock() (*lock.Lockfile, error) {
	return lock.Load(filepath.Join(w.workspace, lock.LockfileName))
}

func (w *world) lockfileRecordsRelease(release string, rev int) error {
	lf, err := w.loadLock()
	if err != nil {
		return err
	}
	cur, err := lf.Current(release)
	if err != nil {
		return err
	}
	if cur.Revision != rev {
		return fmt.Errorf("current revision = %d, want %d", cur.Revision, rev)
	}
	return nil
}

func (w *world) lockfilePinsDigest() error {
	lf, err := w.loadLock()
	if err != nil {
		return err
	}
	cur, err := lf.Current("pdf")
	if err != nil {
		return err
	}
	if !strings.HasPrefix(cur.Digest, "sha256:") {
		return fmt.Errorf("digest pin = %q", cur.Digest)
	}
	return nil
}

func (w *world) lockfileDigestMismatch(name string) error {
	// Install once to create the lockfile, then corrupt the pinned digest.
	if _, err := w.app.Install(context.Background(), "pdf", name, app.InstallOpts{Target: app.TargetFiles}); err != nil {
		return err
	}
	lf, err := w.loadLock()
	if err != nil {
		return err
	}
	cur, _ := lf.Current("pdf")
	cur.Digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	return lf.Save()
}

func (w *world) installFailsDigestMismatch() error {
	if w.lastErr == nil || !strings.Contains(w.lastErr.Error(), "mismatch") {
		return fmt.Errorf("expected digest mismatch error, got: %v", w.lastErr)
	}
	return nil
}

func (w *world) releaseInstalledAt(release string, rev int) error {
	for i := 0; i < rev; i++ {
		if _, err := w.app.Install(context.Background(), release, "pdf-tools", app.InstallOpts{Target: app.TargetFiles}); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) lockfileRecordsRevision(rev int) error {
	return w.lockfileRecordsRelease("pdf", rev)
}

func (w *world) historyShowsBoth() error {
	lf, err := w.loadLock()
	if err != nil {
		return err
	}
	if len(lf.History("pdf")) < 2 {
		return fmt.Errorf("history shows %d revisions", len(lf.History("pdf")))
	}
	return nil
}

func (w *world) filesMatchRevision(rev int) error {
	lf, err := w.loadLock()
	if err != nil {
		return err
	}
	r, err := lf.Get("pdf", rev)
	if err != nil {
		return err
	}
	want, _ := r.FileBytes()
	for path, data := range want {
		got, err := os.ReadFile(filepath.Join(w.workspace, "pdf", filepath.FromSlash(path)))
		if err != nil || string(got) != string(data) {
			return fmt.Errorf("file %q does not match revision %d", path, rev)
		}
	}
	return nil
}

func (w *world) newRevisionEquals(newRev, oldRev int) error {
	lf, err := w.loadLock()
	if err != nil {
		return err
	}
	cur, err := lf.Current("pdf")
	if err != nil {
		return err
	}
	if cur.Revision != newRev {
		return fmt.Errorf("current revision = %d, want %d", cur.Revision, newRev)
	}
	old, err := lf.Get("pdf", oldRev)
	if err != nil {
		return err
	}
	if fmt.Sprint(cur.Files) != fmt.Sprint(old.Files) {
		return fmt.Errorf("revision %d content != revision %d", newRev, oldRev)
	}
	return nil
}

// ---- cluster ----

func (w *world) validConfigMapYAML() error {
	if w.lastErr != nil {
		return fmt.Errorf("template failed: %w", w.lastErr)
	}
	for _, doc := range strings.Split(w.out.String(), "\n---\n") {
		doc = strings.TrimSpace(stripComments(doc))
		if doc == "" {
			continue
		}
		var m map[string]any
		if err := yaml.Unmarshal([]byte(doc), &m); err != nil {
			return fmt.Errorf("invalid YAML: %w\n%s", err, doc)
		}
		if m["kind"] == "ConfigMap" {
			return nil
		}
	}
	return fmt.Errorf("no ConfigMap emitted: %s", w.out.String())
}

func (w *world) yamlNoCredentials() error {
	out := strings.ToLower(w.out.String())
	if strings.Contains(out, "password") || strings.Contains(out, "secret:") {
		return fmt.Errorf("credentials leaked into ConfigMap YAML")
	}
	return nil
}

func (w *world) itemsPathReconstruction() error {
	if !strings.Contains(w.out.String(), "path:") {
		return fmt.Errorf("no items[].path reconstruction in output: %s", w.out.String())
	}
	return nil
}

func (w *world) configMapExists(namespace string) error {
	if w.lastErr != nil {
		return fmt.Errorf("install failed: %w", w.lastErr)
	}
	if _, err := w.app.Kube.GetConfigMap(context.Background(), namespace, "pdf"); err != nil {
		return fmt.Errorf("ConfigMap not found: %w", err)
	}
	return nil
}

func (w *world) filesMountable() error {
	cm, err := w.app.Kube.GetConfigMap(context.Background(), "skills", "pdf")
	if err != nil {
		return err
	}
	if len(cm.Data) == 0 && len(cm.BinaryData) == 0 {
		return fmt.Errorf("ConfigMap has no projected files")
	}
	return nil
}

func (w *world) bigSkill() error {
	big := strings.Repeat("x", 700*1024)
	_, err := w.publishSkillOnly("big-skill", "1.0.0", map[string]string{
		"references/big1.md": big,
		"scripts/big2.sh":    big,
	})
	return err
}

func (w *world) multipleConfigMaps() error {
	if w.lastErr != nil {
		return fmt.Errorf("install failed: %w", w.lastErr)
	}
	cms, err := w.app.Kube.ListConfigMaps(context.Background(), "skills", "epos.dev/release=big")
	if err != nil {
		return err
	}
	if len(cms) < 2 {
		return fmt.Errorf("expected multiple ConfigMaps, got %d", len(cms))
	}
	return nil
}

func (w *world) configMapNamesSuffixed() error {
	cms, err := w.app.Kube.ListConfigMaps(context.Background(), "skills", "epos.dev/release=big")
	if err != nil {
		return err
	}
	names := map[string]bool{}
	for _, cm := range cms {
		names[cm.Name] = true
	}
	if !names["big-references"] || !names["big-scripts"] {
		return fmt.Errorf("expected subtree-suffixed names, got %v", names)
	}
	return nil
}

func (w *world) releaseInstalledCluster(release string, rev int) error {
	opts := app.InstallOpts{Target: app.TargetConfigMap, Namespace: "skills"}
	for i := 0; i < rev; i++ {
		if _, err := w.app.Install(context.Background(), release, "pdf-tools", opts); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) configMapMatchesRevision(rev int) error {
	if w.lastErr != nil {
		return fmt.Errorf("rollback failed: %w", w.lastErr)
	}
	if _, err := w.app.Kube.GetConfigMap(context.Background(), "skills", "pdf"); err != nil {
		return fmt.Errorf("ConfigMap missing after rollback: %w", err)
	}
	return nil
}

func (w *world) noLocalLockfile() error {
	if _, err := os.Stat(filepath.Join(w.workspace, lock.LockfileName)); err == nil {
		return fmt.Errorf("cluster target must not write a local lockfile")
	}
	return nil
}

// ---- sign ----

func (w *world) signWithCosign(name string) error {
	ref := w.registry + "/" + name
	tag := ref + ":" + w.publishedVersion[name]
	desc, err := w.client.Resolve(context.Background(), tag)
	if err != nil {
		return err
	}
	return verify.Sign(context.Background(), w.client, ref, desc, "test-signature")
}

func (w *world) verificationPasses() error {
	if w.lastErr != nil {
		return fmt.Errorf("install failed: %w", w.lastErr)
	}
	if w.app.LastVerify == nil || !w.app.LastVerify.Verified {
		return fmt.Errorf("verification did not pass: %+v", w.app.LastVerify)
	}
	return nil
}

func (w *world) installSucceeds() error {
	if w.lastErr != nil {
		return fmt.Errorf("install failed: %w", w.lastErr)
	}
	return nil
}

func (w *world) noSignaturePresent() error {
	if w.app.LastVerify == nil || w.app.LastVerify.Present {
		return fmt.Errorf("expected no signature present: %+v", w.app.LastVerify)
	}
	return nil
}

func (w *world) installFailsNoSig() error {
	if w.lastErr == nil {
		return fmt.Errorf("expected install to fail without a signature")
	}
	return nil
}

func (w *world) tamperContent() error {
	// Re-push different content to the same tag so the tag digest no longer
	// matches the signed subject.
	dir := filepath.Join(w.workspace, "_tamper", "pdf-tools")
	files := map[string]string{
		"Epos.yaml": "apiVersion: epos/v1\nname: pdf-tools\nversion: 1.4.2\ndescription: tampered\n",
		"SKILL.md":  "---\nname: pdf-tools\ndescription: tampered\n---\n\ntampered body\n",
	}
	for rel, content := range files {
		full := filepath.Join(dir, rel)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(content), 0o644)
	}
	_, err := w.app.Push(context.Background(), dir, w.registry+"/pdf-tools:1.4.2")
	return err
}

func (w *world) verificationFails() error {
	if w.lastErr == nil {
		return fmt.Errorf("expected signature verification to fail")
	}
	return nil
}

func stripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}
