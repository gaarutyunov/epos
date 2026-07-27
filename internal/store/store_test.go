package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/rogpeppe/go-internal/lockedfile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
)

// push writes a one-layer skill artifact and tags it.
func push(t *testing.T, s *Store, tag, body string) ocispec.Descriptor {
	t.Helper()
	ctx := context.Background()

	var manifest ocispec.Descriptor
	err := s.Push(ctx, tag, func(ctx context.Context, st *oci.Store) (ocispec.Descriptor, error) {
		layer, err := oras.PushBytes(ctx, st,
			"application/vnd.agentskills.skill.content.v1.tar+gzip", []byte(body))
		if err != nil {
			return ocispec.Descriptor{}, err
		}
		manifest, err = oras.PackManifest(ctx, st, oras.PackManifestVersion1_1,
			"application/vnd.agentskills.skill.v1", oras.PackManifestOptions{
				Layers: []ocispec.Descriptor{layer},
			})
		return manifest, err
	})
	require.NoError(t, err, "Push(%s)", tag)
	return manifest
}

func TestPushResolveRoundTrip(t *testing.T) {
	s := Under(t.TempDir())
	want := push(t, s, "hello:1.0.0", "layer one")

	got, err := s.Resolve(context.Background(), "hello:1.0.0")
	require.NoError(t, err)
	assert.Equal(t, want.Digest, got.Digest)
}

// SPEC.md 9.1: many versions stay resident so any can be installed later.
func TestManyVersionsCoexist(t *testing.T) {
	s := Under(t.TempDir())
	push(t, s, "hello:1.0.0", "one")
	push(t, s, "hello:1.1.0", "two")
	push(t, s, "other:0.1.0", "three")

	tags, err := s.Tags(context.Background())
	require.NoError(t, err)
	assert.Subset(t, tags, []string{"hello:1.0.0", "hello:1.1.0", "other:0.1.0"})
}

// The reason this package exists (SPEC.md 9.2): oras-go's store is
// single-process, so two writers would silently lose each other's tags. Under
// the lock, every tag must survive.
func TestConcurrentPushesDoNotLoseTags(t *testing.T) {
	s := Under(t.TempDir())

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			err := s.Push(ctx, fmt.Sprintf("skill:%d.0.0", i),
				func(ctx context.Context, st *oci.Store) (ocispec.Descriptor, error) {
					layer, err := oras.PushBytes(ctx, st,
						"application/vnd.agentskills.skill.content.v1.tar+gzip",
						[]byte(fmt.Sprintf("layer %d", i)))
					if err != nil {
						return ocispec.Descriptor{}, err
					}
					return oras.PackManifest(ctx, st, oras.PackManifestVersion1_1,
						"application/vnd.agentskills.skill.v1", oras.PackManifestOptions{
							Layers: []ocispec.Descriptor{layer},
						})
				})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "concurrent push")
	}

	tags, err := s.Tags(context.Background())
	require.NoError(t, err)
	assert.Len(t, tags, writers,
		"a concurrent push was lost; that is the failure 9.2 describes")
}

// 9.2: index.json is replaced, never rewritten in place, so a reader never
// sees a partial file.
func TestIndexIsValidJSONAfterEveryWrite(t *testing.T) {
	s := Under(t.TempDir())
	root := s.Path()

	for i := 0; i < 5; i++ {
		push(t, s, fmt.Sprintf("skill:%d.0.0", i), fmt.Sprintf("body %d", i))

		body, err := os.ReadFile(filepath.Join(root, "index.json"))
		require.NoError(t, err)

		var idx ocispec.Index
		require.NoError(t, json.Unmarshal(body, &idx),
			"index.json is not valid JSON after write %d", i)
		assert.Equal(t, 2, idx.SchemaVersion)
		for _, m := range idx.Manifests {
			assert.NotEmpty(t, m.Annotations[ocispec.AnnotationRefName],
				"manifest %s carries no ref name annotation", m.Digest)
		}
	}

	// No temp files left behind.
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	for _, e := range entries {
		assert.NotEqual(t, ".tmp", filepath.Ext(e.Name()),
			"temp file %s was left in the store", e.Name())
	}
}

// A store that has never been written must still be readable.
func TestResolveOnEmptyStore(t *testing.T) {
	s := Under(t.TempDir())

	_, err := s.Resolve(context.Background(), "absent:1.0.0")
	assert.Error(t, err)

	tags, err := s.Tags(context.Background())
	require.NoError(t, err)
	assert.Empty(t, tags)
}

// SPEC.md 9.3: prune is mark-and-sweep from the tagged manifests. Anything a
// tag reaches survives; anything else goes.
func TestPruneKeepsReachableBlobsAndSweepsTheRest(t *testing.T) {
	s := Under(t.TempDir())
	root := s.Path()
	push(t, s, "hello:1.0.0", "reachable")

	blobs := filepath.Join(root, "blobs", "sha256")
	before, err := os.ReadDir(blobs)
	require.NoError(t, err)

	// An orphan: on disk, reachable from no tag.
	require.NoError(t, os.WriteFile(filepath.Join(blobs, "deadbeef"), []byte("orphan"), 0o644))

	removed, err := s.Prune(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, removed)

	after, err := os.ReadDir(blobs)
	require.NoError(t, err)
	assert.Len(t, after, len(before), "every blob a tag reaches must survive")

	// The tag still resolves, so the sweep did not take anything live.
	_, err = s.Resolve(context.Background(), "hello:1.0.0")
	assert.NoError(t, err)
}

// A read-only blob must still be removable: 9.3 calls out the defect that
// makes rm -rf on GOMODCACHE fail.
func TestPruneRemovesReadOnlyBlobs(t *testing.T) {
	s := Under(t.TempDir())
	root := s.Path()
	push(t, s, "hello:1.0.0", "reachable")

	orphan := filepath.Join(root, "blobs", "sha256", "readonlyorphan")
	require.NoError(t, os.WriteFile(orphan, []byte("orphan"), 0o444))

	removed, err := s.Prune(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, removed)
	assert.NoFileExists(t, orphan)
}

// SPEC.md 9.2: shared for fetch, resolve and install-into-worktree, so
// parallel worktree installs do not serialise.
//
// Asserted by making the two readers wait for each other. Under a shared lock
// both are inside at once and each one's signal releases the other; under an
// exclusive one the first would hold the lock while waiting for a second that
// cannot enter, and neither would ever arrive.
func TestSharedReadsDoNotSerialise(t *testing.T) {
	s := Under(t.TempDir())
	push(t, s, "hello:1.0.0", "layer one")

	inside := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	done := make(chan error, 2)
	for i := range inside {
		go func() {
			done <- s.Read(context.Background(), func(context.Context, *oci.Store) error {
				close(inside[i])
				select {
				case <-inside[1-i]:
					return nil
				case <-time.After(30 * time.Second):
					return fmt.Errorf("reader %d was alone in the store; the lock serialised", i)
				}
			})
		}()
	}
	for range inside {
		require.NoError(t, <-done)
	}
}

// The other half of the discipline: shared does not mean absent. A reader must
// still wait behind a writer, or the atomic index write has nothing to be
// atomic against.
func TestASharedReadWaitsForAnExclusiveHolder(t *testing.T) {
	root := t.TempDir()
	s := Under(root)
	push(t, s, "hello:1.0.0", "layer one")

	// The same file Store takes, taken the way Store takes it for a write.
	held, err := lockedfile.OpenFile(filepath.Join(root, lockName),
		os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)

	read := make(chan error, 1)
	go func() {
		read <- s.Read(context.Background(), func(context.Context, *oci.Store) error { return nil })
	}()

	select {
	case err := <-read:
		_ = held.Close()
		t.Fatalf("the read entered the store while a writer held it exclusively: %v", err)
	case <-time.After(250 * time.Millisecond):
	}

	require.NoError(t, held.Close())
	select {
	case err := <-read:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the read never entered the store after the writer let go")
	}
}
