// Hand-authored driven ports extending the model-generated MaterializePort and
// RevisionStore with the read/restore operations the install use cases need
// (SPEC §5.3). Adapters implement these; the application core depends only on
// these interfaces (the Dependency Rule).

package out

// OverlayPin records an overlay applied at install, pinned by digest (SPEC §9.7).
type OverlayPin struct {
	Name   string
	Digest string
}

// RevisionInfo is a neutral view of a stored revision bundle.
type RevisionInfo struct {
	Number   int
	Version  string
	Digest   string
	Registry string
	Values   map[string]any
	Overlays []OverlayPin
	Files    map[string][]byte
}

// RevisionSpec is the complete data recorded for one revision so rollback
// restores the entire previously installed bundle (SPEC §5.3): version+digest,
// the source registry ref, the exact resolved values, the pinned overlays, and
// the materialized file snapshot.
type RevisionSpec struct {
	Version  string
	Digest   string
	Registry string
	Values   map[string]any
	Overlays []OverlayPin
	Files    map[string][]byte
}

// RevisionRepository is the read/append side of the revision-history backend
// (lockfile, in-cluster ConfigMap/Secret, or PostgreSQL — SPEC §5.4, §11).
type RevisionRepository interface {
	RevisionStore
	// Append records one revision bundle and returns its assigned number.
	Append(release, target, namespace string, spec RevisionSpec) (int, error)
	// History returns the retained revisions of a release (oldest first).
	History(release, target, namespace string) ([]RevisionInfo, error)
	// Get returns a specific retained revision.
	Get(release, target, namespace string, number int) (RevisionInfo, error)
	// Delete removes a release's revision history.
	Delete(release, target, namespace string) error
}

// Materializer is the write side of the materialization backend (files or
// mountable ConfigMap(s) — SPEC §14). It extends the model-generated
// MaterializePort with file access for revision snapshots and restore.
type Materializer interface {
	MaterializePort
	// LastFiles returns the file set from the most recent Materialize call.
	LastFiles() map[string][]byte
	// LastDigest returns the manifest digest from the most recent Materialize call.
	LastDigest() string
	// LastValues returns the effective merged values from the most recent
	// Materialize call (for the revision snapshot, SPEC §5.3).
	LastValues() map[string]any
	// Write materializes a file set to a target (used by rollback restore).
	Write(release, target, namespace string, files map[string][]byte) error
	// Remove deletes a release's materialized files (uninstall).
	Remove(release, target, namespace string) error
}
