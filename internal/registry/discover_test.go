package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

// SPEC.md 7.2: steps 3 and 4 are lazy, and that is a stated requirement rather
// than an optimisation. The assertion is over the calls actually made — a test
// on the printed rows would still pass if list fetched every manifest and threw
// the result away.
func TestListWithoutVersionsAsksNoRepositoryForAnything(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)

	client.EXPECT().
		Catalog(gomock.Any()).
		Return([]string{"demo/agent-skills/pdf", "demo/agent-skills/reviewer"}, nil).
		Times(1)
	client.EXPECT().Tags(gomock.Any(), gomock.Any()).Times(0)
	client.EXPECT().Annotations(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	listing, err := Discover(t.Context(), client, "demo/agent-skills", false)
	require.NoError(t, err)
	assert.Equal(t, []Skill{
		{Repository: "demo/agent-skills/pdf"},
		{Repository: "demo/agent-skills/reviewer"},
	}, listing, "a listing without --versions carries repository names and nothing else")
}

// SPEC.md 7.2 steps 3 and 4, which --versions opts into.
func TestListWithVersionsResolvesEveryVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)

	client.EXPECT().Catalog(gomock.Any()).Return([]string{"demo/agent-skills/pdf"}, nil)
	client.EXPECT().Tags(gomock.Any(), "demo/agent-skills/pdf").Return([]string{"1.0.0", "1.1.0"}, nil)
	for _, version := range []string{"1.0.0", "1.1.0"} {
		client.EXPECT().
			Annotations(gomock.Any(), "demo/agent-skills/pdf", version).
			Return(map[string]string{
				ocispec.AnnotationTitle:       "pdf",
				ocispec.AnnotationDescription: "extracts text from PDF files",
			}, nil)
	}

	listing, err := Discover(t.Context(), client, "", true)
	require.NoError(t, err)
	assert.Equal(t, []Skill{
		{Repository: "demo/agent-skills/pdf", Version: "1.0.0",
			Name: "pdf", Description: "extracts text from PDF files"},
		{Repository: "demo/agent-skills/pdf", Version: "1.1.0",
			Name: "pdf", Description: "extracts text from PDF files"},
	}, listing)
}

// A listing built by ranging over a map, or by trusting the registry's own
// order, is unstable — the class of bug this repo has already shipped twice.
func TestListingOrderIsIndependentOfTheRegistrysOrder(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)

	client.EXPECT().
		Catalog(gomock.Any()).
		Return([]string{"demo/agent-skills/reviewer", "demo/agent-skills/pdf"}, nil)
	client.EXPECT().
		Tags(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, string) ([]string, error) {
			return []string{"1.10.0", "1.2.0"}, nil
		}).
		Times(2)
	client.EXPECT().
		Annotations(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(map[string]string{}, nil).
		AnyTimes()

	listing, err := Discover(t.Context(), client, "", true)
	require.NoError(t, err)

	var got []string
	for _, s := range listing {
		got = append(got, s.Repository+":"+s.Version)
	}
	assert.Equal(t, []string{
		"demo/agent-skills/pdf:1.10.0",
		"demo/agent-skills/pdf:1.2.0",
		"demo/agent-skills/reviewer:1.10.0",
		"demo/agent-skills/reviewer:1.2.0",
	}, got, "repositories and versions are both sorted, whatever order the registry answered in")
}

// SPEC.md 7.2 step 2.
func TestNamespaceFilter(t *testing.T) {
	catalog := []string{
		"demo/agent-skills",
		"demo/agent-skills/pdf",
		"demo/agent-skills-legacy/pdf",
		"other/toolbox",
	}

	tests := []struct {
		name      string
		namespace string
		want      []string
	}{
		{"whole registry", "", catalog},
		{"under the namespace", "demo/agent-skills",
			[]string{"demo/agent-skills", "demo/agent-skills/pdf"}},
		{"a trailing slash is not a different namespace", "demo/agent-skills/",
			[]string{"demo/agent-skills", "demo/agent-skills/pdf"}},
		{"a sibling with a longer name is not under it", "demo/agent-skills-legacy",
			[]string{"demo/agent-skills-legacy/pdf"}},
		{"nothing matches", "nope", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, WithinNamespace(catalog, tt.namespace))
		})
	}
}

// SPEC.md 7.3: the query matches repository name, skill name and description.
func TestSearchMatchesRepositoryNameSkillNameAndDescription(t *testing.T) {
	s := Skill{
		Repository:  "demo/agent-skills/pdf",
		Version:     "1.0.0",
		Name:        "pdf",
		Description: "extracts text from PDF files",
	}

	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{"repository name", "agent-skills", true},
		{"skill name", "pdf", true},
		{"description", "extracts text", true},
		{"case insensitive", "EXTRACTS TEXT", true},
		{"no match", "spreadsheet", false},
		{"the version is not searched", "1.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, s.Matches(tt.query))
		})
	}
}

// SPEC.md 7.1: the capability is reported as unavailable, not as an HTTP error.
func TestCatalogUnavailableIsReportedAsAMissingCapability(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().Catalog(gomock.Any()).Return(nil, ErrNoCatalog).Times(2)

	for _, run := range []struct {
		name string
		call func() error
	}{
		{"list", func() error {
			listing, err := Discover(t.Context(), client, "", false)
			assert.Nil(t, listing)
			return err
		}},
		{"search", func() error {
			listing, err := Discover(t.Context(), client, "", true)
			assert.Nil(t, listing)
			return err
		}},
	} {
		t.Run(run.name, func(t *testing.T) {
			err := run.call()
			require.ErrorIs(t, err, ErrNoCatalog)
		})
	}

	message := CatalogUnavailable("registry.example.com").Error()
	assert.Contains(t, message, "does not support catalog enumeration")
	assert.Contains(t, message, "/v2/_catalog")
	assert.Contains(t, message, "epos pull registry.example.com/<repository>:<version>",
		"the message has to say that direct references still work")
}

// The distinction the message depends on: a registry that will not serve
// _catalog, versus one that could not answer this time.
func TestUnsupportedRecognisesADeclinedCatalog(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"not found", statusError(http.StatusNotFound), true},
		{"forbidden", statusError(http.StatusForbidden), true},
		{"method not allowed", statusError(http.StatusMethodNotAllowed), true},
		{"not implemented", statusError(http.StatusNotImplemented), true},
		{"unauthorized is a credentials problem", statusError(http.StatusUnauthorized), false},
		{"server error", statusError(http.StatusInternalServerError), false},
		{"wrapped", fmt.Errorf("list: %w", statusError(http.StatusNotFound)), true},
		{"not an HTTP error at all", errors.New("dial tcp: connection refused"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Unsupported(tt.err))
		})
	}
}

func statusError(status int) error {
	return &errcode.ErrorResponse{Method: http.MethodGet, StatusCode: status}
}

// A registry host carries a port far more often than not — every local zot and
// every test registry does — and the reference parser must not read it as a tag.
func TestRegistryHostMayCarryAPort(t *testing.T) {
	client, err := NewOCIRegistry("127.0.0.1:45100", Options{PlainHTTP: true})
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:45100", client.reg.Reference.Registry)
	assert.True(t, client.reg.PlainHTTP)
}
