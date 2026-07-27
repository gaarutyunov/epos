// Package store is the local OCI Image Layout of SPEC.md 9, plus the
// concurrency the layout specification leaves out.
//
// The OCI Image Layout spec says nothing about locking or about mutating
// index.json, and oras-go's content/oci.Store is single-process only: it holds
// sync mutexes, reads index.json once at construction and never re-reads it,
// and SaveIndex rewrites the file in place. Two processes silently lose each
// other's tags, and a crash mid-write truncates the index.
//
// So this package supplies both missing pieces, the way the Go toolchain does:
// advisory file locking around every access, and index writes that go through
// a temp file and a rename. Every operation opens the store *inside* the lock,
// because a store opened outside it would hold a stale index.
package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rogpeppe/go-internal/lockedfile"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/oci"
)

// lockName is the lock file, kept beside the layout rather than inside it so
// it is never mistaken for part of the OCI layout.
const lockName = "store.lock"

// Store is a handle on the layout directory. It holds no open store and no
// lock: those live for exactly the length of one operation.
//
// Construct one with Under or Default; where the layout lives is root.go's
// single answer, never a path assembled at the call site.
type Store struct {
	root string
}

// Path is where the layout lives; `epos store path` prints it.
func (s *Store) Path() string { return s.root }

// withLock runs fn with the store open under a lock.
//
// exclusive picks the lock mode 9.2 asks for: shared for fetch, resolve and
// install so parallel worktree installs do not serialise, exclusive for push,
// tag, untag and prune.
func (s *Store) withLock(exclusive bool, fn func(*oci.Store) error) error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return fmt.Errorf("create store: %w", err)
	}

	lockPath := filepath.Join(filepath.Dir(s.root), lockName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return fmt.Errorf("create store: %w", err)
	}

	var (
		lock *lockedfile.File
		err  error
	)
	if exclusive {
		lock, err = lockedfile.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644)
	} else {
		lock, err = lockedfile.Open(lockPath)
		if os.IsNotExist(err) {
			// A shared reader on a store that has never been written: create
			// the lock file, then take the shared lock.
			if f, cerr := lockedfile.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o644); cerr == nil {
				_ = f.Close()
			}
			lock, err = lockedfile.Open(lockPath)
		}
	}
	if err != nil {
		return fmt.Errorf("lock store: %w", err)
	}
	defer func() { _ = lock.Close() }()

	// Opened inside the lock, so the index on disk is read fresh (9.2).
	st, err := oci.New(s.root)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	// Epos writes index.json itself, atomically; oras-go's in-place rewrite is
	// what makes a crash mid-write able to truncate it.
	st.AutoSaveIndex = false

	return fn(st)
}

// Push writes an artifact into the store and tags it, under an exclusive lock.
//
// write returns the manifest descriptor to tag: the caller cannot know it
// before its blobs are in the store, and the whole point of holding the lock
// across both steps is that no other process sees the manifest untagged.
func (s *Store) Push(ctx context.Context, tag string,
	write func(context.Context, *oci.Store) (ocispec.Descriptor, error)) error {
	return s.withLock(true, func(st *oci.Store) error {
		manifest, err := write(ctx, st)
		if err != nil {
			return err
		}
		if err := st.Tag(ctx, manifest, tag); err != nil {
			return fmt.Errorf("tag %s: %w", tag, err)
		}
		return s.saveIndex(ctx, st)
	})
}

// Read runs fn against the store under a shared lock, for callers that need
// the store itself rather than one resolved descriptor -- copying an artifact
// out to a registry, say. Shared because 9.2 wants parallel reads not to
// serialise.
func (s *Store) Read(ctx context.Context, fn func(context.Context, *oci.Store) error) error {
	return s.withLock(false, func(st *oci.Store) error { return fn(ctx, st) })
}

// Resolve turns a tag into its descriptor, under a shared lock.
func (s *Store) Resolve(ctx context.Context, tag string) (ocispec.Descriptor, error) {
	var desc ocispec.Descriptor
	err := s.withLock(false, func(st *oci.Store) error {
		var err error
		desc, err = st.Resolve(ctx, tag)
		return err
	})
	return desc, err
}

// Tags lists what the store holds; `epos store ls` prints it.
func (s *Store) Tags(ctx context.Context) ([]string, error) {
	var tags []string
	err := s.withLock(false, func(st *oci.Store) error {
		return st.Tags(ctx, "", func(page []string) error {
			tags = append(tags, page...)
			return nil
		})
	})
	return tags, err
}

// Prune deletes every blob no tagged manifest reaches (SPEC.md 9.3).
//
// Mark and sweep from the tags, under an exclusive lock. Manual only, like the
// Go module cache, pnpm, Cargo and Bazel: there is no reference counting, no
// GC roots, no leases and no worktree liveness tracking, because those exist
// to make *automatic* collection safe and explicit cleanup has nothing to make
// safe.
func (s *Store) Prune(ctx context.Context) (removed int, err error) {
	err = s.withLock(true, func(st *oci.Store) error {
		reachable := map[digest.Digest]bool{}

		// Mark: walk each tagged manifest and everything it references.
		if err := st.Tags(ctx, "", func(page []string) error {
			for _, tag := range page {
				desc, err := st.Resolve(ctx, tag)
				if err != nil {
					return fmt.Errorf("resolve %s: %w", tag, err)
				}
				if err := mark(ctx, st, desc, reachable); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}

		// Sweep: anything on disk the mark never reached.
		blobs := filepath.Join(s.root, "blobs", "sha256")
		entries, err := os.ReadDir(blobs)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if reachable[digest.Digest("sha256:"+e.Name())] {
				continue
			}
			path := filepath.Join(blobs, e.Name())
			// Blobs may have been written read-only for integrity, and
			// removing one then fails the way rm -rf on GOMODCACHE does (9.3).
			_ = os.Chmod(path, 0o644)
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove %s: %w", e.Name(), err)
			}
			removed++
		}
		return nil
	})
	return removed, err
}

// mark records desc and everything reachable from it.
func mark(ctx context.Context, st *oci.Store, desc ocispec.Descriptor,
	seen map[digest.Digest]bool) error {
	if seen[desc.Digest] {
		return nil
	}
	seen[desc.Digest] = true

	successors, err := content.Successors(ctx, st, desc)
	if err != nil {
		// A tag pointing at something the store no longer holds is a broken
		// tag, not a reason to refuse to collect the rest.
		return nil
	}
	for _, next := range successors {
		if err := mark(ctx, st, next, seen); err != nil {
			return err
		}
	}
	return nil
}

// saveIndex writes index.json atomically: temp file, fsync, rename (9.2).
//
// Deliberately not oras-go's SaveIndex, which is the os.WriteFile-in-place
// that 9.2 names as the defect: a crash partway through leaves a truncated
// index and the store unreadable. Rename within a directory is atomic on all
// three platforms, so a reader sees either the previous index or the new one.
//
// The index is rebuilt from the store's tags rather than read out of oras-go,
// which does not expose it. Untagged manifests are dropped, which is the same
// thing prune would do to them: 9.3 marks from tagged manifests, so anything
// untagged is already garbage.
func (s *Store) saveIndex(ctx context.Context, st *oci.Store) error {
	var manifests []ocispec.Descriptor
	err := st.Tags(ctx, "", func(page []string) error {
		for _, tag := range page {
			desc, err := st.Resolve(ctx, tag)
			if err != nil {
				return fmt.Errorf("resolve %s: %w", tag, err)
			}
			if desc.Annotations == nil {
				desc.Annotations = map[string]string{}
			}
			desc.Annotations[ocispec.AnnotationRefName] = tag
			manifests = append(manifests, desc)
		}
		return nil
	})
	if err != nil {
		return err
	}

	return writeJSONAtomic(filepath.Join(s.root, "index.json"), ocispec.Index{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: manifests,
	})
}

// writeJSONAtomic renders v into path via a temp file in the same directory,
// fsyncs it, and renames it over the target.
func writeJSONAtomic(path string, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}

	// Same directory: rename is only atomic within a filesystem.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".index-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp index: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once the rename succeeds

	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp index: %w", err)
	}
	// fsync before rename: the rename can otherwise land while the contents
	// are still only in the page cache, which a crash would lose.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp index: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp index: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", filepath.Base(path), err)
	}
	return nil
}
