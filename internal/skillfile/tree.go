package skillfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gaarutyunov/epos/internal/registry"
)

// Tree is the skill being built: path → file contents, slash-separated and
// rooted at the skill directory.
//
// Held in memory rather than on disk because a build is a pure function
// (SPEC.md 8.1) — nothing executes, files are kilobytes of Markdown and YAML,
// and keeping the whole tree addressable makes the later-wins rule of 8.2 a
// map write rather than a filesystem dance.
type Tree struct {
	files map[string][]byte
}

// NewTree returns an empty tree.
func NewTree() *Tree { return &Tree{files: map[string][]byte{}} }

// LoadDir reads a directory into a tree.
//
// The same path rules as packing apply (2.5): forward slashes, no absolute
// paths, no `..`, no symlinks. A base that could escape its root at build time
// would escape it at install time too.
func LoadDir(dir string) (*Tree, error) {
	t := NewTree()

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		slash := filepath.ToSlash(rel)
		if err := checkPath(slash); err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s: symlinks are not allowed in a skill", slash)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: only regular files can be built from", slash)
		}

		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		t.files[slash] = body
		return nil
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// Clone copies the tree, so a stage can be consumed without mutating it.
//
// 8.4 composes stages by explicit COPY --from, and a stage stays available to
// later stages, so each FROM starts from its own copy.
func (t *Tree) Clone() *Tree {
	out := NewTree()
	for p, body := range t.files {
		out.files[p] = append([]byte(nil), body...)
	}
	return out
}

// Paths returns every file path, sorted.
func (t *Tree) Paths() []string {
	out := make([]string, 0, len(t.files))
	for p := range t.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Files returns every file, keyed by its slash-separated path.
//
// A copy, and a deep one: the packer is handed the result of a build, and a map
// sharing the tree's own slices would let it observe — or be observed by — a
// tree the caller still holds. The paths are slash-separated on every platform
// (2.5), which is also what an OCI layer entry name has to be.
func (t *Tree) Files() map[string][]byte {
	out := make(map[string][]byte, len(t.files))
	for p, body := range t.files {
		out[p] = append([]byte(nil), body...)
	}
	return out
}

// Get returns a file's contents.
func (t *Tree) Get(p string) ([]byte, bool) {
	body, ok := t.files[p]
	return body, ok
}

// Set writes a file, creating or replacing it.
func (t *Tree) Set(p string, body []byte) error {
	if err := checkPath(p); err != nil {
		return err
	}
	t.files[p] = body
	return nil
}

// Remove deletes a file or, if p names a directory prefix, everything under
// it. Reports how many files went.
//
// A directory is not a thing the tree holds — only files are — so RM on a
// prefix removing its contents is what a user means by `RM sections/`.
func (t *Tree) Remove(p string) int {
	if _, ok := t.files[p]; ok {
		delete(t.files, p)
		return 1
	}

	prefix := strings.TrimSuffix(p, "/") + "/"
	removed := 0
	for existing := range t.files {
		if strings.HasPrefix(existing, prefix) {
			delete(t.files, existing)
			removed++
		}
	}
	return removed
}

// checkPath rejects what 2.5 rejects, so a Skillfile cannot write outside the
// skill it is building.
//
// The rules live in internal/registry, with the fetch-and-untar that moved
// there: that routine reads somebody else's artifact and the Skillfile path
// reads somebody else's base, so the two need the same guard and a second copy
// is how one of them loses a rule.
func checkPath(slash string) error { return registry.CheckPath(slash) }
