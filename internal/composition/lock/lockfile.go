// Package lock implements Epos.lock: the composition lockfile that records the
// pins captured for pulled layers (OCI + git dependencies and published
// overlays) so composition is reproducible and can be verified (SPEC §9.7).
package lock

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// LockfileName is the on-disk composition lockfile (SPEC §9.6/§9.7).
const LockfileName = "Epos.lock"

// LayerPin is one pulled-layer pin record. Per SPEC §9.7 it carries the layer
// name, kind (skill|overlay), source type (oci|git), the source, the requested
// version/ref, and the content pin: an OCI manifest digest, or a git commit SHA
// plus the git tree object SHA of the subpath.
type LayerPin struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	SourceType string `json:"sourceType"`
	Source     string `json:"source"`
	Version    string `json:"version,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Commit     string `json:"commit,omitempty"`
	TreeSha    string `json:"treeSha,omitempty"`
	Subpath    string `json:"subpath,omitempty"`
}

// Lockfile is the parsed Epos.lock.
type Lockfile struct {
	LockfileVersion int        `json:"lockfileVersion"`
	Layers          []LayerPin `json:"layers"`
}

// New builds a lockfile from resolved layer pins, ordered by name for stable
// diffs.
func New(pins []LayerPin) *Lockfile {
	sorted := append([]LayerPin(nil), pins...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	return &Lockfile{LockfileVersion: 1, Layers: sorted}
}

// Path returns the Epos.lock path within a skill directory.
func Path(skillDir string) string { return filepath.Join(skillDir, LockfileName) }

// Save writes the lockfile to skillDir/Epos.lock as pretty JSON.
func (lf *Lockfile) Save(skillDir string) error {
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(skillDir), append(data, '\n'), 0o644)
}

// Load reads skillDir/Epos.lock, returning (nil, nil) when absent.
func Load(skillDir string) (*Lockfile, error) {
	data, err := os.ReadFile(Path(skillDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var lf Lockfile
	if err := json.Unmarshal(data, &lf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", LockfileName, err)
	}
	return &lf, nil
}

// Verify checks that each recorded pin matches the freshly-resolved pin of the
// same layer; any digest/commit/tree-SHA mismatch is a hard error (SPEC §9.7).
// Resolved pins missing from the lock, or lock entries missing from resolved,
// are reported too.
func (lf *Lockfile) Verify(resolved []LayerPin) error {
	byName := map[string]LayerPin{}
	for _, p := range resolved {
		byName[p.Name] = p
	}
	for _, want := range lf.Layers {
		got, ok := byName[want.Name]
		if !ok {
			return fmt.Errorf("locked layer %q is not present in the resolved stack", want.Name)
		}
		if got.Digest != want.Digest || got.Commit != want.Commit || got.TreeSha != want.TreeSha {
			return fmt.Errorf("pin mismatch for layer %q: lock has {digest:%s commit:%s tree:%s} but resolution gives {digest:%s commit:%s tree:%s}",
				want.Name, want.Digest, want.Commit, want.TreeSha, got.Digest, got.Commit, got.TreeSha)
		}
	}
	return nil
}
