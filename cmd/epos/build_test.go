package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"

	"github.com/gaarutyunov/epos/internal/store"
)

// The fixture exercises several instruction kinds rather than one, because the
// determinism claim of 2.4 is about the whole evaluation: a stray timestamp or
// an iteration-order leak can hide in any of COPY, PATCH, AWK, SET or the
// packer, and a Skillfile with a single FROM in it would prove none of them.
const buildFixtureSkillfile = `ARG language=Python
FROM ./shared AS shared
FROM ./base
COPY --from=shared reference/style.md references/style.md
COPY extra/glossary.md references/glossary.md
RM sections/obsolete.md
APPEND sections/checklist.md <<EOF
- table-driven tests
EOF
REPLACE SKILL.md "model: sonnet" "model: opus"
REPLACE SKILL.md "no-such-text-anywhere" "unreachable"
SET language $language
SET metadata.author acme
UNSET deprecated-field
UNSET never-was-here
PATCH docs/guide.md patches/guide.diff
AWK sections/checklist.md <<EOF
/^- / { print "*" substr($0, 2); next }
{ print }
EOF
`

// buildFixtureFiles is the context the fixture Skillfile builds against.
//
// Enough files, and enough of them nested, that a map-iteration leak anywhere
// on the path has something to reorder: Go randomises map ranging per loop, so
// a set this size makes two builds in one process disagree if any stage of the
// pipeline ever iterates a map straight into its output.
func buildFixtureFiles() map[string]string {
	return map[string]string{
		// The frontmatter is written the way an author writes one — a comment
		// above a key, a comment trailing another, a deliberate rather than
		// alphabetical key order, mixed quoting — because SET and UNSET edit it
		// through goccy's AST (8.2.4) and this is the fixture that has to prove
		// the edit neither reorders the document nor lets a Go map's iteration
		// order anywhere near the digest.
		"base/SKILL.md": "---\n" +
			"# the fields an agent reads before loading anything\n" +
			"name: reviewer\n" +
			"version: \"2.0.0\" # pinned by hand\n" +
			"description: reviews code\n" +
			"model: sonnet\n" +
			"language: 'Python'\n" +
			"deprecated-field: yes\n" +
			"---\n\n# Reviewer\n",
		"base/sections/checklist.md": "- a\n- b\n",
		"base/sections/obsolete.md":  "gone\n",
		"base/sections/house.md":     "house style\n",
		"base/reference/rules.md":    "rules\n",
		"base/docs/guide.md":         "line one\nline two\nline three\n",
		"base/docs/appendix.md":      "appendix\n",
		"shared/SKILL.md":            "---\nname: shared\nversion: 1.0.0\n---\n",
		"shared/reference/style.md":  "House style.\n",
		"shared/reference/unused.md": "not copied\n",
		"extra/glossary.md":          "glossary\n",
		"patches/guide.diff": "--- a/docs/guide.md\n" +
			"+++ b/docs/guide.md\n" +
			"@@ -1,3 +1,3 @@\n" +
			" line one\n" +
			"-line two\n" +
			"+line two, revised\n" +
			" line three\n",
		"Skillfile": buildFixtureSkillfile,
	}
}

// writeContext materialises a build context.
//
// The files are written in sorted or reverse-sorted order and with different
// permissions depending on reverse, because neither creation order nor source
// mode may reach the digest (2.4) and a second context written identically
// would not be able to tell.
func writeContext(t *testing.T, files map[string]string, reverse bool) string {
	t.Helper()

	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	mode := os.FileMode(0o600)
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(paths)))
		mode = 0o755
	}

	dir := t.TempDir()
	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(files[p]), mode))
	}
	return dir
}

// buildInto runs the command as cobra would and returns stdout and stderr.
func buildInto(t *testing.T, s *store.Store, opts buildOptions) (string, string) {
	t.Helper()
	var out, warn bytes.Buffer
	require.NoError(t, runBuild(context.Background(), &out, &warn, s, opts))
	return out.String(), warn.String()
}

// digestOf reads the manifest digest out of the `<tag> <digest>` line.
func digestOf(t *testing.T, stdout string) string {
	t.Helper()
	fields := strings.Fields(stdout)
	require.Len(t, fields, 2, "stdout must be exactly `<tag> <digest>`, got %q", stdout)
	return fields[1]
}

func manifestOf(t *testing.T, s *store.Store, tag string) ocispec.Manifest {
	t.Helper()
	var m ocispec.Manifest
	require.NoError(t, s.Read(context.Background(), func(ctx context.Context, st *oci.Store) error {
		desc, err := st.Resolve(ctx, tag)
		if err != nil {
			return err
		}
		body, err := content.FetchAll(ctx, st, desc)
		if err != nil {
			return err
		}
		return json.Unmarshal(body, &m)
	}))
	return m
}

// layerOf reads the content layer back as path → contents, which is what a
// conforming client extracting the artifact would see.
func layerOf(t *testing.T, s *store.Store, tag string) map[string]string {
	t.Helper()

	m := manifestOf(t, s, tag)
	require.Len(t, m.Layers, 1, "2.1: exactly one content layer")

	var packed []byte
	require.NoError(t, s.Read(context.Background(), func(ctx context.Context, st *oci.Store) error {
		var err error
		packed, err = content.FetchAll(ctx, st, m.Layers[0])
		return err
	}))

	gr, err := gzip.NewReader(bytes.NewReader(packed))
	require.NoError(t, err)
	defer func() { _ = gr.Close() }()

	out := map[string]string{}
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		assert.NotContains(t, h.Name, `\`, "2.5: layer paths are forward slashes on every host")
		if h.Typeflag == tar.TypeDir {
			continue
		}
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[h.Name] = string(body)
	}
	return out
}

func TestBuildAppliesTheSkillfileAndTagsTheResult(t *testing.T) {
	dir := writeContext(t, buildFixtureFiles(), false)
	s := store.At(filepath.Join(t.TempDir(), "store"))

	stdout, _ := buildInto(t, s, buildOptions{contextDir: dir})
	assert.Equal(t, "reviewer:2.0.0", strings.Fields(stdout)[0],
		"the tag defaults to the built SKILL.md's name and version")

	layer := layerOf(t, s, "reviewer:2.0.0")

	assert.Contains(t, layer["reviewer/SKILL.md"], "model: opus", "REPLACE")
	assert.Contains(t, layer["reviewer/SKILL.md"], "language: Python", "the ARG default")
	assert.Contains(t, layer["reviewer/SKILL.md"], "author: acme", "SET on a nested key")
	assert.NotContains(t, layer["reviewer/SKILL.md"], "deprecated-field", "UNSET")
	assert.Equal(t, "House style.\n", layer["reviewer/references/style.md"], "COPY --from")
	assert.Equal(t, "glossary\n", layer["reviewer/references/glossary.md"], "COPY from the context")
	assert.Equal(t, "line one\nline two, revised\nline three\n", layer["reviewer/docs/guide.md"], "PATCH")
	assert.Equal(t, "* a\n* b\n* table-driven tests\n", layer["reviewer/sections/checklist.md"],
		"APPEND then AWK, in file order")

	assert.NotContains(t, layer, "reviewer/sections/obsolete.md", "RM")
	assert.NotContains(t, layer, "reviewer/reference/unused.md",
		"8.4 composes by explicit enumeration; an unnamed file must not come across")

	// 8.2.4: SET and UNSET edit the document, they do not rewrite it. The
	// frontmatter comes back in the order it was written, with its comments and
	// with the quoting its author chose.
	assert.Equal(t, "---\n"+
		"# the fields an agent reads before loading anything\n"+
		"name: reviewer\n"+
		"version: \"2.0.0\" # pinned by hand\n"+
		"description: reviews code\n"+
		"model: opus\n"+
		"language: Python\n"+
		"metadata:\n"+
		"  author: acme\n"+
		"---\n\n# Reviewer\n", layer["reviewer/SKILL.md"])
}

// SPEC.md 2.4: same bases, same Skillfile, same context, same digest.
//
// Deliberately not two runs of one function against one directory, which would
// be true by construction. The two builds go into separate stores from separate
// context directories written in opposite order with different permissions, and
// they are separated in time by more than the one-second granularity of a tar
// or gzip header — so a clock reaching the artifact through any of the places
// one has already reached it (oras.PackManifest's created annotation, a tar
// mtime, the gzip header) shows up as a different digest rather than as a test
// that happened to run fast enough.
func TestBuildIsDeterministicAcrossStoresAndTime(t *testing.T) {
	files := buildFixtureFiles()

	first := store.At(filepath.Join(t.TempDir(), "store"))
	firstOut, _ := buildInto(t, first, buildOptions{contextDir: writeContext(t, files, false)})

	time.Sleep(1100 * time.Millisecond)

	second := store.At(filepath.Join(t.TempDir(), "store"))
	secondOut, _ := buildInto(t, second, buildOptions{contextDir: writeContext(t, files, true)})

	// The manifest digest, not the layer's: the layer being identical is the
	// easy half, and the manifest is where a timestamp annotation would live.
	assert.Equal(t, digestOf(t, firstOut), digestOf(t, secondOut),
		"identical inputs must produce identical manifest digests; 2.4 requires it")

	firstManifest := manifestOf(t, first, "reviewer:2.0.0")
	secondManifest := manifestOf(t, second, "reviewer:2.0.0")
	assert.Equal(t, firstManifest, secondManifest)
	assert.NotContains(t, firstManifest.Annotations, ocispec.AnnotationCreated,
		"a created timestamp would make every build of the same inputs a different artifact")
	assert.Equal(t, layerOf(t, first, "reviewer:2.0.0"), layerOf(t, second, "reviewer:2.0.0"))
}

// Go randomises map iteration, so an ordering leak is a bug that appears in
// some runs and not others. Repeating the build in one process is what turns
// "it happened to match" into evidence.
func TestBuildDigestDoesNotDependOnMapIteration(t *testing.T) {
	files := buildFixtureFiles()
	dir := writeContext(t, files, false)

	seen := map[string]bool{}
	for range 8 {
		s := store.At(filepath.Join(t.TempDir(), "store"))
		stdout, _ := buildInto(t, s, buildOptions{contextDir: dir})
		seen[digestOf(t, stdout)] = true
	}
	assert.Len(t, seen, 1, "the same build produced more than one digest: %v", seen)
}

// SPEC.md 8.2.2 and 8.2.4: warnings, not errors — but visible ones.
func TestBuildWarnsAboutNoOpEditsOnStderr(t *testing.T) {
	dir := writeContext(t, buildFixtureFiles(), false)
	s := store.At(filepath.Join(t.TempDir(), "store"))

	stdout, stderr := buildInto(t, s, buildOptions{contextDir: dir})

	assert.Contains(t, stderr, `"no-such-text-anywhere" matched nothing`)
	assert.Contains(t, stderr, `key "never-was-here" was already absent`)
	assert.NotContains(t, stdout, "warning",
		"stdout carries the digest a script reads and nothing else")
}

// SPEC.md 2.3: a skill built from a base records where it came from.
func TestBuildRecordsProvenance(t *testing.T) {
	dir := writeContext(t, buildFixtureFiles(), false)
	s := store.At(filepath.Join(t.TempDir(), "store"))
	buildInto(t, s, buildOptions{contextDir: dir})

	m := manifestOf(t, s, "reviewer:2.0.0")
	assert.Equal(t, "./base", m.Annotations[ocispec.AnnotationBaseImageName],
		"the base is the final stage's FROM, not the first one declared")
	assert.NotContains(t, m.Annotations, ocispec.AnnotationBaseImageDigest,
		"8.3 gives a local base no pin, and an annotation asserting one would be a claim the build cannot back")
	assert.Contains(t, m.Annotations[skillfileDigestAnnotation], "sha256:")
}

// SPEC.md 8.7: --build-arg beats the ARG default.
func TestBuildArgOverridesTheDefault(t *testing.T) {
	dir := writeContext(t, buildFixtureFiles(), false)
	s := store.At(filepath.Join(t.TempDir(), "store"))

	buildInto(t, s, buildOptions{contextDir: dir, buildArgs: []string{"language=Rust"}})

	layer := layerOf(t, s, "reviewer:2.0.0")
	assert.Contains(t, layer["reviewer/SKILL.md"], "language: Rust")
}

func TestBuildArgMustBeKeyValue(t *testing.T) {
	_, err := parseBuildArgs([]string{"nope"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not k=v")

	args, err := parseBuildArgs([]string{"language=", "model=opus"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"language": "", "model": "opus"}, args,
		"an empty value is how an ARG default is suppressed")
}

// SPEC.md 8.7: -f names the Skillfile, and -t names the tag.
func TestBuildHonoursFileAndTagFlags(t *testing.T) {
	files := buildFixtureFiles()
	files["recipes/Other"] = "FROM ./base\nSET description \"a second recipe\"\n"
	dir := writeContext(t, files, false)
	s := store.At(filepath.Join(t.TempDir(), "store"))

	stdout, _ := buildInto(t, s, buildOptions{
		contextDir: dir,
		skillfile:  filepath.Join(dir, "recipes", "Other"),
		tag:        "reviewer:9.9.9",
	})
	assert.Equal(t, "reviewer:9.9.9", strings.Fields(stdout)[0])

	layer := layerOf(t, s, "reviewer:9.9.9")
	assert.Contains(t, layer["reviewer/SKILL.md"], "a second recipe")
	assert.Contains(t, layer["reviewer/SKILL.md"], "model: sonnet",
		"the other Skillfile's edits belong to the other Skillfile")
}

func TestBuildFailsWithoutASkillfile(t *testing.T) {
	s := store.At(filepath.Join(t.TempDir(), "store"))
	var out, warn bytes.Buffer
	err := runBuild(context.Background(), &out, &warn, s,
		buildOptions{contextDir: t.TempDir()})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read the Skillfile")
}
