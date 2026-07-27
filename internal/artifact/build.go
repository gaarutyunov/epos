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
	return assemble(ctx, target, src, func(name string) ([]byte, error) {
		return PackDir(dir, name)
	}, nil)
}

// BuildFiles is Build for a skill that only exists in memory: the result of a
// Skillfile (SPEC.md 8.7), which the evaluator never writes to disk.
//
// Same config derivation, same manifest and same layer writer as Build. The two
// differ in where the bytes come from and in nothing else, because a build and
// a pack of the same skill must produce the same digest — 2.4 is a property of
// the inputs, not of which command was run.
//
// annotations carry provenance (2.3). They are additional: the title and the
// description come from the frontmatter and are not the caller's to contradict.
func BuildFiles(ctx context.Context, target content.Storage,
	files map[string][]byte, annotations map[string]string) (Skill, error) {
	src, ok := files[SkillFile]
	if !ok {
		return Skill{}, fmt.Errorf("the build produced no %s", SkillFile)
	}
	return assemble(ctx, target, src, func(name string) ([]byte, error) {
		return PackFiles(files, name)
	}, annotations)
}

// assemble derives the config from SKILL.md, packs the layer with pack, and
// writes the manifest.
func assemble(ctx context.Context, target content.Storage, src []byte,
	pack func(name string) ([]byte, error), extra map[string]string) (Skill, error) {
	cfg, err := ParseFrontmatter(src)
	if err != nil {
		return Skill{}, err
	}

	layer, err := pack(cfg.Name())
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

	// encoding/json sorts map keys, so which order the annotations were added
	// in never reaches the manifest bytes and therefore never reaches the
	// digest (2.4).
	annotations := map[string]string{
		ocispec.AnnotationTitle:       cfg.Name(),
		ocispec.AnnotationDescription: cfg.Description(),
	}
	for k, v := range extra {
		if _, derived := annotations[k]; derived || v == "" {
			continue
		}
		annotations[k] = v
	}

	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config:       configDesc,
		Layers:       []ocispec.Descriptor{layerDesc},
		Annotations:  annotations,
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
	manifestDesc.Annotations = annotations

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
