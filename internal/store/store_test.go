package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
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
	s := At(filepath.Join(t.TempDir(), "store"))
	want := push(t, s, "hello:1.0.0", "layer one")

	got, err := s.Resolve(context.Background(), "hello:1.0.0")
	require.NoError(t, err)
	assert.Equal(t, want.Digest, got.Digest)
}

// SPEC.md 9.1: many versions stay resident so any can be installed later.
func TestManyVersionsCoexist(t *testing.T) {
	s := At(filepath.Join(t.TempDir(), "store"))
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
	s := At(filepath.Join(t.TempDir(), "store"))

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
	root := filepath.Join(t.TempDir(), "store")
	s := At(root)

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
	s := At(filepath.Join(t.TempDir(), "store"))

	_, err := s.Resolve(context.Background(), "absent:1.0.0")
	assert.Error(t, err)

	tags, err := s.Tags(context.Background())
	require.NoError(t, err)
	assert.Empty(t, tags)
}
