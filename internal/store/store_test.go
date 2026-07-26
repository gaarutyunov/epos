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
	if err != nil {
		t.Fatalf("Push(%s): %v", tag, err)
	}
	return manifest
}

func TestPushResolveRoundTrip(t *testing.T) {
	s := At(filepath.Join(t.TempDir(), "store"))
	want := push(t, s, "hello:1.0.0", "layer one")

	got, err := s.Resolve(context.Background(), "hello:1.0.0")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Digest != want.Digest {
		t.Errorf("resolved %s, want %s", got.Digest, want.Digest)
	}
}

// SPEC.md 9.1: many versions stay resident so any can be installed later.
func TestManyVersionsCoexist(t *testing.T) {
	s := At(filepath.Join(t.TempDir(), "store"))
	push(t, s, "hello:1.0.0", "one")
	push(t, s, "hello:1.1.0", "two")
	push(t, s, "other:0.1.0", "three")

	tags, err := s.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	for _, want := range []string{"hello:1.0.0", "hello:1.1.0", "other:0.1.0"} {
		found := false
		for _, got := range tags {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("tag %q missing from %v", want, tags)
		}
	}
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
		t.Fatalf("concurrent push: %v", err)
	}

	tags, err := s.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != writers {
		t.Errorf("store holds %d tags, want %d — a concurrent push was lost: %v",
			len(tags), writers, tags)
	}
}

// 9.2: index.json is replaced, never rewritten in place, so a reader never
// sees a partial file.
func TestIndexIsValidJSONAfterEveryWrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "store")
	s := At(root)

	for i := 0; i < 5; i++ {
		push(t, s, fmt.Sprintf("skill:%d.0.0", i), fmt.Sprintf("body %d", i))

		body, err := os.ReadFile(filepath.Join(root, "index.json"))
		if err != nil {
			t.Fatalf("read index: %v", err)
		}
		var idx ocispec.Index
		if err := json.Unmarshal(body, &idx); err != nil {
			t.Fatalf("index.json is not valid JSON after write %d: %v", i, err)
		}
		if idx.SchemaVersion != 2 {
			t.Errorf("index schemaVersion = %d, want 2", idx.SchemaVersion)
		}
		for _, m := range idx.Manifests {
			if m.Annotations[ocispec.AnnotationRefName] == "" {
				t.Errorf("manifest %s carries no ref name annotation", m.Digest)
			}
		}
	}

	// No temp files left behind.
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file %s was left in the store", e.Name())
		}
	}
}

// A store that has never been written must still be readable.
func TestResolveOnEmptyStore(t *testing.T) {
	s := At(filepath.Join(t.TempDir(), "store"))
	if _, err := s.Resolve(context.Background(), "absent:1.0.0"); err == nil {
		t.Error("Resolve on an empty store returned no error, want one")
	}
	tags, err := s.Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags on an empty store: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("empty store has tags %v", tags)
	}
}
