package artifact

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Permissions every entry is normalised to (SPEC.md 2.4). The source
// directory's own modes are deliberately not carried: they differ between a
// clone on Linux and the same clone on Windows, and packing must be a pure
// function of file *contents* and names.
const (
	fileMode = 0o644
	dirMode  = 0o755
)

// PackDir builds the content layer for a skill directory.
//
// The layer is rooted at "<name>/" so that extracting it yields something
// indistinguishable from a hand-authored skill directory (2.1).
func PackDir(dir, name string) ([]byte, error) {
	entries, err := collect(dir, name)
	if err != nil {
		return nil, err
	}
	return writeLayer(entries)
}

// PackFiles builds the content layer for an in-memory file set, keyed by
// slash-separated path.
//
// This is how a Skillfile build reaches the layer (SPEC.md 8.7): the evaluator
// holds its result in memory and never writes it out, so there is no directory
// for PackDir to walk. Deliberately the same entry construction and the same
// writer, because a second packing path is a second answer to "what does this
// skill hash to", and only one of them would be the one 2.4 is tested against.
func PackFiles(files map[string][]byte, name string) ([]byte, error) {
	entries, err := fileEntries(files, name)
	if err != nil {
		return nil, err
	}
	return writeLayer(entries)
}

// fileEntries turns path → contents into tar entries, in lexicographic order
// and with the directory entries a walk of the same tree would have produced.
//
// The paths are sorted before anything reads them. Go randomises map iteration,
// so a loop straight over files would be a way for two builds of identical
// inputs to lay the archive out differently — the exact leak 2.4 rules out, and
// one that no single run can reveal.
func fileEntries(files map[string][]byte, name string) ([]entry, error) {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var entries []entry
	seenDir := map[string]bool{}
	for _, p := range paths {
		if err := checkPath(p); err != nil {
			return nil, err
		}
		for _, dir := range ancestors(p) {
			if seenDir[dir] {
				continue
			}
			seenDir[dir] = true
			entries = append(entries, entry{name: name + "/" + dir, isDir: true})
		}
		entries = append(entries, entry{name: name + "/" + p, body: files[p]})
	}

	if !hasSkillFile(entries, name) {
		return nil, fmt.Errorf("the build produced no %s", SkillFile)
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return append([]entry{{name: name + "/", isDir: true}}, entries...), nil
}

// ancestors lists the directories p lives under, outermost first.
//
// A tar of a directory carries an entry for each directory as well as each
// file, and an in-memory tree holds only files — so they are synthesised here,
// and packing a tree loaded from a directory yields the same archive as packing
// the directory itself.
func ancestors(p string) []string {
	var out []string
	for i := range len(p) {
		if p[i] == '/' {
			out = append(out, p[:i])
		}
	}
	return out
}

// entry is one tar member, already validated and named.
type entry struct {
	name  string // slash-separated, rooted at "<skill>/"
	isDir bool
	body  []byte
}

// collect walks dir and returns its entries in lexicographic order.
//
// Order is fixed here rather than left to the filesystem: readdir order varies
// by filesystem and by platform, and 2.4 requires identical inputs to yield
// identical digests everywhere.
func collect(dir, name string) ([]entry, error) {
	var entries []entry

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

		// Symlinks are rejected rather than followed or stored: a link can
		// name anything on the extracting machine, and 2.5 rules them out at
		// pack and at install alike.
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s: symlinks are not allowed in a skill", slash)
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return fmt.Errorf("%s: only regular files and directories can be packed", slash)
		}

		e := entry{name: name + "/" + slash, isDir: d.IsDir()}
		if !d.IsDir() {
			if e.body, err = os.ReadFile(p); err != nil {
				return err
			}
		}
		entries = append(entries, e)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if !hasSkillFile(entries, name) {
		return nil, fmt.Errorf("%s has no %s", dir, SkillFile)
	}

	// The root directory entry comes first, then everything else by name.
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	return append([]entry{{name: name + "/", isDir: true}}, entries...), nil
}

func hasSkillFile(entries []entry, name string) bool {
	want := name + "/" + SkillFile
	for _, e := range entries {
		if e.name == want {
			return true
		}
	}
	return false
}

// checkPath rejects the entry names 2.5 rules out.
//
// Deliberately no further validation: Windows reserved device names, the
// characters < > : " \ | ? *, trailing dots and spaces, and case-insensitive
// collisions are all accepted. They are legal on Linux, every other tool in
// the ecosystem accepts them, and rejecting them here would refuse skills —
// including third-party bases pulled via FROM — that the author cannot fix.
// Extraction fails at install on platforms that cannot represent them, and
// 2.5 accepts that consequence.
func checkPath(slash string) error {
	switch {
	case slash == "":
		return fmt.Errorf("empty entry name")
	case path.IsAbs(slash):
		return fmt.Errorf("%s: absolute paths are not allowed", slash)
	case strings.HasPrefix(slash, "../") || slash == ".." || strings.Contains(slash, "/../"):
		return fmt.Errorf("%s: .. escapes the skill root", slash)
	}
	// path.Clean collapses anything else that would leave the root.
	if cleaned := path.Clean(slash); cleaned != slash {
		return fmt.Errorf("%s: entry name is not in canonical form (%s)", slash, cleaned)
	}
	return nil
}

// writeLayer renders entries as tar+gzip.
func writeLayer(entries []entry) ([]byte, error) {
	var gzipped bytes.Buffer

	gw, err := gzip.NewWriterLevel(&gzipped, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	// gzip stamps a modification time, a name and an OS byte into its header.
	// Left alone they would make two packs of the same directory differ, so
	// they are pinned: 255 is "unknown", which is what a reproducible build
	// wants rather than whichever OS happened to run pack.
	gw.Header = gzip.Header{OS: 255}

	tw := tar.NewWriter(gw)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Mode:     fileMode,
			Typeflag: tar.TypeReg,
			Size:     int64(len(e.body)),
			// The Unix epoch, not time.Time{}: a zero Time is year 1, which
			// the tar writer clamps on the way out. Saying epoch outright
			// means the header holds what 2.4 asks for rather than whatever
			// the clamp happens to produce.
			ModTime: time.Unix(0, 0).UTC(),
			Uid:     0,
			Gid:     0,
			Format:  tar.FormatPAX,
		}
		if e.isDir {
			hdr.Mode, hdr.Typeflag, hdr.Size = dirMode, tar.TypeDir, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, fmt.Errorf("write %s: %w", e.name, err)
		}
		if !e.isDir {
			if _, err := io.Copy(tw, bytes.NewReader(e.body)); err != nil {
				return nil, fmt.Errorf("write %s: %w", e.name, err)
			}
		}
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return gzipped.Bytes(), nil
}
