package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/gaarutyunov/epos/internal/registry"
)

// D3: the two modes are a setting, not a fallback chain. Degrading silently
// from "everything in the namespace" to "whatever a file lists" would make a
// missing skill indistinguishable from a registry that answered 404.
func TestTheTwoEnumerationModesAreExclusiveAndOneIsRequired(t *testing.T) {
	both := IndexOptions{NamespaceMode: true, Refs: []string{"demo/pdf:1.0.0"}}
	require.ErrorContains(t, both.Validate(), "mutually exclusive")

	neither := IndexOptions{}
	require.ErrorContains(t, neither.Validate(), "--catalog.namespace or --catalog.refs")

	require.NoError(t, IndexOptions{NamespaceMode: true}.Validate(),
		"an empty namespace is legal: a registry holding nothing but skills needs no filter")
	require.NoError(t, IndexOptions{Refs: []string{"demo/pdf:1.0.0"}}.Validate())
}

// 8.2a: the catalog shows the registry the process fronts. A reference naming
// somebody else's registry is an error, not a silent fetch from it.
func TestARefsEntryMayNotNameADifferentRegistry(t *testing.T) {
	got, err := resolveRefs([]string{
		"demo/agent-skills/pdf:1.0.0",
		"registry.example.com/demo/agent-skills/reviewer:0.4.0",
	}, "registry.example.com")
	require.NoError(t, err)
	assert.Equal(t, []reference{
		{repository: "demo/agent-skills/pdf", tag: "1.0.0"},
		{repository: "demo/agent-skills/reviewer", tag: "0.4.0"},
	}, got, "the host is optional and, when present, must be the one being fronted")

	_, err = resolveRefs([]string{"ghcr.io/o/agent-skills/pdf:1.0.0"}, "registry.example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names a registry other than registry.example.com")

	_, err = resolveRefs([]string{"demo/agent-skills/pdf"}, "registry.example.com")
	require.ErrorContains(t, err, "is not a reference")
}

func TestReadRefsFileSkipsCommentsAndBlanks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "refs.txt")
	require.NoError(t, os.WriteFile(path, []byte(
		"# the skills the demo publishes\n\ndemo/pdf:1.0.0\n\n  demo/reviewer:0.4.0  \n"), 0o600))

	refs, err := ReadRefsFile(path)
	require.NoError(t, err)
	assert.Equal(t, []string{"demo/pdf:1.0.0", "demo/reviewer:0.4.0"}, refs)

	empty := filepath.Join(t.TempDir(), "empty.txt")
	require.NoError(t, os.WriteFile(empty, []byte("# nothing here\n"), 0o600))
	_, err = ReadRefsFile(empty)
	require.ErrorContains(t, err, "lists no references")
}

// D2a: a failed index never stops the registry. It is recorded on the catalog
// and the pages say so.
func TestAFailedSweepIsRecordedRatherThanReturned(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().Catalog(gomock.Any()).Return(nil, registry.ErrNoCatalog)

	got := BuildIndex(t.Context(), client, IndexOptions{
		Host: "ghcr.io", NamespaceMode: true, Namespace: "o/agent-skills",
	})

	assert.Empty(t, got.Skills)
	assert.Contains(t, got.Err, "catalog enumeration")
}

// D3d: one unreadable artifact leaves that skill out and the rest listed. A
// catalog that 500s because one publisher pushed something broken is a catalog
// one publisher can take down.
func TestOneBadArtifactDoesNotFailTheIndex(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().Catalog(gomock.Any()).
		Return([]string{"demo/agent-skills/pdf", "demo/agent-skills/broken"}, nil)
	client.EXPECT().Tags(gomock.Any(), "demo/agent-skills/pdf").
		Return([]string{"1.2.0", "1.10.0"}, nil)
	client.EXPECT().Tags(gomock.Any(), "demo/agent-skills/broken").
		Return(nil, errors.New("the registry hung up"))
	client.EXPECT().Manifest(gomock.Any(), "demo/agent-skills/pdf", "1.10.0").
		Return(registry.Manifest{
			Digest: "sha256:aaaa",
			Annotations: map[string]string{
				ocispec.AnnotationTitle:       "pdf",
				ocispec.AnnotationDescription: "extracts text",
			},
		}, nil)

	got := BuildIndex(t.Context(), client, IndexOptions{Host: "registry.example.com", NamespaceMode: true})

	require.Len(t, got.Skills, 1)
	assert.Equal(t, "demo/agent-skills/pdf", got.Skills[0].Repository)
	assert.Empty(t, got.Err, "one bad artifact is not an index failure")
}

// D3a: one entry per repository, showing the newest version — and "newest" has
// to compare 1.10.0 above 1.2.0, which a plain string sort gets backwards.
func TestTheNewestVersionIsTheOneRendered(t *testing.T) {
	tags := []string{"1.2.0", "1.10.0", "0.9.0", "1.10.0-rc1"}
	sortVersions(tags)
	assert.Equal(t, "1.10.0", tags[0])
	assert.Equal(t, []string{"1.10.0", "1.10.0-rc1", "1.2.0", "0.9.0"}, tags)
}

// D3d again, one level down: a layer that cannot be read leaves the skill
// listed and its page saying so, rather than failing anything.
func TestAnUnreadableLayerBecomesAMessageOnThePage(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().FetchContent(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(registry.Content{}, errors.New("the content layer is 12 GiB"))

	got, cacheable := LoadDocument(t.Context(), client, Skill{Repository: "demo/pdf", Version: "1.0.0"})
	assert.Empty(t, got.Document)
	assert.Contains(t, got.DocumentError, "could not be read")
	assert.Contains(t, got.DocumentError, "12 GiB")
	assert.False(t, cacheable,
		"a failed fetch is a property of the moment, not of the artifact: caching it "+
			"against an immutable digest would make one blip permanent")
}

// And an artifact with no SKILL.md at all, which is a different failure with
// the same rule.
func TestAnArtifactWithNoSkillFileSaysSo(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().FetchContent(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(registry.Content{Files: map[string][]byte{"README.md": []byte("hi")}}, nil)

	got, cacheable := LoadDocument(t.Context(), client, Skill{Repository: "demo/pdf", Version: "1.0.0"})
	assert.Contains(t, got.DocumentError, "carries no SKILL.md")
	assert.True(t, cacheable,
		"an artifact with no SKILL.md fails the same way forever, and caching that is "+
			"what stops a hostile layer being fetched on every request")
}

// The inline config blob is why one manifest GET is enough for a list page.
func TestTheFrontmatterIsReadFromTheInlineConfigBlob(t *testing.T) {
	skill := skillFrom("demo/agent-skills/pdf", []string{"1.0.0"}, registry.Manifest{
		Digest:      "sha256:aaaa",
		Annotations: map[string]string{ocispec.AnnotationTitle: "pdf"},
		Config: ocispec.Descriptor{
			Data: []byte(`{"name":"pdf","description":"from the blob","license":"MIT"}`),
		},
	})

	assert.Equal(t, "MIT", skill.License)
	assert.Equal(t, "from the blob", skill.Description,
		"the annotation wins when it is present; the blob fills in when it is not")
}
