package artifact

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillDir writes a skill directory and returns its path.
func skillDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const minimalSkill = "---\nname: hello\ndescription: says hello\n---\n\n# Hello\n"

// headers reads back the tar entries, so assertions are about the artifact
// rather than about our own writer.
func headers(t *testing.T, layer []byte) []*tar.Header {
	t.Helper()
	gr, err := gzip.NewReader(strings.NewReader(string(layer)))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer func() { _ = gr.Close() }()

	var out []*tar.Header
	tr := tar.NewReader(gr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		out = append(out, h)
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
	if err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	var names []string
	for _, h := range headers(t, layer) {
		names = append(names, h.Name)
		if !strings.HasPrefix(h.Name, "hello/") {
			t.Errorf("entry %q is not rooted at the skill name", h.Name)
		}
	}

	for _, want := range []string{"hello/", "hello/SKILL.md", "hello/sections/detail.md"} {
		if !contains(names, want) {
			t.Errorf("entry %q missing from %v", want, names)
		}
	}
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
	if err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	// A second directory, written in a different order, with different source
	// permissions and mtimes: none of that may reach the digest.
	dir := skillDir(t, files)
	for name := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	second, err := PackDir(dir, "hello")
	if err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	if string(first) != string(second) {
		t.Error("identical inputs produced different layers; 2.4 requires identical digests")
	}
}

// The normalisation 2.4 asks for, asserted on the archive itself.
func TestEntriesAreNormalised(t *testing.T) {
	dir := skillDir(t, map[string]string{
		"SKILL.md":           minimalSkill,
		"sections/nested.md": "nested\n",
	})

	layer, err := PackDir(dir, "hello")
	if err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	for _, h := range headers(t, layer) {
		if h.ModTime.Unix() != 0 {
			t.Errorf("%s has mtime %v, want the Unix epoch", h.Name, h.ModTime)
		}
		if h.Uid != 0 || h.Gid != 0 {
			t.Errorf("%s has uid/gid %d/%d, want 0/0", h.Name, h.Uid, h.Gid)
		}
		want := int64(fileMode)
		if h.Typeflag == tar.TypeDir {
			want = dirMode
		}
		if h.Mode != want {
			t.Errorf("%s has mode %o, want %o", h.Name, h.Mode, want)
		}
		if strings.Contains(h.Name, `\`) {
			t.Errorf("%s contains a backslash; 2.5 is forward slashes exclusively", h.Name)
		}
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
	if err != nil {
		t.Fatalf("PackDir: %v", err)
	}

	hs := headers(t, layer)
	if hs[0].Name != "hello/" {
		t.Errorf("first entry = %q, want the root directory", hs[0].Name)
	}
	for i := 2; i < len(hs); i++ {
		if hs[i-1].Name > hs[i].Name {
			t.Errorf("entries out of order: %q before %q", hs[i-1].Name, hs[i].Name)
		}
	}
}

// SPEC.md 2.5: symlinks are rejected at pack.
func TestSymlinksAreRejected(t *testing.T) {
	dir := skillDir(t, map[string]string{"SKILL.md": minimalSkill})
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "leak.md")); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	if _, err := PackDir(dir, "hello"); err == nil {
		t.Error("PackDir accepted a symlink, want an error")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error = %v, want it to name the symlink", err)
	}
}

// A skill without SKILL.md has no frontmatter to derive a config from.
func TestSkillFileIsRequired(t *testing.T) {
	if _, err := PackDir(skillDir(t, map[string]string{"other.md": "x\n"}), "hello"); err == nil {
		t.Error("PackDir accepted a directory with no SKILL.md, want an error")
	}
}

// checkPath is the security boundary of 2.5; the rejected set is exact, and so
// is the accepted set.
func TestCheckPath(t *testing.T) {
	rejected := []string{"../escape.md", "a/../../b.md", "/absolute.md", "./a.md", "a//b.md"}
	for _, p := range rejected {
		t.Run("reject "+p, func(t *testing.T) {
			if err := checkPath(p); err == nil {
				t.Errorf("checkPath(%q) = nil, want an error", p)
			}
		})
	}

	// 2.5 lists these as explicitly *not* validated.
	accepted := []string{"CON", "aux.md", `a:b.md`, "trailing.", "trailing ", "a?b.md", "README.md", "a/b/c.md"}
	for _, p := range accepted {
		t.Run("accept "+p, func(t *testing.T) {
			if err := checkPath(p); err != nil {
				t.Errorf("checkPath(%q) = %v, want nil — 2.5 does not validate this", p, err)
			}
		})
	}
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
