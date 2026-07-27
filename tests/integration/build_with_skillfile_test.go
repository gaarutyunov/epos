//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
)

// skillBuilder drives `epos build` as a user would: a real binary, a real
// store directory, a real git server. Nothing here reaches into the packages
// under test, and — the point of B1 — nothing here starts a registry.
type skillBuilder struct {
	bin     string
	fixture gitFixture

	home    string // HOME, so the CLI's ~/.epos/store lands in the sandbox
	altHome string // a second machine, with a store that has never seen the skill
	dir     string // the build context

	stdout   string
	stderr   string
	buildErr error

	tag      string
	digests  []string
	worktree string
}

func (b *skillBuilder) reset(t *testing.T) {
	root := t.TempDir()
	b.home = filepath.Join(root, "home")
	b.altHome = filepath.Join(root, "home2")
	b.worktree = filepath.Join(root, "worktree")
	for _, d := range []string{b.home, b.altHome, b.worktree} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	b.dir = filepath.Join(root, "context")
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b.stdout, b.stderr, b.buildErr = "", "", nil
	b.tag = ""
	b.digests = nil
}

// epos runs the CLI with stdout and stderr kept apart, which is the whole point
// of the split: the digest goes to one and the warnings and pins to the other.
func (b *skillBuilder) epos(home string, args ...string) (string, string, error) {
	cmd := exec.Command(b.bin, args...)
	cmd.Env = append(os.Environ(), "HOME="+home)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

func (b *skillBuilder) write(name, body string) error {
	full := filepath.Join(b.dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(body), 0o600)
}

// gitBase is the FROM reference every scenario derives from.
func (b *skillBuilder) gitBase() string {
	return "git+" + b.fixture.url + "#main:skills/pdf"
}

// --- Given ------------------------------------------------------------------

func (b *skillBuilder) aGitServer(ctx context.Context, t *testing.T) error {
	b.fixture = sharedGitea(ctx, t)
	return nil
}

// aSkillfileDeriving is the ordinary case: one git base, edited into a skill of
// its own.
func (b *skillBuilder) aSkillfileDeriving(name string) error {
	b.tag = name + ":2.0.0"
	if err := b.write("notes.md", "in-house notes\n"); err != nil {
		return err
	}
	return b.write("Skillfile", fmt.Sprintf(`ARG language=Python
FROM %s
SET name %s
SET version 2.0.0
SET description "reviews code"
SET language $language
RM extra.md
COPY notes.md references/notes.md
APPEND SKILL.md <<EOF

Derived from a git base, with no registry involved.
EOF
`, b.gitBase(), name))
}

func (b *skillBuilder) aSkillfileWithNoOpEdits() error {
	b.tag = "reviewer:2.0.0"
	return b.write("Skillfile", fmt.Sprintf(`FROM %s
SET name reviewer
SET version 2.0.0
SET description "reviews code"
REPLACE SKILL.md "no-such-text-anywhere" "unreachable"
UNSET never-was-here
`, b.gitBase()))
}

// aComposedSkillfile takes one named file out of a git stage and nothing else,
// which is what 8.4 means by explicit enumeration.
func (b *skillBuilder) aComposedSkillfile() error {
	b.tag = "reviewer:2.0.0"
	err := b.write("base/SKILL.md",
		"---\nname: reviewer\nversion: 2.0.0\ndescription: reviews code\n---\n\n# Reviewer\n")
	if err != nil {
		return err
	}
	return b.write("Skillfile", fmt.Sprintf(`FROM %s AS upstream
FROM ./base
COPY --from=upstream references/style.md references/style.md
`, b.gitBase()))
}

// aStageReusedAsABase is 8.4's worked example: a stage is declared, and a later
// FROM names it instead of a path.
func (b *skillBuilder) aStageReusedAsABase() error {
	b.tag = "reviewer:2.0.0"
	if err := b.write("local/house.md", "house rules\n"); err != nil {
		return err
	}
	return b.write("Skillfile", fmt.Sprintf(`FROM %s AS upstream
FROM ./local AS local
FROM upstream
SET name reviewer
SET version 2.0.0
SET description "reviews code"
RM extra.md
COPY --from=local house.md references/house.md
`, b.gitBase()))
}

// aHandWrittenFrontmatter is a frontmatter block written the way a person
// writes one rather than the way a serialiser would: a comment above a key, a
// comment trailing another, a deliberate key order and quoting chosen by hand.
func (b *skillBuilder) aHandWrittenFrontmatter() error {
	b.tag = "reviewer:2.0.0"
	err := b.write("base/SKILL.md", `---
# the fields an agent reads before loading anything
name: reviewer
version: "2.0.0" # pinned by hand, and a string on purpose
description: reviews code
model: sonnet # the cheap one
language: 'Python'
---

# Reviewer
`)
	if err != nil {
		return err
	}
	return b.write("Skillfile", "FROM ./base\nSET model opus\n")
}

// --- When -------------------------------------------------------------------

func (b *skillBuilder) builds(home string, args ...string) error {
	b.stdout, b.stderr, b.buildErr = b.epos(home, append([]string{"build"}, append(args, b.dir)...)...)
	if b.buildErr == nil {
		b.digests = append(b.digests, lastField(b.stdout))
	}
	return nil // scenarios assert on buildErr
}

func (b *skillBuilder) theAuthorBuilds() error { return b.builds(b.home) }

func (b *skillBuilder) aSecondMachineBuilds() error { return b.builds(b.altHome) }

func (b *skillBuilder) buildsWithArg(arg string) error {
	return b.builds(b.home, "--build-arg", arg)
}

// extracts unpacks the content layer the way a client installing the skill
// would: straight into the default install path of 10.2.
func (b *skillBuilder) extracts() error {
	m, err := b.manifest(b.home)
	if err != nil {
		return err
	}
	packed, err := b.blob(b.home, m.Layers[0])
	if err != nil {
		return err
	}

	gr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return err
	}
	defer func() { _ = gr.Close() }()

	root := filepath.Join(b.worktree, ".claude", "skills")
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if strings.Contains(h.Name, `\`) {
			return fmt.Errorf("layer entry %q is not slash-separated; 2.5 requires it", h.Name)
		}
		// FromSlash, which is what 2.5 says install does with an entry name.
		target := filepath.Join(root, filepath.FromSlash(h.Name))
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, body, 0o644); err != nil {
			return err
		}
	}
}

// --- Then -------------------------------------------------------------------

func (b *skillBuilder) buildSucceeds() error {
	if b.buildErr != nil {
		return fmt.Errorf("build failed: %v\nstdout: %s\nstderr: %s", b.buildErr, b.stdout, b.stderr)
	}
	return nil
}

func (b *skillBuilder) storeHoldsTag(tag string) error {
	out, _, err := b.epos(b.home, "store", "ls")
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

func (b *skillBuilder) exactlyOneContentLayer() error {
	m, err := b.manifest(b.home)
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

func (b *skillBuilder) layerHolds(path, want string) error {
	files, err := b.layer(b.home)
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

// layerIsExactly compares a whole file, which is what 8.2.4's fidelity claim
// needs: a `contains` assertion cannot tell a preserved document from a
// re-serialised one that happens to still hold the same words.
func (b *skillBuilder) layerIsExactly(path string, want *godog.DocString) error {
	files, err := b.layer(b.home)
	if err != nil {
		return err
	}
	body, ok := files[path]
	if !ok {
		return fmt.Errorf("the layer holds %v, not %s", sortedKeys(files), path)
	}
	if strings.TrimRight(body, "\n") != strings.TrimRight(want.Content, "\n") {
		return fmt.Errorf("%s is\n%s\nwant\n%s", path, body, want.Content)
	}
	return nil
}

func (b *skillBuilder) layerDoesNotHold(path string) error {
	files, err := b.layer(b.home)
	if err != nil {
		return err
	}
	if _, ok := files[path]; ok {
		return fmt.Errorf("%s is in the layer; the Skillfile did not ask for it", path)
	}
	return nil
}

// reportsThePin checks the two SHAs against the objects the fixture itself
// built, not against whatever the build claimed — otherwise the assertion is
// only that the resolver agrees with itself.
func (b *skillBuilder) reportsThePin() error {
	for _, want := range []string{
		b.gitBase(),
		"commit " + b.fixture.mainCommit.String(),
		"tree   " + b.fixture.mainTree.String(),
	} {
		if !strings.Contains(b.stderr, want) {
			return fmt.Errorf("the build did not report %q:\n%s", want, b.stderr)
		}
	}
	return nil
}

func (b *skillBuilder) recordsProvenance() error {
	m, err := b.manifest(b.home)
	if err != nil {
		return err
	}
	want := map[string]string{
		ocispec.AnnotationBaseImageName: b.gitBase(),
		ocispec.AnnotationBaseImageDigest: b.fixture.mainCommit.String() + "+" +
			b.fixture.mainTree.String(),
	}
	for k, v := range want {
		if m.Annotations[k] != v {
			return fmt.Errorf("annotation %s = %q, want %q", k, m.Annotations[k], v)
		}
	}
	if !strings.HasPrefix(m.Annotations["dev.epos.skillfile.digest"], "sha256:") {
		return fmt.Errorf("the Skillfile digest annotation is %q",
			m.Annotations["dev.epos.skillfile.digest"])
	}
	return nil
}

func (b *skillBuilder) warnsAboutTheReplace() error {
	return b.warns(`"no-such-text-anywhere" matched nothing`)
}

func (b *skillBuilder) warnsAboutTheUnset() error {
	return b.warns(`key "never-was-here" was already absent`)
}

func (b *skillBuilder) warns(want string) error {
	if !strings.Contains(b.stderr, want) {
		return fmt.Errorf("the build did not warn %q:\n%s", want, b.stderr)
	}
	if strings.Contains(b.stdout, want) {
		return fmt.Errorf("the warning reached stdout, which carries only the digest:\n%s", b.stdout)
	}
	return nil
}

func (b *skillBuilder) bothBuildsAgree() error {
	if len(b.digests) < 2 {
		return fmt.Errorf("expected 2 digests, got %d", len(b.digests))
	}
	if b.digests[0] != b.digests[1] {
		return fmt.Errorf("digests differ: %s vs %s; 2.4 requires them identical",
			b.digests[0], b.digests[1])
	}
	return nil
}

// worktreeHoldsTheSkill checks that what came out of the layer is an ordinary
// skill directory — the same thing a hand-authored one would be (2.1).
func (b *skillBuilder) worktreeHoldsTheSkill() error {
	root := filepath.Join(b.worktree, ".claude", "skills", "reviewer")
	for path, want := range map[string]string{
		"SKILL.md":            "name: reviewer",
		"references/notes.md": "in-house notes",
		"references/style.md": "House style",
	} {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		if !strings.Contains(string(body), want) {
			return fmt.Errorf("%s does not contain %q:\n%s", path, want, body)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "extra.md")); !os.IsNotExist(err) {
		return fmt.Errorf("extra.md survived the RM into the worktree")
	}
	return nil
}

// --- store helpers ----------------------------------------------------------

func (b *skillBuilder) manifest(home string) (ocispec.Manifest, error) {
	var m ocispec.Manifest
	body, err := b.resolveAndFetch(home)
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(body, &m)
}

func (b *skillBuilder) resolveAndFetch(home string) ([]byte, error) {
	st, err := oci.New(filepath.Join(home, ".epos", "store"))
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	desc, err := st.Resolve(ctx, b.tag)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", b.tag, err)
	}
	return content.FetchAll(ctx, st, desc)
}

func (b *skillBuilder) blob(home string, desc ocispec.Descriptor) ([]byte, error) {
	st, err := oci.New(filepath.Join(home, ".epos", "store"))
	if err != nil {
		return nil, err
	}
	return content.FetchAll(context.Background(), st, desc)
}

// layer reads the content layer back as path → contents, which is what a
// conforming client extracting the artifact sees.
func (b *skillBuilder) layer(home string) (map[string]string, error) {
	m, err := b.manifest(home)
	if err != nil {
		return nil, err
	}
	if len(m.Layers) != 1 {
		return nil, fmt.Errorf("manifest has %d layers, want exactly 1", len(m.Layers))
	}
	packed, err := b.blob(home, m.Layers[0])
	if err != nil {
		return nil, err
	}

	gr, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, err
	}
	defer func() { _ = gr.Close() }()

	out := map[string]string{}
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
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

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// --- suite ------------------------------------------------------------------

// TestBuildWithSkillfile is B1's gate: a skill derived from a git base builds
// with no registry involved anywhere.
//
// No zot is started here and none is needed. The base comes over plain git
// HTTP from the shared Gitea, and the result never leaves the local store.
func TestBuildWithSkillfile(t *testing.T) {
	godogT = t

	b := &skillBuilder{bin: buildBinary(t, "epos", "../../cmd/epos")}

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				b.reset(t)
				return ctx, nil
			})

			sc.Given(`^a git server holding a base skill$`, func(ctx context.Context) error {
				return b.aGitServer(ctx, t)
			})
			sc.Given(`^a Skillfile deriving "([^"]+)" from that git base$`, b.aSkillfileDeriving)
			sc.Given(`^a Skillfile whose REPLACE matches nothing and whose UNSET key is absent$`,
				b.aSkillfileWithNoOpEdits)
			sc.Given(`^a Skillfile composing the git base with a local base$`, b.aComposedSkillfile)
			sc.Given(`^a Skillfile whose last stage starts from a stage declared earlier$`,
				b.aStageReusedAsABase)
			sc.Given(`^a Skillfile setting one key of a hand-written frontmatter block$`,
				b.aHandWrittenFrontmatter)

			sc.When(`^the author builds it$`, b.theAuthorBuilds)
			sc.When(`^a second machine builds it$`, b.aSecondMachineBuilds)
			sc.When(`^the author builds it with the build argument "([^"]+)"$`, b.buildsWithArg)
			sc.When(`^the artifact is extracted into a worktree$`, b.extracts)

			sc.Then(`^the build succeeds$`, b.buildSucceeds)
			sc.Then(`^the store holds "([^"]+)"$`, b.storeHoldsTag)
			sc.Then(`^the artifact has exactly one content layer$`, b.exactlyOneContentLayer)
			sc.Then(`^the layer holds "([^"]+)" containing "([^"]+)"$`, b.layerHolds)
			sc.Then(`^the layer's "([^"]+)" is exactly:$`, b.layerIsExactly)
			sc.Then(`^the layer does not hold "([^"]+)"$`, b.layerDoesNotHold)
			sc.Then(`^the build reports the commit and tree of the git base$`, b.reportsThePin)
			sc.Then(`^the artifact records the git base in its provenance annotations$`,
				b.recordsProvenance)
			sc.Then(`^the build warns that the REPLACE matched nothing$`, b.warnsAboutTheReplace)
			sc.Then(`^the build warns that the UNSET key was already absent$`, b.warnsAboutTheUnset)
			sc.Then(`^both builds report the same digest$`, b.bothBuildsAgree)
			sc.Then(`^the worktree holds the skill directory the Skillfile built$`,
				b.worktreeHoldsTheSkill)
		},
		Options: &godog.Options{
			Format:   "pretty,junit:junit-build.xml",
			Paths:    []string{"../../features/build-with-skillfile.feature"},
			Tags:     "~@wip",
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("build with skillfile suite failed")
	}
}
