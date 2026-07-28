//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry/remote"
)

// The remaining media types of the Agent Skills OCI Artifacts spec (SPEC.md
// 2.1); the artifact type itself is skillArtifactType, in steps_read_surface.go.
//
// Spelled out rather than imported from internal/artifact. These suites drive
// the CLI as a user does, and what goes on the wire is the promise being
// checked — a constant shared with the code under test could only prove it
// agrees with itself.
const (
	skillConfigType  = "application/vnd.agentskills.skill.config.v1+json"
	skillContentType = "application/vnd.agentskills.skill.content.v1.tar+gzip"
)

// registryBuilder drives `epos build` against a real registry: a real binary, a
// real store directory, a real zot. Nothing here reaches into the packages
// under test.
type registryBuilder struct {
	bin string
	// registryURL is host:port, without a scheme, which is how an OCI reference
	// spells a registry.
	registryURL string

	// eposHome is EPOS_HOME, so the CLI's store lands in the sandbox. Nothing
	// here moves HOME.
	eposHome string
	dir      string // the build context

	// base is the FROM reference as written, baseRepo the <registry>/<repo>
	// part of it, and baseTag the tag it names.
	base     string
	baseRepo string
	baseTag  string
	// baseDigest is the manifest digest the harness pushed, most recently.
	baseDigest string
	// awkward are the paths of 2.5's "not validated, therefore accepted" list
	// that the base was seeded with.
	awkward []string

	tag      string
	stdout   string
	stderr   string
	buildErr error

	// One entry per successful build, in order.
	digests   []string
	manifests []ocispec.Manifest
	layers    []map[string]string

	published      string
	pulledDigest   string
	pulledManifest ocispec.Manifest
}

func (r *registryBuilder) reset(t *testing.T) {
	root := t.TempDir()
	r.eposHome = filepath.Join(root, "epos")
	r.dir = filepath.Join(root, "context")
	for _, d := range []string{r.eposHome, r.dir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r.base, r.baseRepo, r.baseTag, r.baseDigest = "", "", "", ""
	r.awkward = nil
	r.tag = "reviewer:2.0.0"
	r.stdout, r.stderr, r.buildErr = "", "", nil
	r.digests, r.manifests, r.layers = nil, nil, nil
	r.published, r.pulledDigest = "", ""
	r.pulledManifest = ocispec.Manifest{}
}

// epos runs the CLI with stdout and stderr kept apart: the digest goes to one
// and the pins go to the other.
func (r *registryBuilder) epos(args ...string) (string, string, error) {
	cmd := exec.Command(r.bin, args...)
	cmd.Env = append(os.Environ(), eposHomeEnv+"="+r.eposHome)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

func (r *registryBuilder) write(name, body string) error {
	full := filepath.Join(r.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(body), 0o600)
}

// --- Given ------------------------------------------------------------------

// baseSkillFiles is the skill a base repository holds, as a client extracting
// it would see it, minus the `<skill-name>/` root.
func baseSkillFiles(version, style string) map[string]string {
	return map[string]string{
		"SKILL.md": "---\nname: pdf\nversion: " + version +
			"\ndescription: reads PDFs\n---\n\n# PDF\n",
		"references/style.md": style,
		"extra.md":            "upstream extras\n",
	}
}

func (r *registryBuilder) aRegistry(ctx context.Context, t *testing.T) error {
	r.registryURL = sharedZot(ctx, t)
	return nil
}

// aBaseSkill publishes a base into a repository of its own.
//
// A repository per scenario, so the scenarios share one container without
// sharing state: the moved-tag scenario re-pushes over its own tag, and nothing
// else is looking at it.
func (r *registryBuilder) aBaseSkill(ctx context.Context, files map[string]string) error {
	r.baseRepo = fmt.Sprintf("%s/demo/agent-skills/pdf%d",
		r.registryURL, baseCounter.Add(1))
	r.baseTag = "1.2.0"

	digest, err := publishBaseSkill(ctx, r.baseRepo, r.baseTag, "pdf", files)
	if err != nil {
		return err
	}
	r.baseDigest = digest
	r.base = r.baseRepo + ":" + r.baseTag
	return nil
}

func (r *registryBuilder) anOrdinaryBaseSkill(ctx context.Context) error {
	return r.aBaseSkill(ctx, baseSkillFiles("1.2.0", "House style.\n"))
}

// anAwkwardBaseSkill is 2.5's "not validated, therefore accepted" list, in a
// base a consumer deriving from it has no way to fix. Every one of these is
// legal on Linux and is accepted by git and by oras.
func (r *registryBuilder) anAwkwardBaseSkill(ctx context.Context) error {
	r.awkward = []string{
		"aux.md",
		"references/con",
		"references/a:b.md",
		`references/back\slash.md`,
		"references/trailing.",
		"references/README.md",
		"references/readme.md",
	}

	files := baseSkillFiles("1.2.0", "House style.\n")
	for _, p := range r.awkward {
		files[p] = "content of " + p + "\n"
	}
	return r.aBaseSkill(ctx, files)
}

// anEscapingBaseSkill is the other half of 2.5, which is a security rule and
// not a portability one: an entry name that leaves the skill root.
func (r *registryBuilder) anEscapingBaseSkill(ctx context.Context) error {
	files := baseSkillFiles("1.2.0", "House style.\n")
	files["../../etc/passwd"] = "root:x:0:0\n"
	return r.aBaseSkill(ctx, files)
}

// aSkillfileDeriving is the ordinary case: one OCI base, edited into a skill of
// its own.
func (r *registryBuilder) aSkillfileDeriving(name string) error {
	r.tag = name + ":2.0.0"
	if err := r.write("notes.md", "in-house notes\n"); err != nil {
		return err
	}
	return r.writeSkillfile(r.base, name)
}

func (r *registryBuilder) writeSkillfile(from, name string) error {
	return r.write("Skillfile", fmt.Sprintf(`FROM %s
SET name %s
SET version 2.0.0
SET description "reviews code"
RM extra.md
COPY notes.md references/notes.md
`, from, name))
}

// --- When -------------------------------------------------------------------

func (r *registryBuilder) builds() error {
	r.stdout, r.stderr, r.buildErr = r.epos("build", "--plain-http", r.dir)
	if r.buildErr != nil {
		return nil // scenarios assert on buildErr
	}

	r.digests = append(r.digests, lastField(r.stdout))
	m, err := r.manifest()
	if err != nil {
		return err
	}
	r.manifests = append(r.manifests, m)

	layer, err := r.layer(m)
	if err != nil {
		return err
	}
	r.layers = append(r.layers, layer)
	return nil
}

// theTagMoves re-pushes different content over the base's tag, which is what
// makes a tag not a pin (8.3).
//
// The content the tag is about to leave behind is given a tag of its own first.
// zot removes a manifest no tag references any more, and a registry's retention
// policy is not what this scenario is about: the claim under test is that the
// digest Epos recorded still names the bytes the tag used to point at, not that
// every registry keeps them forever.
func (r *registryBuilder) theTagMoves(ctx context.Context) error {
	repo, err := plainRepository(r.baseRepo)
	if err != nil {
		return err
	}
	previous, err := repo.Resolve(ctx, r.baseTag)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", r.base, err)
	}
	if err := repo.Tag(ctx, previous, "superseded"); err != nil {
		return fmt.Errorf("keep %s reachable: %w", previous.Digest, err)
	}

	digest, err := publishBaseSkill(ctx, r.baseRepo, r.baseTag, "pdf",
		baseSkillFiles("1.3.0", "House style, revised.\n"))
	if err != nil {
		return err
	}
	if digest == r.baseDigest {
		return fmt.Errorf("the moved tag resolves to the same digest, so nothing moved")
	}
	r.baseDigest = digest
	return nil
}

// buildsFromTheRecordedDigest rewrites the Skillfile to name the pin the first
// build recorded, which is the whole point of recording one.
func (r *registryBuilder) buildsFromTheRecordedDigest() error {
	if len(r.manifests) == 0 {
		return fmt.Errorf("nothing has been built yet")
	}
	pinned := r.manifests[0].Annotations[ocispec.AnnotationBaseImageDigest]
	if pinned == "" {
		return fmt.Errorf("the first build recorded no base digest")
	}
	if err := r.writeSkillfile(r.baseRepo+"@"+pinned, "reviewer"); err != nil {
		return err
	}
	return r.builds()
}

// publishedWithPlainOras copies the built artifact out of the local store with
// a stock client. `epos push` does not exist — the write path is withdrawn
// (4.5) — and a skill reaches a registry through whatever client already holds
// the user's credentials.
func (r *registryBuilder) publishedWithPlainOras(ctx context.Context) error {
	src, err := oci.New(storeDir(r.eposHome))
	if err != nil {
		return fmt.Errorf("open the author's store: %w", err)
	}
	repo, err := plainRepository(r.registryURL + "/demo/agent-skills/reviewer")
	if err != nil {
		return err
	}

	if _, err := oras.Copy(ctx, src, r.tag, repo, "2.0.0", oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("publish %s: %w", r.tag, err)
	}
	r.published = r.registryURL + "/demo/agent-skills/reviewer:2.0.0"
	return nil
}

// plainOrasPullsItBack fetches with a client that has never heard of Epos, and
// reads the manifest it got back (2.1).
func (r *registryBuilder) plainOrasPullsItBack(ctx context.Context) error {
	if r.published == "" {
		return fmt.Errorf("nothing was published")
	}
	repo, err := plainRepository(r.registryURL + "/demo/agent-skills/reviewer")
	if err != nil {
		return err
	}

	dst := memory.New()
	desc, err := oras.Copy(ctx, repo, "2.0.0", dst, "2.0.0", oras.DefaultCopyOptions)
	if err != nil {
		return fmt.Errorf("plain oras pull: %w", err)
	}
	body, err := content.FetchAll(ctx, dst, desc)
	if err != nil {
		return fmt.Errorf("read the pulled manifest: %w", err)
	}

	r.pulledDigest = desc.Digest.String()
	return json.Unmarshal(body, &r.pulledManifest)
}

// --- Then -------------------------------------------------------------------

func (r *registryBuilder) buildSucceeds() error {
	if r.buildErr != nil {
		return fmt.Errorf("build failed: %v\nstdout: %s\nstderr: %s",
			r.buildErr, r.stdout, r.stderr)
	}
	return nil
}

func (r *registryBuilder) storeHoldsTag(tag string) error {
	out, _, err := r.epos("store", "ls")
	if err != nil {
		return fmt.Errorf("store ls: %v: %s", err, out)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if strings.TrimSpace(line) == tag {
			return nil
		}
	}
	return fmt.Errorf("store holds %q, want %s", out, tag)
}

func (r *registryBuilder) exactlyOneContentLayer() error {
	m, err := r.lastManifest()
	if err != nil {
		return err
	}
	if len(m.Layers) != 1 {
		return fmt.Errorf("manifest has %d layers, want exactly 1", len(m.Layers))
	}
	if m.Layers[0].MediaType != skillContentType {
		return fmt.Errorf("layer media type = %q", m.Layers[0].MediaType)
	}
	return nil
}

func (r *registryBuilder) layerHolds(path, want string) error {
	files, err := r.lastLayer()
	if err != nil {
		return err
	}
	body, ok := files[path]
	if !ok {
		return fmt.Errorf("the layer holds %v, not %s", sortedKeys(files), path)
	}
	if !strings.Contains(body, want) {
		return fmt.Errorf("%s does not contain %q:\n%s", path, want, body)
	}
	return nil
}

func (r *registryBuilder) layerDoesNotHold(path string) error {
	files, err := r.lastLayer()
	if err != nil {
		return err
	}
	if _, ok := files[path]; ok {
		return fmt.Errorf("%s is in the layer; the Skillfile did not ask for it", path)
	}
	return nil
}

// reportsTheDigest checks the pin against the digest the harness pushed, not
// against whatever the build claimed — otherwise the assertion is only that the
// resolver agrees with itself.
func (r *registryBuilder) reportsTheDigest() error {
	for _, want := range []string{r.base, "digest " + r.baseDigest} {
		if !strings.Contains(r.stderr, want) {
			return fmt.Errorf("the build did not report %q:\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.stdout, r.baseDigest) {
		return fmt.Errorf("the pin reached stdout, which carries only the built digest:\n%s",
			r.stdout)
	}
	return nil
}

// recordsProvenance is SPEC.md 2.3's table, checked annotation by annotation.
func (r *registryBuilder) recordsProvenance() error {
	m, err := r.lastManifest()
	if err != nil {
		return err
	}
	if got := m.Annotations[ocispec.AnnotationBaseImageName]; got != r.base {
		return fmt.Errorf("%s = %q, want the reference as written, %q",
			ocispec.AnnotationBaseImageName, got, r.base)
	}
	if got := m.Annotations[ocispec.AnnotationBaseImageDigest]; got != r.baseDigest {
		return fmt.Errorf("%s = %q, want the resolved manifest digest, %q",
			ocispec.AnnotationBaseImageDigest, got, r.baseDigest)
	}
	if got := m.Annotations["dev.epos.skillfile.digest"]; !strings.HasPrefix(got, "sha256:") {
		return fmt.Errorf("the Skillfile digest annotation is %q", got)
	}
	return nil
}

// recordedDigestIsTheRegistrys resolves the tag independently, so the pin is
// checked against the registry rather than against the build.
func (r *registryBuilder) recordedDigestIsTheRegistrys(ctx context.Context) error {
	m, err := r.lastManifest()
	if err != nil {
		return err
	}
	repo, err := plainRepository(r.baseRepo)
	if err != nil {
		return err
	}
	desc, err := repo.Resolve(ctx, r.baseTag)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", r.base, err)
	}
	if got := m.Annotations[ocispec.AnnotationBaseImageDigest]; got != desc.Digest.String() {
		return fmt.Errorf("the build recorded %q; the registry answers %q for %s",
			got, desc.Digest, r.base)
	}
	return nil
}

func (r *registryBuilder) recordedBaseDigests() ([]string, error) {
	if len(r.manifests) < 2 {
		return nil, fmt.Errorf("expected 2 builds, got %d", len(r.manifests))
	}
	out := make([]string, 0, len(r.manifests))
	for _, m := range r.manifests {
		out = append(out, m.Annotations[ocispec.AnnotationBaseImageDigest])
	}
	return out, nil
}

func (r *registryBuilder) twoBuildsDisagreeOnTheBase() error {
	got, err := r.recordedBaseDigests()
	if err != nil {
		return err
	}
	if got[0] == got[1] {
		return fmt.Errorf("both builds recorded %s; the tag was moved between them", got[0])
	}
	return nil
}

func (r *registryBuilder) secondBuildRecordsTheFirstsBase() error {
	got, err := r.recordedBaseDigests()
	if err != nil {
		return err
	}
	if got[0] != got[1] {
		return fmt.Errorf("the rebuild from the pin recorded %s, want %s", got[1], got[0])
	}
	return nil
}

func (r *registryBuilder) bothBuildsProduceTheSameLayer() error {
	if len(r.layers) < 2 {
		return fmt.Errorf("expected 2 builds, got %d", len(r.layers))
	}
	if first, second := renderLayer(r.layers[0]), renderLayer(r.layers[1]); first != second {
		return fmt.Errorf("the rebuild from the pin produced a different tree:\n%s\nvs\n%s",
			first, second)
	}
	return nil
}

// declaresNoEposMediaType is SPEC.md 2.2: the vnd.epos.* namespace is reserved
// and v2.0 defines nothing in it. A derived artifact that introduced one would
// be unreadable by the conforming clients 2.1 promises.
func (r *registryBuilder) declaresNoEposMediaType() error {
	m, err := r.lastManifest()
	if err != nil {
		return err
	}
	types := []string{m.MediaType, m.ArtifactType, m.Config.MediaType}
	for _, l := range m.Layers {
		types = append(types, l.MediaType)
	}
	for _, mt := range types {
		if strings.Contains(mt, "vnd.epos") {
			return fmt.Errorf("the artifact declares %q; 2.2 defines no vnd.epos.* type in v2.0", mt)
		}
	}
	return nil
}

// carriesTheAgentSkillsTypes is SPEC.md 8.1: a consumer cannot tell a derived
// artifact from a hand-packed one, except by reading the annotations.
func (r *registryBuilder) carriesTheAgentSkillsTypes() error {
	m, err := r.lastManifest()
	if err != nil {
		return err
	}
	for _, want := range []struct{ name, got, expect string }{
		{"manifest media type", m.MediaType, ocispec.MediaTypeImageManifest},
		{"artifact type", m.ArtifactType, skillArtifactType},
		{"config media type", m.Config.MediaType, skillConfigType},
	} {
		if want.got != want.expect {
			return fmt.Errorf("%s = %q, want %q", want.name, want.got, want.expect)
		}
	}
	return r.exactlyOneContentLayer()
}

func (r *registryBuilder) layerHoldsEveryAwkwardPath() error {
	files, err := r.lastLayer()
	if err != nil {
		return err
	}
	if len(r.awkward) == 0 {
		return fmt.Errorf("the base carried no awkward paths, so nothing was proven")
	}
	for _, p := range r.awkward {
		entry := "reviewer/" + p
		body, ok := files[entry]
		if !ok {
			return fmt.Errorf("%s did not survive the build; 2.5 must not reject it", entry)
		}
		if want := "content of " + p + "\n"; body != want {
			return fmt.Errorf("%s = %q, want %q", entry, body, want)
		}
	}
	return nil
}

func (r *registryBuilder) buildFailsOnAnEscapingBase() error {
	if r.buildErr == nil {
		return fmt.Errorf("the build succeeded; 2.5 rejects a path that escapes the skill root")
	}
	if !strings.Contains(r.stderr, "escapes the skill root") {
		return fmt.Errorf("the build failed for another reason:\n%s", r.stderr)
	}
	return nil
}

func (r *registryBuilder) pulledDigestMatchesTheBuild() error {
	if len(r.digests) == 0 {
		return fmt.Errorf("nothing was built")
	}
	if r.pulledDigest != r.digests[0] {
		return fmt.Errorf("plain oras pulled %s, the build reported %s",
			r.pulledDigest, r.digests[0])
	}
	return nil
}

// pulledManifestCarriesProvenance is the gate: what a standard tool reads back
// off the registry still says where the skill came from (2.3).
func (r *registryBuilder) pulledManifestCarriesProvenance() error {
	for _, key := range []string{
		ocispec.AnnotationBaseImageName,
		ocispec.AnnotationBaseImageDigest,
		"dev.epos.skillfile.digest",
	} {
		if r.pulledManifest.Annotations[key] == "" {
			return fmt.Errorf("the pulled manifest carries no %s", key)
		}
	}
	return nil
}

func (r *registryBuilder) pulledManifestNamesTheBase() error {
	if got := r.pulledManifest.Annotations[ocispec.AnnotationBaseImageName]; got != r.base {
		return fmt.Errorf("%s = %q, want %q", ocispec.AnnotationBaseImageName, got, r.base)
	}
	if got := r.pulledManifest.Annotations[ocispec.AnnotationBaseImageDigest]; got != r.baseDigest {
		return fmt.Errorf("%s = %q, want %q", ocispec.AnnotationBaseImageDigest, got, r.baseDigest)
	}
	return nil
}

// --- store helpers ----------------------------------------------------------

func (r *registryBuilder) manifest() (ocispec.Manifest, error) {
	var m ocispec.Manifest
	st, err := oci.New(storeDir(r.eposHome))
	if err != nil {
		return m, err
	}
	ctx := context.Background()
	desc, err := st.Resolve(ctx, r.tag)
	if err != nil {
		return m, fmt.Errorf("resolve %s: %w", r.tag, err)
	}
	body, err := content.FetchAll(ctx, st, desc)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(body, &m)
}

func (r *registryBuilder) lastManifest() (ocispec.Manifest, error) {
	if len(r.manifests) == 0 {
		return ocispec.Manifest{}, fmt.Errorf("nothing was built")
	}
	return r.manifests[len(r.manifests)-1], nil
}

func (r *registryBuilder) lastLayer() (map[string]string, error) {
	if len(r.layers) == 0 {
		return nil, fmt.Errorf("nothing was built")
	}
	return r.layers[len(r.layers)-1], nil
}

// layer reads the content layer back as path → contents, which is what a
// conforming client extracting the artifact sees.
func (r *registryBuilder) layer(m ocispec.Manifest) (map[string]string, error) {
	if len(m.Layers) != 1 {
		return nil, fmt.Errorf("manifest has %d layers, want exactly 1", len(m.Layers))
	}
	st, err := oci.New(storeDir(r.eposHome))
	if err != nil {
		return nil, err
	}
	packed, err := content.FetchAll(context.Background(), st, m.Layers[0])
	if err != nil {
		return nil, err
	}
	return readLayer(packed)
}

func readLayer(packed []byte) (map[string]string, error) {
	gr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	out := map[string]string{}
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		out[h.Name] = string(body)
	}
}

// renderLayer writes a file map out in a fixed order, so two of them can be
// compared without Go's map iteration order deciding the answer.
func renderLayer(files map[string]string) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out strings.Builder
	for _, p := range paths {
		fmt.Fprintf(&out, "%s\n%s\n", p, files[p])
	}
	return out.String()
}

// --- the base fixture -------------------------------------------------------

// baseCounter gives every scenario a base repository of its own, so one shared
// registry does not become shared state.
var baseCounter atomic.Int64

func plainRepository(ref string) (*remote.Repository, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return nil, fmt.Errorf("repository %q: %w", ref, err)
	}
	repo.PlainHTTP = true
	return repo, nil
}

// publishBaseSkill puts a conformant skill artifact in the registry and returns
// its manifest digest.
//
// Assembled here rather than through internal/artifact, and pushed with
// oras-go: a FROM names somebody else's skill, and a base built by the code
// under test could only show that Epos reads what Epos writes. This is the
// third party.
func publishBaseSkill(ctx context.Context, ref, tag, name string,
	files map[string]string) (string, error) {
	layer, err := baseLayer(name, files)
	if err != nil {
		return "", err
	}

	src := memory.New()
	layerDesc, err := pushBytes(ctx, src, skillContentType, layer)
	if err != nil {
		return "", err
	}

	configJSON, err := json.Marshal(map[string]string{
		"name": name, "version": "1.2.0", "description": "reads PDFs",
	})
	if err != nil {
		return "", err
	}
	configDesc, err := pushBytes(ctx, src, skillConfigType, configJSON)
	if err != nil {
		return "", err
	}

	body, err := json.Marshal(ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: skillArtifactType,
		Config:       configDesc,
		Layers:       []ocispec.Descriptor{layerDesc},
		Annotations:  map[string]string{ocispec.AnnotationTitle: name},
	})
	if err != nil {
		return "", err
	}
	manifestDesc, err := pushBytes(ctx, src, ocispec.MediaTypeImageManifest, body)
	if err != nil {
		return "", err
	}
	if err := src.Tag(ctx, manifestDesc, tag); err != nil {
		return "", err
	}

	repo, err := plainRepository(ref)
	if err != nil {
		return "", err
	}
	desc, err := oras.Copy(ctx, src, tag, repo, tag, oras.DefaultCopyOptions)
	if err != nil {
		return "", fmt.Errorf("publish the base %s:%s: %w", ref, tag, err)
	}
	return desc.Digest.String(), nil
}

func pushBytes(ctx context.Context, target content.Storage,
	mediaType string, data []byte) (ocispec.Descriptor, error) {
	desc := content.NewDescriptorFromBytes(mediaType, data)
	if err := target.Push(ctx, desc, bytes.NewReader(data)); err != nil {
		return ocispec.Descriptor{}, err
	}
	return desc, nil
}

// baseLayer renders files as the tar+gzip a skill artifact's content layer is:
// rooted at `<name>/`, forward slashes throughout (2.1, 2.5).
//
// Entry names are taken verbatim. The whole point of the awkward-path fixture
// is that a third party can write a name Epos would never have chosen, so
// nothing here normalises one.
func baseLayer(name string, files map[string]string) ([]byte, error) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	if err := tw.WriteHeader(&tar.Header{
		Name: name + "/", Mode: 0o755, Typeflag: tar.TypeDir, Format: tar.FormatPAX,
	}); err != nil {
		return nil, err
	}
	for _, p := range paths {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name + "/" + p,
			Mode:     0o644,
			Typeflag: tar.TypeReg,
			Size:     int64(len(files[p])),
			Format:   tar.FormatPAX,
		}); err != nil {
			return nil, err
		}
		if _, err := io.WriteString(tw, files[p]); err != nil {
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// --- the shared registry ----------------------------------------------------

var (
	zotOnce     sync.Once
	zotEndpoint string
	zotStop     func()
)

// sharedZot is the one registry this suite uses.
//
// One container for the whole feature rather than one per scenario: each
// scenario publishes its base into a repository of its own (see baseCounter),
// so there is no state to keep apart, and starting a registry seven times over
// buys nothing. It is torn down by TestMain rather than by t.Cleanup, because a
// cleanup registered on whichever test asked for it first would take the
// container down while later tests still needed it.
func sharedZot(ctx context.Context, t *testing.T) string {
	t.Helper()
	zotOnce.Do(func() {
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
			t.Fatalf("start zot: %v", err)
		}
		zotStop = func() { _ = c.Terminate(context.Background()) }

		// Without a scheme: an OCI reference names host:port, and the scheme is
		// the client's business (--plain-http).
		endpoint, err := c.PortEndpoint(ctx, "5000/tcp", "")
		if err != nil {
			t.Fatalf("zot endpoint: %v", err)
		}
		zotEndpoint = endpoint
	})
	if zotEndpoint == "" {
		t.Fatal("the shared zot failed to start")
	}
	return zotEndpoint
}

// stopSharedZot terminates the shared container, if one was ever started.
func stopSharedZot() {
	if zotStop != nil {
		zotStop()
		zotStop = nil
	}
}

// --- suite ------------------------------------------------------------------

// TestBuildFromRegistry is B2's gate: a skill derived from an OCI base, pinned
// by the manifest digest its tag resolved to, published and pulled back by a
// client that has never heard of Epos.
func TestBuildFromRegistry(t *testing.T) {
	godogT = t

	r := &registryBuilder{bin: buildBinary(t, "epos", "../../cmd/epos")}

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				r.reset(t)
				return ctx, nil
			})

			sc.Given(`^a registry holding a base skill$`, func(ctx context.Context) error {
				if err := r.aRegistry(ctx, t); err != nil {
					return err
				}
				return r.anOrdinaryBaseSkill(ctx)
			})
			sc.Given(`^a registry holding a base skill with awkward but legal paths$`,
				func(ctx context.Context) error { return r.anAwkwardBaseSkill(ctx) })
			sc.Given(`^a registry holding a base skill whose layer escapes its root$`,
				func(ctx context.Context) error { return r.anEscapingBaseSkill(ctx) })
			sc.Given(`^a Skillfile deriving "([^"]+)" from that OCI base$`, r.aSkillfileDeriving)

			sc.When(`^the author builds it$`, r.builds)
			sc.When(`^the author builds it again$`, r.builds)
			sc.When(`^the base tag is moved to different content$`, r.theTagMoves)
			sc.When(`^the author builds it from the digest the first build recorded$`,
				r.buildsFromTheRecordedDigest)
			sc.When(`^the derived skill is published with plain oras$`, r.publishedWithPlainOras)
			sc.When(`^plain oras pulls it back$`, r.plainOrasPullsItBack)

			sc.Then(`^the build succeeds$`, r.buildSucceeds)
			sc.Then(`^the store holds "([^"]+)"$`, r.storeHoldsTag)
			sc.Then(`^the artifact has exactly one content layer$`, r.exactlyOneContentLayer)
			sc.Then(`^the layer holds "([^"]+)" containing "([^"]+)"$`, r.layerHolds)
			sc.Then(`^the layer does not hold "([^"]+)"$`, r.layerDoesNotHold)
			sc.Then(`^the build reports the manifest digest of the OCI base$`, r.reportsTheDigest)
			sc.Then(`^the artifact records the OCI base in its provenance annotations$`,
				r.recordsProvenance)
			sc.Then(`^the recorded base digest is the one the registry holds$`,
				r.recordedDigestIsTheRegistrys)
			sc.Then(`^the two builds record different base digests$`, r.twoBuildsDisagreeOnTheBase)
			sc.Then(`^the second build records the same base digest as the first$`,
				r.secondBuildRecordsTheFirstsBase)
			sc.Then(`^both builds produce the same content layer$`, r.bothBuildsProduceTheSameLayer)
			sc.Then(`^the artifact declares no Epos media type$`, r.declaresNoEposMediaType)
			sc.Then(`^the artifact carries the agent-skills media types and nothing else$`,
				r.carriesTheAgentSkillsTypes)
			sc.Then(`^the layer holds every awkward path the base carried$`,
				r.layerHoldsEveryAwkwardPath)
			sc.Then(`^the build fails because the base escapes the skill root$`,
				r.buildFailsOnAnEscapingBase)
			sc.Then(`^the pulled digest matches the digest the build reported$`,
				r.pulledDigestMatchesTheBuild)
			sc.Then(`^the pulled manifest carries the provenance annotations$`,
				r.pulledManifestCarriesProvenance)
			sc.Then(`^the pulled manifest names the base it was built from$`,
				r.pulledManifestNamesTheBase)
		},
		Options: &godog.Options{
			Format:   "pretty,junit:junit-build-from-registry.xml",
			Paths:    []string{"../../features/build-from-registry.feature"},
			Tags:     "~@wip",
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("build from registry suite failed")
	}
}
