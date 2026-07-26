package artifact

import (
	"bytes"
	"context"
	// go-digest resolves algorithms through a registry that each hash
	// populates in its init, so a binary that never imports sha256 elsewhere
	// panics on the first digest. Blank-importing it here keeps that the
	// artifact package's problem rather than every caller's.
	_ "crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
)

// Skill is a packed skill, ready to be tagged.
type Skill struct {
	Config   Config
	Manifest ocispec.Descriptor
}

// Build packs dir and writes the artifact into target, returning its manifest.
//
// The result has exactly one separately-fetchable blob (SPEC.md 2.1): the
// content layer. The config blob is carried inline in its descriptor's data
// field, so a client that only wants the frontmatter — `epos search`, a
// discovery UI — reads it out of the manifest without a second round trip.
//
// The manifest is assembled here rather than by oras.PackManifest, which
// stamps org.opencontainers.image.created with the current time. That would
// make the manifest digest differ on every pack and break 2.4 for the artifact
// as a whole, even with a byte-identical layer.
func Build(ctx context.Context, target content.Storage, dir string) (Skill, error) {
	src, err := os.ReadFile(filepath.Join(dir, SkillFile))
	if err != nil {
		return Skill{}, fmt.Errorf("read %s: %w", SkillFile, err)
	}

	cfg, err := ParseFrontmatter(src)
	if err != nil {
		return Skill{}, err
	}

	layer, err := PackDir(dir, cfg.Name())
	if err != nil {
		return Skill{}, err
	}
	layerDesc, err := push(ctx, target, ContentMediaType, layer)
	if err != nil {
		return Skill{}, fmt.Errorf("write content layer: %w", err)
	}

	configJSON, err := cfg.JSON()
	if err != nil {
		return Skill{}, err
	}
	// Pushed as well as inlined: a conforming client is entitled to fetch the
	// config by digest, and 2.1's "exactly one separately-fetchable blob" is
	// about what a pull needs to fetch, not about withholding it.
	configDesc, err := push(ctx, target, ConfigMediaType, configJSON)
	if err != nil {
		return Skill{}, fmt.Errorf("write config blob: %w", err)
	}
	configDesc.Data = configJSON

	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config:       configDesc,
		Layers:       []ocispec.Descriptor{layerDesc},
		Annotations: map[string]string{
			ocispec.AnnotationTitle:       cfg.Name(),
			ocispec.AnnotationDescription: cfg.Description(),
		},
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return Skill{}, fmt.Errorf("encode manifest: %w", err)
	}
	manifestDesc, err := push(ctx, target, ocispec.MediaTypeImageManifest, body)
	if err != nil {
		return Skill{}, fmt.Errorf("write manifest: %w", err)
	}
	manifestDesc.ArtifactType = ArtifactType
	manifestDesc.Annotations = manifest.Annotations

	return Skill{Config: cfg, Manifest: manifestDesc}, nil
}

// push writes data unless the store already holds it.
//
// Content addressing makes a repeat push a no-op by definition, so
// ErrAlreadyExists is the expected outcome of packing an unchanged skill twice
// — which 9.1's "build once, keep many versions resident" depends on — not a
// failure.
func push(ctx context.Context, target content.Storage,
	mediaType string, data []byte) (ocispec.Descriptor, error) {
	desc := content.NewDescriptorFromBytes(mediaType, data)
	err := target.Push(ctx, desc, bytes.NewReader(data))
	if err != nil && !errors.Is(err, errdef.ErrAlreadyExists) {
		return ocispec.Descriptor{}, err
	}
	return desc, nil
}
