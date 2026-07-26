package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skillDir writes a skill directory and returns its path.
func skillDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	}
	return dir
}

const minimalSkill = "---\nname: hello\ndescription: says hello\n---\n\n# Hello\n"

// headers reads back the tar entries, so assertions are about the artifact
// rather than about our own writer.
func headers(t *testing.T, layer []byte) []*tar.Header {
	t.Helper()
	gr, err := gzip.NewReader(bytes.NewReader(layer))
	require.NoError(t, err)
	defer func() { _ = gr.Close() }()

	var out []*tar.Header
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		out = append(out, h)
	}
	return out
}

func names(hs []*tar.Header) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Name)
	}
	return out
}

// SPEC.md 2.1: the layer is rooted at <skill-name>/ and extracts to something
// indistinguishable from a hand-authored directory.
func TestLayerIsRootedAtTheSkillName(t *testing.T) {
	dir := skillDir(t, map[string]string{
		"SKILL.md":            minimalSkill,
		"sections/detail.md":  "detail\n",
		"reference/notes.txt": "notes\n",
	})

	layer, err := PackDir(dir, "hello")
	require.NoError(t, err)

	got := names(headers(t, layer))
	for _, name := range got {
		assert.True(t, filepath.ToSlash(name)[:6] == "hello/",
			"entry %q is not rooted at the skill name", name)
	}
	assert.Subset(t, got, []string{"hello/", "hello/SKILL.md", "hello/sections/detail.md"})
}

// SPEC.md 2.4: packing is a pure function of its inputs.
func TestPackingIsDeterministic(t *testing.T) {
	files := map[string]string{
		"SKILL.md":           minimalSkill,
		"b.md":               "b\n",
		"a.md":               "a\n",
		"sections/nested.md": "nested\n",
	}

	first, err := PackDir(skillDir(t, files), "hello")
	require.NoError(t, err)

	// A second directory, written in a different order and with different
	// source permissions: none of that may reach the digest.
	dir := skillDir(t, files)
	for name := range files {
		require.NoError(t, os.Chmod(filepath.Join(dir, filepath.FromSlash(name)), 0o755))
	}
	second, err := PackDir(dir, "hello")
	require.NoError(t, err)

	assert.Equal(t, first, second,
		"identical inputs produced different layers; 2.4 requires identical digests")
}

// The normalisation 2.4 asks for, asserted on the archive itself.
func TestEntriesAreNormalised(t *testing.T) {
	dir := skillDir(t, map[string]string{
		"SKILL.md":           minimalSkill,
		"sections/nested.md": "nested\n",
	})

	layer, err := PackDir(dir, "hello")
	require.NoError(t, err)

	for _, h := range headers(t, layer) {
		assert.Zero(t, h.ModTime.Unix(), "%s: mtime must be the Unix epoch", h.Name)
		assert.Zero(t, h.Uid, "%s: uid must be 0", h.Name)
		assert.Zero(t, h.Gid, "%s: gid must be 0", h.Name)

		want := int64(fileMode)
		if h.Typeflag == tar.TypeDir {
			want = dirMode
		}
		assert.Equal(t, want, h.Mode, "%s: mode", h.Name)
		assert.NotContains(t, h.Name, `\`, "2.5 is forward slashes exclusively")
	}
}

// Entries are lexicographic, which is what makes the order reproducible.
func TestEntriesAreLexicographic(t *testing.T) {
	layer, err := PackDir(skillDir(t, map[string]string{
		"SKILL.md": minimalSkill,
		"z.md":     "z\n",
		"a.md":     "a\n",
		"m/n.md":   "n\n",
	}), "hello")
	require.NoError(t, err)

	got := names(headers(t, layer))
	require.NotEmpty(t, got)
	assert.Equal(t, "hello/", got[0], "the root directory comes first")

	assert.IsNonDecreasing(t, got[1:], "entries must be lexicographic")
}

// SPEC.md 2.5: symlinks are rejected at pack.
func TestSymlinksAreRejected(t *testing.T) {
	dir := skillDir(t, map[string]string{"SKILL.md": minimalSkill})
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "leak.md")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	_, err := PackDir(dir, "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
}

// A skill without SKILL.md has no frontmatter to derive a config from.
func TestSkillFileIsRequired(t *testing.T) {
	_, err := PackDir(skillDir(t, map[string]string{"other.md": "x\n"}), "hello")
	assert.Error(t, err)
}

// checkPath is the security boundary of 2.5; the rejected set is exact, and so
// is the accepted set.
func TestCheckPath(t *testing.T) {
	for _, p := range []string{"../escape.md", "a/../../b.md", "/absolute.md", "./a.md", "a//b.md"} {
		t.Run("reject "+p, func(t *testing.T) {
			assert.Error(t, checkPath(p))
		})
	}

	// 2.5 lists these as explicitly *not* validated.
	for _, p := range []string{"CON", "aux.md", "a:b.md", "trailing.", "trailing ", "a?b.md", "README.md", "a/b/c.md"} {
		t.Run("accept "+p, func(t *testing.T) {
			assert.NoError(t, checkPath(p), "2.5 does not validate this")
		})
	}
}
