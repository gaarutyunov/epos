package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestFile is the human-authored declaration of desired skills (SPEC.md
// 10.2). A worktree that has never installed anything does not have one.
const ManifestFile = "skills.json"

// LockFile is the digest-pinned resolution of ManifestFile, and the
// per-worktree version pin (10.2).
const LockFile = "skills.lock.json"

// DefaultBasePath is where a skill installs unless skills.json names more
// (10.2). Written with forward slashes because it is a JSON value before it is
// a path: FromSlash turns it into one at the moment it touches the filesystem.
const DefaultBasePath = ".claude/skills"

// lockVersion is the shape of the lock this build writes, so a future change
// of shape has something to branch on rather than having to guess.
const lockVersion = 1

// Manifest is skills.json: what the worktree asked for, and where it wants it.
type Manifest struct {
	// Skills are the declarations, sorted by name so the file does not churn.
	Skills []Declared `json:"skills"`
	// AdditionalBasePaths are the install targets beyond DefaultBasePath, for
	// worktrees that feed more than one agent vendor (10.2). Order is the
	// author's and is left alone.
	AdditionalBasePaths []string `json:"additionalBasePaths,omitempty"`
}

// Declared is one entry of skills.json.
type Declared struct {
	Name string `json:"name"`
	// Ref is what the user asked for, exactly as written: a store tag, or the
	// registry reference it was pulled from. Kept verbatim so the declaration
	// still says where the skill came from.
	Ref string `json:"ref"`
}

// Lock is skills.lock.json: what was actually resolved, pinned by digest.
type Lock struct {
	LockfileVersion int `json:"lockfileVersion"`
	// Skills are sorted by name. A lock whose entry order came from a Go map
	// would be rewritten on every install and show up in every diff.
	Skills []Locked `json:"skills"`
}

// Locked is one pinned skill.
type Locked struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Ref     string `json:"ref"`
	// Digest is the pin: the manifest digest this worktree is on, whatever the
	// store happens to hold under the tag later.
	Digest string `json:"digest"`
	// BasePaths are the directories the skill was written into, relative to
	// the worktree and slash-separated so the file is byte-identical on
	// Windows and on Linux. Recorded rather than recomputed, so uninstall
	// removes what install created even if skills.json has moved on since.
	BasePaths []string `json:"basePaths"`
}

// LoadManifest reads dir's skills.json. A worktree without one gets an empty
// manifest rather than an error: `epos install` in a fresh worktree is the
// ordinary case, not a mistake.
func LoadManifest(dir string) (*Manifest, error) {
	m := &Manifest{}
	if err := readJSON(filepath.Join(dir, ManifestFile), m); err != nil {
		return nil, err
	}
	return m, nil
}

// Save writes skills.json.
func (m *Manifest) Save(dir string) error {
	sort.Slice(m.Skills, func(i, j int) bool { return m.Skills[i].Name < m.Skills[j].Name })
	return writeJSON(filepath.Join(dir, ManifestFile), m)
}

// BasePaths is every directory an install writes into: the default first, then
// whatever skills.json adds (10.2).
//
// Duplicates are dropped. Naming .claude/skills in additionalBasePaths is a
// reasonable thing for an author to do, and installing the same skill into the
// same directory twice is not.
func (m *Manifest) BasePaths() []string {
	out := []string{DefaultBasePath}
	seen := map[string]bool{DefaultBasePath: true}
	for _, p := range m.AdditionalBasePaths {
		p = strings.TrimSuffix(filepath.ToSlash(p), "/")
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// Declare records a skill in the manifest, replacing any earlier declaration
// of the same name.
func (m *Manifest) Declare(d Declared) {
	for i, existing := range m.Skills {
		if existing.Name == d.Name {
			m.Skills[i] = d
			return
		}
	}
	m.Skills = append(m.Skills, d)
}

// Undeclare drops a skill, reporting whether it was there.
func (m *Manifest) Undeclare(name string) bool {
	for i, existing := range m.Skills {
		if existing.Name == name {
			m.Skills = append(m.Skills[:i], m.Skills[i+1:]...)
			return true
		}
	}
	return false
}

// LoadLock reads dir's skills.lock.json, empty if there is none.
func LoadLock(dir string) (*Lock, error) {
	l := &Lock{LockfileVersion: lockVersion}
	if err := readJSON(filepath.Join(dir, LockFile), l); err != nil {
		return nil, err
	}
	if l.LockfileVersion == 0 {
		l.LockfileVersion = lockVersion
	}
	return l, nil
}

// Save writes skills.lock.json.
//
// The entries are sorted and the fields are a struct, so the same set of
// installed skills always serialises to the same bytes. A lock assembled by
// ranging a map would differ between two runs that installed the same things,
// which is the difference between a pin and a file that merely happens to hold
// the right digests.
func (l *Lock) Save(dir string) error {
	l.LockfileVersion = lockVersion
	sort.Slice(l.Skills, func(i, j int) bool { return l.Skills[i].Name < l.Skills[j].Name })
	return writeJSON(filepath.Join(dir, LockFile), l)
}

// Pin records a resolution, replacing any earlier pin of the same name.
func (l *Lock) Pin(entry Locked) {
	for i, existing := range l.Skills {
		if existing.Name == entry.Name {
			l.Skills[i] = entry
			return
		}
	}
	l.Skills = append(l.Skills, entry)
}

// Unpin drops a pin and returns it.
func (l *Lock) Unpin(name string) (Locked, bool) {
	for i, existing := range l.Skills {
		if existing.Name == name {
			l.Skills = append(l.Skills[:i], l.Skills[i+1:]...)
			return existing, true
		}
	}
	return Locked{}, false
}

// readJSON decodes path into v, treating an absent file as nothing to read.
func readJSON(path string, v any) error {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("parse %s: %w", filepath.Base(path), err)
	}
	return nil
}

// writeJSON renders v as indented JSON with a trailing newline.
//
// Indented and newline-terminated because both files are meant to be read,
// reviewed and committed: skills.json is hand-edited and skills.lock.json is
// the pin a reviewer checks.
func writeJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Base(path), err)
	}
	return nil
}
