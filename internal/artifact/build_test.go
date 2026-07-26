package artifact

import (
	"context"
	"encoding/json"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
)

const buildSkill = "---\nname: reviewer\nversion: 1.0.0\ndescription: reviews Go code\n---\n\n# Reviewer\n"

func TestBuildProducesAConformantManifest(t *testing.T) {
	dir := skillDir(t, map[string]string{
		"SKILL.md":              buildSkill,
		"sections/checklist.md": "checklist\n",
	})

	target := memory.New()
	skill, err := Build(context.Background(), target, dir)
	require.NoError(t, err)

	body, err := content.FetchAll(context.Background(), target, skill.Manifest)
	require.NoError(t, err)

	var manifest ocispec.Manifest
	require.NoError(t, json.Unmarshal(body, &manifest))

	assert.Equal(t, ArtifactType, manifest.ArtifactType)
	assert.Equal(t, ConfigMediaType, manifest.Config.MediaType)
	assert.Len(t, manifest.Layers, 1, "2.1: exactly one content layer")
	assert.Equal(t, ContentMediaType, manifest.Layers[0].MediaType)

	// 2.1: the config rides inline, so a client that only wants the
	// frontmatter needs no second round trip.
	assert.NotEmpty(t, manifest.Config.Data, "the config blob must be inlined via data")
	var inlined Config
	require.NoError(t, json.Unmarshal(manifest.Config.Data, &inlined))
	assert.Equal(t, "reviewer", inlined.Name())

	assert.Equal(t, "reviewer", manifest.Annotations[ocispec.AnnotationTitle])
}

// SPEC.md 2.4 has to hold for the whole artifact, not just the layer:
// oras.PackManifest would stamp org.opencontainers.image.created with the
// current time and make the manifest digest differ on every pack.
func TestBuildIsDeterministic(t *testing.T) {
	files := map[string]string{
		"SKILL.md":              buildSkill,
		"sections/checklist.md": "checklist\n",
	}

	first, err := Build(context.Background(), memory.New(), skillDir(t, files))
	require.NoError(t, err)
	second, err := Build(context.Background(), memory.New(), skillDir(t, files))
	require.NoError(t, err)

	assert.Equal(t, first.Manifest.Digest, second.Manifest.Digest,
		"identical inputs must produce an identical manifest digest")
}

// Packing the same skill twice must not fail: content addressing makes the
// second push a no-op, which 9.1's "build once, keep many versions resident"
// depends on.
func TestBuildIsIdempotentAgainstOneTarget(t *testing.T) {
	dir := skillDir(t, map[string]string{"SKILL.md": buildSkill})
	target := memory.New()

	first, err := Build(context.Background(), target, dir)
	require.NoError(t, err)
	second, err := Build(context.Background(), target, dir)
	require.NoError(t, err, "re-packing an unchanged skill must succeed")

	assert.Equal(t, first.Manifest.Digest, second.Manifest.Digest)
}

func TestBuildRejectsASkillWithoutFrontmatter(t *testing.T) {
	dir := skillDir(t, map[string]string{"SKILL.md": "# No frontmatter\n"})
	_, err := Build(context.Background(), memory.New(), dir)
	assert.Error(t, err)
}
