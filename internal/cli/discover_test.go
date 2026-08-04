package cli

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/gaarutyunov/epos/internal/registry"
)

func TestOpenRequiresARegistry(t *testing.T) {
	_, err := (&discoveryFlags{}).open()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--registry")
}

// SPEC.md 7.1: the command itself has to turn a missing _catalog into the
// capability message. discover reports registry.ErrNoCatalog; if list and search relayed
// that verbatim the user would get a sentence with no registry in it and no
// mention of the direct reference that still works.
func TestCommandsReportAMissingCatalogAsAMissingCapability(t *testing.T) {
	for _, run := range []struct {
		name string
		call func(context.Context, io.Writer, registry.Client) error
	}{
		{"list", func(ctx context.Context, out io.Writer, c registry.Client) error {
			return runList(ctx, out, c, "registry.example.com", "", false)
		}},
		{"list --versions", func(ctx context.Context, out io.Writer, c registry.Client) error {
			return runList(ctx, out, c, "registry.example.com", "", true)
		}},
		{"search", func(ctx context.Context, out io.Writer, c registry.Client) error {
			return runSearch(ctx, out, c, "registry.example.com", "", "pdf")
		}},
	} {
		t.Run(run.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := NewMockClient(ctrl)
			client.EXPECT().Catalog(gomock.Any()).Return(nil, registry.ErrNoCatalog)

			var out bytes.Buffer
			err := run.call(t.Context(), &out, client)

			require.Error(t, err)
			assert.Contains(t, err.Error(), "registry.example.com does not support catalog enumeration")
			assert.Contains(t, err.Error(), "epos pull registry.example.com/<repository>:<version>")
			assert.Empty(t, out.String(), "nothing is listed when the capability is missing")
		})
	}
}

// SPEC.md 7.2, at the command rather than the pipeline: without --versions the
// command issues one catalog call and nothing else.
func TestRunListWithoutVersionsPrintsRepositoriesOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().
		Catalog(gomock.Any()).
		Return([]string{"demo/agent-skills/reviewer", "demo/agent-skills/pdf", "other/toolbox"}, nil).
		Times(1)
	client.EXPECT().Tags(gomock.Any(), gomock.Any()).Times(0)
	client.EXPECT().Annotations(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	var out bytes.Buffer
	require.NoError(t, runList(t.Context(), &out, client, "registry.example.com",
		"demo/agent-skills", false))

	assert.Equal(t, "demo/agent-skills/pdf\ndemo/agent-skills/reviewer\n", out.String())
}

// SPEC.md 7.3: a client-side filter over the enumeration. The registry is asked
// to enumerate, never to search — nothing carries the query to it.
func TestRunSearchFiltersOnTheClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().
		Catalog(gomock.Any()).
		Return([]string{"demo/agent-skills/reviewer", "demo/agent-skills/pdf"}, nil)
	client.EXPECT().
		Tags(gomock.Any(), gomock.Any()).
		Return([]string{"1.0.0"}, nil).
		Times(2)
	client.EXPECT().
		Annotations(gomock.Any(), "demo/agent-skills/pdf", "1.0.0").
		Return(map[string]string{
			ocispec.AnnotationTitle:       "pdf",
			ocispec.AnnotationDescription: "extracts text from PDF files",
		}, nil)
	client.EXPECT().
		Annotations(gomock.Any(), "demo/agent-skills/reviewer", "1.0.0").
		Return(map[string]string{
			ocispec.AnnotationTitle:       "reviewer",
			ocispec.AnnotationDescription: "reviews code changes",
		}, nil)

	var out bytes.Buffer
	require.NoError(t, runSearch(t.Context(), &out, client, "registry.example.com",
		"demo/agent-skills", "extracts text"))

	assert.Equal(t, "demo/agent-skills/pdf:1.0.0\tpdf\textracts text from PDF files\n", out.String())
}

// A search that matches nothing prints nothing and succeeds: an empty result is
// an answer, not a failure. Only a missing _catalog exits non-zero (7.1).
func TestRunSearchThatMatchesNothingSucceedsSilently(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().Catalog(gomock.Any()).Return([]string{"demo/agent-skills/pdf"}, nil)
	client.EXPECT().Tags(gomock.Any(), gomock.Any()).Return([]string{"1.0.0"}, nil)
	client.EXPECT().
		Annotations(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(map[string]string{ocispec.AnnotationTitle: "pdf"}, nil)

	var out bytes.Buffer
	require.NoError(t, runSearch(t.Context(), &out, client, "registry.example.com", "", "spreadsheet"))
	assert.Empty(t, out.String())
}

func TestPrintSkills(t *testing.T) {
	var out bytes.Buffer
	printSkills(&out, []registry.Skill{
		{Repository: "demo/agent-skills/pdf"},
		{Repository: "demo/agent-skills/reviewer", Version: "1.0.0",
			Name: "reviewer", Description: "reviews code changes"},
	})

	assert.Equal(t, "demo/agent-skills/pdf\n"+
		"demo/agent-skills/reviewer:1.0.0\treviewer\treviews code changes\n", out.String())
}

// statusError builds the error shape oras-go surfaces a registry's refusal as.
// It stays here because credentials_test.go asserts on explainAuth's reading of
// a 401; the _catalog side of the same distinction moved to internal/registry
// with the pipeline that makes it.
func statusError(status int) error {
	return &errcode.ErrorResponse{Method: http.MethodGet, StatusCode: status}
}
