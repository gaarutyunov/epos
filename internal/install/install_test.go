package install

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oras.land/oras-go/v2/content/oci"

	"github.com/gaarutyunov/epos/internal/artifact"
	"github.com/gaarutyunov/epos/internal/store"
)

// skill is the fixture every scenario installs: a parameterised SKILL.md, plus
// one file that a stage contributed.
func skill(version string) map[string][]byte {
	return map[string][]byte{
		"SKILL.md": []byte("---\nname: reviewer\nversion: " + version +
			"\ndescription: reviews code\n---\n\n# {{ .Values.title }}\n\n" +
			"Model: {{ .Values.model }} at {{ .Values.global.org }}\n"),
		"references/shared.md": []byte("# {{ .Values.title }}\n"),
	}
}

// pack writes an artifact into the store and tags it, the way `epos pack` and
// `epos build` do. Returns the manifest digest, which is what the lock pins.
func pack(t *testing.T, s *store.Store, tag string,
	files map[string][]byte, annotations map[string]string) string {
	t.Helper()

	var built artifact.Skill
	err := s.Push(context.Background(), tag,
		func(ctx context.Context, st *oci.Store) (ocispec.Descriptor, error) {
			var err error
			built, err = artifact.BuildFiles(ctx, st, files, annotations)
			if err != nil {
				return ocispec.Descriptor{}, err
			}
			return built.Manifest, nil
		})
	require.NoError(t, err, "pack %s", tag)
	return built.Manifest.Digest.String()
}

// stagesAnnotation is what a multi-stage build records (SPEC.md 8.4).
func stagesAnnotation(t *testing.T, stages map[string]string) map[string]string {
	t.Helper()
	body, err := json.Marshal(stages)
	require.NoError(t, err)
	return map[string]string{artifact.StagesAnnotation: string(body)}
}

func valuesFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func readFile(t *testing.T, parts ...string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(parts...))
	require.NoError(t, err)
	return string(body)
}

// --- the reference parser ---------------------------------------------------

func TestStoreTag(t *testing.T) {
	for _, tc := range []struct {
		ref  string
		want string
	}{
		{"reviewer:2.0.0", "reviewer:2.0.0"},
		{"ghcr.io/acme/agent-skills/pdf:1.2.0", "pdf:1.2.0"},
		// A registry may carry a port, so the version is the last colon after
		// the last slash and not the first colon in the string.
		{"127.0.0.1:45100/demo/agent-skills/pdf:1.0.0", "pdf:1.0.0"},
		{"localhost:5000/pdf:1.0.0", "pdf:1.0.0"},
	} {
		got, err := StoreTag(tc.ref)
		require.NoError(t, err, tc.ref)
		assert.Equal(t, tc.want, got, tc.ref)
	}
}

func TestStoreTagRejectsWhatHasNoVersion(t *testing.T) {
	for _, ref := range []string{"reviewer", "127.0.0.1:5000/demo/pdf", "reviewer:", ":1.0.0"} {
		_, err := StoreTag(ref)
		assert.Error(t, err, ref)
	}
}

// --- installing -------------------------------------------------------------

// SPEC.md 10.1 and 10.4: the artifact carries templates verbatim and they are
// rendered here, into the default install path of 10.2.
func TestInstallRendersIntoTheDefaultBasePath(t *testing.T) {
	root := t.TempDir()
	s := store.Under(root)
	digest := pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)
	dir := t.TempDir()

	res, err := Install(context.Background(), s, Options{
		Dir:        dir,
		Ref:        "reviewer:2.0.0",
		ValueFiles: []string{valuesFile(t, "title: Reviewer\nmodel: opus\nglobal:\n  org: Acme\n")},
	})
	require.NoError(t, err)

	assert.Equal(t, "reviewer", res.Name)
	assert.Equal(t, "2.0.0", res.Version)
	assert.Equal(t, digest, res.Digest)
	assert.Equal(t, []string{DefaultBasePath}, res.BasePaths)

	installed := readFile(t, dir, ".claude", "skills", "reviewer", "SKILL.md")
	assert.Contains(t, installed, "# Reviewer")
	assert.Contains(t, installed, "Model: opus at Acme")
	assert.NotContains(t, installed, "{{")

	// The store still holds the template: rendering happens at install and
	// changes nothing upstream of it.
	assert.Contains(t, string(skill("2.0.0")["SKILL.md"]), "{{ .Values.title }}")
}

func TestInstallAppliesSetOverAValuesFile(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)
	dir := t.TempDir()

	_, err := Install(context.Background(), s, Options{
		Dir:        dir,
		Ref:        "reviewer:2.0.0",
		ValueFiles: []string{valuesFile(t, "title: FromFile\nmodel: sonnet\nglobal:\n  org: Acme\n")},
		Sets:       []string{"title=FromFlag", "model=opus"},
	})
	require.NoError(t, err)

	assert.Contains(t, readFile(t, dir, ".claude", "skills", "reviewer", "SKILL.md"),
		"# FromFlag")
}

// SPEC.md 10.3, the collision case the scoping exists for: two stages both
// writing .Values.title, each getting its own.
func TestTwoStagesRenderTheSameKeyDifferently(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:2.0.0", skill("2.0.0"),
		stagesAnnotation(t, map[string]string{"references/shared.md": "shared"}))
	dir := t.TempDir()

	_, err := Install(context.Background(), s, Options{
		Dir: dir,
		Ref: "reviewer:2.0.0",
		ValueFiles: []string{valuesFile(t, `
global:
  org: Acme
title: The final stage
model: opus
shared:
  title: The shared stage
`)},
	})
	require.NoError(t, err)

	root := filepath.Join(dir, ".claude", "skills", "reviewer")
	assert.Contains(t, readFile(t, root, "SKILL.md"), "# The final stage")
	assert.Contains(t, readFile(t, root, "references", "shared.md"), "# The shared stage")
}

// Without the provenance annotation there is one scope, which is the right
// answer for a packed skill and for a build that never composed anything.
func TestASkillWithoutStagesRendersInTheTopLevelScope(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)
	dir := t.TempDir()

	_, err := Install(context.Background(), s, Options{
		Dir:        dir,
		Ref:        "reviewer:2.0.0",
		ValueFiles: []string{valuesFile(t, "title: One scope\nmodel: opus\nglobal:\n  org: Acme\n")},
	})
	require.NoError(t, err)

	root := filepath.Join(dir, ".claude", "skills", "reviewer")
	assert.Contains(t, readFile(t, root, "SKILL.md"), "# One scope")
	assert.Contains(t, readFile(t, root, "references", "shared.md"), "# One scope")
}

// SPEC.md 10.2: additionalBasePaths covers the other agent vendors.
func TestInstallHonoursAdditionalBasePaths(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)

	dir := t.TempDir()
	declared := &Manifest{AdditionalBasePaths: []string{".cursor/skills", ".claude/skills"}}
	require.NoError(t, declared.Save(dir))

	res, err := Install(context.Background(), s, Options{
		Dir:        dir,
		Ref:        "reviewer:2.0.0",
		ValueFiles: []string{valuesFile(t, "title: T\nmodel: opus\nglobal:\n  org: Acme\n")},
	})
	require.NoError(t, err)

	// The default first, the vendor path second, and the duplicate dropped.
	assert.Equal(t, []string{DefaultBasePath, ".cursor/skills"}, res.BasePaths)
	assert.FileExists(t, filepath.Join(dir, ".cursor", "skills", "reviewer", "SKILL.md"))
	assert.FileExists(t, filepath.Join(dir, ".claude", "skills", "reviewer", "SKILL.md"))
}

// Nothing is written until everything renders, so a template that fails leaves
// the worktree as it was rather than half-installed.
func TestAFailedRenderWritesNothing(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)
	dir := t.TempDir()

	_, err := Install(context.Background(), s, Options{Dir: dir, Ref: "reviewer:2.0.0"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not supplied")

	assert.NoDirExists(t, filepath.Join(dir, ".claude", "skills", "reviewer"))
	assert.NoFileExists(t, filepath.Join(dir, LockFile))
}

// Install resolves; it does not fetch. Fetching takes the exclusive lock, and
// taking it here is exactly what 9.2 does not want an install to do.
func TestInstallSaysWhenTheStoreDoesNotHoldTheSkill(t *testing.T) {
	s := store.Under(t.TempDir())

	_, err := Install(context.Background(), s, Options{Dir: t.TempDir(), Ref: "reviewer:2.0.0"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reviewer:2.0.0")
	assert.Contains(t, err.Error(), "pull or build it first")
}

// A reinstall replaces the directory rather than merging into it: files the
// new version dropped must not survive, or the worktree holds neither version.
func TestReinstallingReplacesTheSkillDirectory(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:1.0.0", skill("1.0.0"), nil)

	slim := skill("2.0.0")
	delete(slim, "references/shared.md")
	pack(t, s, "reviewer:2.0.0", slim, nil)

	dir := t.TempDir()
	values := valuesFile(t, "title: T\nmodel: opus\nglobal:\n  org: Acme\n")
	opts := Options{Dir: dir, Ref: "reviewer:1.0.0", ValueFiles: []string{values}}

	_, err := Install(context.Background(), s, opts)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, ".claude", "skills", "reviewer", "references", "shared.md"))

	opts.Ref = "reviewer:2.0.0"
	_, err = Install(context.Background(), s, opts)
	require.NoError(t, err)
	assert.NoFileExists(t, filepath.Join(dir, ".claude", "skills", "reviewer", "references", "shared.md"))

	pinned, err := List(dir)
	require.NoError(t, err)
	require.Len(t, pinned, 1)
	assert.Equal(t, "2.0.0", pinned[0].Version)
}

// --- the manifests ----------------------------------------------------------

func TestInstallWritesBothManifests(t *testing.T) {
	s := store.Under(t.TempDir())
	digest := pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)
	dir := t.TempDir()

	_, err := Install(context.Background(), s, Options{
		Dir:        dir,
		Ref:        "ghcr.io/acme/agent-skills/reviewer:2.0.0",
		ValueFiles: []string{valuesFile(t, "title: T\nmodel: opus\nglobal:\n  org: Acme\n")},
	})
	require.NoError(t, err)

	declared, err := LoadManifest(dir)
	require.NoError(t, err)
	// The declaration keeps the reference as written, so skills.json still
	// says where the skill came from.
	assert.Equal(t, []Declared{{
		Name: "reviewer",
		Ref:  "ghcr.io/acme/agent-skills/reviewer:2.0.0",
	}}, declared.Skills)

	lock, err := LoadLock(dir)
	require.NoError(t, err)
	assert.Equal(t, []Locked{{
		Name:      "reviewer",
		Version:   "2.0.0",
		Ref:       "ghcr.io/acme/agent-skills/reviewer:2.0.0",
		Digest:    digest,
		BasePaths: []string{DefaultBasePath},
	}}, lock.Skills)
}

// SPEC.md 10.2: a pin file, never a symlink — nothing here depends on a
// Windows symlink working.
func TestTheLockIsAPlainFile(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)
	dir := t.TempDir()

	_, err := Install(context.Background(), s, Options{
		Dir:        dir,
		Ref:        "reviewer:2.0.0",
		ValueFiles: []string{valuesFile(t, "title: T\nmodel: opus\nglobal:\n  org: Acme\n")},
	})
	require.NoError(t, err)

	info, err := os.Lstat(filepath.Join(dir, LockFile))
	require.NoError(t, err)
	assert.True(t, info.Mode().IsRegular(), "skills.lock.json is %s", info.Mode())
}

// A lock whose entry order came from a Go map would churn on every write, and
// a pin that differs between two runs that installed the same things is not a
// pin. Written enough times that map iteration order would have shown up.
func TestTheLockSerialisesDeterministically(t *testing.T) {
	dir := t.TempDir()
	entries := []Locked{
		{Name: "zebra", Version: "1.0.0", Ref: "zebra:1.0.0", Digest: "sha256:c",
			BasePaths: []string{DefaultBasePath}},
		{Name: "alpha", Version: "1.0.0", Ref: "alpha:1.0.0", Digest: "sha256:a",
			BasePaths: []string{DefaultBasePath}},
		{Name: "middle", Version: "1.0.0", Ref: "middle:1.0.0", Digest: "sha256:b",
			BasePaths: []string{DefaultBasePath}},
	}

	var first string
	for run := range 8 {
		lock := &Lock{}
		// A different insertion order every run, which is what an install
		// driven off a map would produce.
		for i := range entries {
			lock.Pin(entries[(i+run)%len(entries)])
		}
		require.NoError(t, lock.Save(dir))

		body := readFile(t, dir, LockFile)
		if run == 0 {
			first = body
			continue
		}
		assert.Equal(t, first, body, "run %d rewrote the lock", run)
	}

	assert.Contains(t, first, `"alpha"`)
	// Sorted by name, so alpha comes before zebra whatever order they arrived.
	assert.Less(t, indexOf(first, "alpha"), indexOf(first, "zebra"))
}

func indexOf(haystack, needle string) int {
	return bytes.Index([]byte(haystack), []byte(needle))
}

// The lock is committed and compared across machines, so its paths are
// slash-separated whichever platform wrote it.
func TestTheLockRecordsSlashSeparatedPaths(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)

	dir := t.TempDir()
	declared := &Manifest{AdditionalBasePaths: []string{".cursor/skills/"}}
	require.NoError(t, declared.Save(dir))

	_, err := Install(context.Background(), s, Options{
		Dir:        dir,
		Ref:        "reviewer:2.0.0",
		ValueFiles: []string{valuesFile(t, "title: T\nmodel: opus\nglobal:\n  org: Acme\n")},
	})
	require.NoError(t, err)

	body := readFile(t, dir, LockFile)
	assert.Contains(t, body, `".claude/skills"`)
	assert.Contains(t, body, `".cursor/skills"`)
	assert.NotContains(t, body, `\\`)
}

// --- uninstall and ls -------------------------------------------------------

func TestUninstallRemovesEverythingItInstalled(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)

	dir := t.TempDir()
	declared := &Manifest{AdditionalBasePaths: []string{".cursor/skills"}}
	require.NoError(t, declared.Save(dir))

	_, err := Install(context.Background(), s, Options{
		Dir:        dir,
		Ref:        "reviewer:2.0.0",
		ValueFiles: []string{valuesFile(t, "title: T\nmodel: opus\nglobal:\n  org: Acme\n")},
	})
	require.NoError(t, err)

	removed, err := Uninstall(dir, "reviewer")
	require.NoError(t, err)
	assert.Equal(t, []string{".claude/skills/reviewer", ".cursor/skills/reviewer"}, removed)

	assert.NoDirExists(t, filepath.Join(dir, ".claude", "skills", "reviewer"))
	assert.NoDirExists(t, filepath.Join(dir, ".cursor", "skills", "reviewer"))

	pinned, err := List(dir)
	require.NoError(t, err)
	assert.Empty(t, pinned)

	declared, err = LoadManifest(dir)
	require.NoError(t, err)
	assert.Empty(t, declared.Skills)
	// The vendor path is the author's declaration and survives an uninstall.
	assert.Equal(t, []string{".cursor/skills"}, declared.AdditionalBasePaths)
}

func TestUninstallSaysWhenNothingIsInstalled(t *testing.T) {
	_, err := Uninstall(t.TempDir(), "reviewer")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not installed here")
}

func TestListOnAWorktreeThatInstalledNothing(t *testing.T) {
	pinned, err := List(t.TempDir())
	require.NoError(t, err)
	assert.Empty(t, pinned)
}

// --- the shared lock (SPEC.md 9.2) ------------------------------------------

// The lock discipline 9.2 asks for, at the point it matters: another shared
// reader must not make an install wait. An exclusive lock here would deadlock
// against the reader this test is holding open, and the install would never
// return.
func TestInstallProceedsWhileAnotherReaderHoldsTheStore(t *testing.T) {
	s := store.Under(t.TempDir())
	pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)
	dir := t.TempDir()

	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Read(context.Background(), func(context.Context, *oci.Store) error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	defer func() {
		close(release)
		require.NoError(t, <-done)
	}()

	installed := make(chan error, 1)
	go func() {
		_, err := Install(context.Background(), s, Options{
			Dir:        dir,
			Ref:        "reviewer:2.0.0",
			ValueFiles: []string{valuesFile(t, "title: T\nmodel: opus\nglobal:\n  org: Acme\n")},
		})
		installed <- err
	}()

	select {
	case err := <-installed:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the install did not finish while another reader held the store; " +
			"9.2 wants a shared lock, and this is what an exclusive one looks like")
	}
}

// SPEC.md 10.2's whole point: different worktrees pin different digests from
// shared storage, at the same time.
func TestTwoWorktreesPinDifferentDigestsAtOnce(t *testing.T) {
	s := store.Under(t.TempDir())
	one := pack(t, s, "reviewer:1.0.0", skill("1.0.0"), nil)
	two := pack(t, s, "reviewer:2.0.0", skill("2.0.0"), nil)
	require.NotEqual(t, one, two)

	values := valuesFile(t, "title: T\nmodel: opus\nglobal:\n  org: Acme\n")
	dirs := []string{t.TempDir(), t.TempDir()}
	refs := []string{"reviewer:1.0.0", "reviewer:2.0.0"}

	// Released together, so both installs are inside the store at once rather
	// than one after the other.
	start := make(chan struct{})
	errs := make(chan error, 2)
	for i := range dirs {
		go func() {
			<-start
			_, err := Install(context.Background(), s, Options{
				Dir: dirs[i], Ref: refs[i], ValueFiles: []string{values},
			})
			errs <- err
		}()
	}
	close(start)
	for range dirs {
		require.NoError(t, <-errs)
	}

	for i, want := range []string{one, two} {
		pinned, err := List(dirs[i])
		require.NoError(t, err)
		require.Len(t, pinned, 1)
		assert.Equal(t, want, pinned[0].Digest, "worktree %d", i)
	}
}

// --- what the layer may contain (SPEC.md 2.5) -------------------------------

// A layer is not necessarily one this epos produced, so the rules pack applies
// are applied again on the way out.
func TestExtractRejectsWhatEscapesTheSkillRoot(t *testing.T) {
	for _, tc := range []struct {
		name  string
		entry tar.Header
		want  string
	}{
		{
			name:  "parent directory",
			entry: tar.Header{Name: "reviewer/../../escaped.md", Typeflag: tar.TypeReg},
			want:  "escapes the skill root",
		},
		{
			name:  "symlink",
			entry: tar.Header{Name: "reviewer/link.md", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"},
			want:  "contains a link",
		},
		{
			name:  "outside the root",
			entry: tar.Header{Name: "elsewhere/SKILL.md", Typeflag: tar.TypeReg},
			want:  "not rooted at",
		},
		{
			name:  "backslash separator",
			entry: tar.Header{Name: `reviewer\SKILL.md`, Typeflag: tar.TypeReg},
			want:  "slash-separated",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := extract(hostileLayer(t, tc.entry), "reviewer")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestExtractRejectsALayerWithNoSkillFile(t *testing.T) {
	_, err := extract(hostileLayer(t, tar.Header{
		Name: "reviewer/notes.md", Typeflag: tar.TypeReg,
	}), "reviewer")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no SKILL.md")
}

// hostileLayer builds a layer the packer would never have produced.
func hostileLayer(t *testing.T, entries ...tar.Header) []byte {
	t.Helper()

	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, h := range entries {
		h.Mode = 0o644
		require.NoError(t, tw.WriteHeader(&h))
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}
