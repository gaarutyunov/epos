// Package lock implements the skills-lock.json lockfile (SPEC §5): digest-pinned,
// bounded, self-contained bundle revision history that gives install
// reproducibility and powers rollback/history/status.
package lock

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"
)

// LockfileName is the on-disk lockfile (SPEC §5.1).
const LockfileName = "skills-lock.json"

// DefaultRetention is the number of revisions retained per release (SPEC §5.3).
const DefaultRetention = 10

// OverlayPin records an overlay applied at install, pinned by digest (SPEC §9.7).
type OverlayPin struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// Revision is a self-contained revision record: the complete bundle so rollback
// restores the entire previously installed state (SPEC §5.3).
type Revision struct {
	Revision    int            `json:"revision"`
	Version     string         `json:"version"`
	Digest      string         `json:"digest"`
	Registry    string         `json:"registry"`
	Values      map[string]any `json:"values,omitempty"`
	Overlays    []OverlayPin   `json:"overlays,omitempty"`
	InstalledAt string         `json:"installedAt"`
	// Files is the materialized file snapshot (base64), making rollback
	// self-contained and offline (SPEC §5.3 "snapshotted in full").
	Files map[string]string `json:"files,omitempty"`
}

// FileBytes decodes the snapshot into a path→bytes map.
func (r *Revision) FileBytes() (map[string][]byte, error) {
	out := make(map[string][]byte, len(r.Files))
	for p, b64 := range r.Files {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("decode file %q: %w", p, err)
		}
		out[p] = data
	}
	return out, nil
}

// SetFiles snapshots a path→bytes map into the revision.
func (r *Revision) SetFiles(files map[string][]byte) {
	r.Files = make(map[string]string, len(files))
	for p, data := range files {
		r.Files[p] = base64.StdEncoding.EncodeToString(data)
	}
}

// ReleaseLock is the per-release lockfile entry (SPEC §5.5).
type ReleaseLock struct {
	Current   int        `json:"current"`
	Revisions []Revision `json:"revisions"`
}

// Lockfile is the parsed skills-lock.json (SPEC §5.5).
type Lockfile struct {
	LockfileVersion int                     `json:"lockfileVersion"`
	Skills          map[string]*ReleaseLock `json:"skills"`

	path      string
	retention int
}

// New returns an empty lockfile bound to path.
func New(path string) *Lockfile {
	return &Lockfile{LockfileVersion: 1, Skills: map[string]*ReleaseLock{}, path: path, retention: DefaultRetention}
}

// Load reads a lockfile, returning an empty one if the file is absent.
func Load(path string) (*Lockfile, error) {
	lf := New(path)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return lf, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, lf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if lf.Skills == nil {
		lf.Skills = map[string]*ReleaseLock{}
	}
	lf.path = path
	lf.retention = DefaultRetention
	return lf, nil
}

// SetRetention overrides the retained-revision count (config-driven, SPEC §5.3).
func (lf *Lockfile) SetRetention(n int) {
	if n > 0 {
		lf.retention = n
	}
}

// Path returns the lockfile's on-disk path.
func (lf *Lockfile) Path() string { return lf.path }

// Save writes the lockfile to disk as pretty JSON.
func (lf *Lockfile) Save() error {
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(lf.path, append(data, '\n'), 0o644)
}

// AddRevision appends a new revision to a release, assigning the next number and
// enforcing bounded retention. Returns the assigned revision number.
func (lf *Lockfile) AddRevision(release string, rev Revision) int {
	rl := lf.Skills[release]
	if rl == nil {
		rl = &ReleaseLock{}
		lf.Skills[release] = rl
	}
	next := 1
	for _, r := range rl.Revisions {
		if r.Revision >= next {
			next = r.Revision + 1
		}
	}
	rev.Revision = next
	if rev.InstalledAt == "" {
		rev.InstalledAt = time.Now().UTC().Format(time.RFC3339)
	}
	rl.Revisions = append(rl.Revisions, rev)
	rl.Current = next
	lf.trim(rl)
	return next
}

// trim keeps only the last N revisions (bounded history, SPEC §5.3).
func (lf *Lockfile) trim(rl *ReleaseLock) {
	if lf.retention <= 0 || len(rl.Revisions) <= lf.retention {
		return
	}
	rl.Revisions = rl.Revisions[len(rl.Revisions)-lf.retention:]
}

// Get returns a specific retained revision of a release.
func (lf *Lockfile) Get(release string, number int) (*Revision, error) {
	rl := lf.Skills[release]
	if rl == nil {
		return nil, fmt.Errorf("release %q not found", release)
	}
	for i := range rl.Revisions {
		if rl.Revisions[i].Revision == number {
			return &rl.Revisions[i], nil
		}
	}
	return nil, fmt.Errorf("release %q: revision %d not retained", release, number)
}

// Current returns the current revision of a release.
func (lf *Lockfile) Current(release string) (*Revision, error) {
	rl := lf.Skills[release]
	if rl == nil {
		return nil, fmt.Errorf("release %q not found", release)
	}
	return lf.Get(release, rl.Current)
}

// History returns the retained revisions of a release, oldest first.
func (lf *Lockfile) History(release string) []Revision {
	rl := lf.Skills[release]
	if rl == nil {
		return nil
	}
	out := append([]Revision(nil), rl.Revisions...)
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out
}

// Has reports whether a release exists in the lockfile.
func (lf *Lockfile) Has(release string) bool { _, ok := lf.Skills[release]; return ok }

// Remove deletes a release from the lockfile (uninstall, unless --keep-history).
func (lf *Lockfile) Remove(release string) { delete(lf.Skills, release) }
