package catalog

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/gaarutyunov/epos/internal/registry"
)

//go:generate go tool mockgen -destination=mocks_registry_test.go -package=catalog github.com/gaarutyunov/epos/internal/registry Client

// fixture is the literal the renderer is tested against.
//
// A literal, not a registry: D2's model carries no registry types precisely so
// that every page can be asserted without a container, and a renderer test that
// needed one would be an integration test wearing a unit test's build tag.
func fixture() Catalog {
	return Catalog{
		Registry: "registry.example.com",
		Skills: []Skill{
			{
				Repository:  "demo/agent-skills/pdf",
				Name:        "pdf",
				Description: "extracts text from PDF files",
				Version:     "1.10.0",
				Versions:    []string{"1.10.0", "1.2.0"},
				Digest:      "sha256:aaaa",
				License:     "MIT",
				Provenance: &Provenance{
					BaseName:        "ghcr.io/o/agent-skills/base",
					BaseDigest:      "sha256:bbbb",
					SkillfileDigest: "sha256:cccc",
					Stages:          []Stage{{Name: "containers", Files: []string{"references/go.md"}}},
				},
			},
			{
				Repository:  "demo/agent-skills/reviewer",
				Name:        "reviewer",
				Description: "reviews code changes",
				Version:     "0.4.0",
				Versions:    []string{"0.4.0"},
				Digest:      "sha256:dddd",
			},
		},
	}
}

func counts() *Counts {
	return &Counts{
		CapturedAt: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC),
		Rows: map[string]Pulls{
			"demo/agent-skills/pdf": {Verified: 1284, Unverified: 3910},
			// reviewer deliberately has no row: unknown is not zero.
		},
	}
}

func render(t *testing.T, catalog Catalog, basePath string, route Route, c *Counts) string {
	t.Helper()
	renderer, err := NewRenderer(catalog, basePath)
	require.NoError(t, err)

	var out bytes.Buffer
	require.NoError(t, renderer.Render(&out, route, c, nil))
	return out.String()
}

// Every page renders, at the default base path and at a nested one, with and
// without counts. The four shapes are the whole route table (D2).
func TestEveryPageRendersInBothShapes(t *testing.T) {
	catalog := fixture()

	for _, base := range []string{"/", "/epos/catalog/"} {
		for _, c := range []*Counts{nil, counts()} {
			for _, route := range catalog.Routes() {
				page := render(t, catalog, base, route, c)
				assert.Contains(t, page, "<!doctype html>")
				assert.Contains(t, page, `<link rel="stylesheet" href="`+base+`assets/app.css">`,
					"every internal URL carries the base path")
				assert.Contains(t, page, base+"assets/ga-ui-kit.min.js")
			}
		}
	}
}

// D4h, and it is the requirement an implementation satisfies by rendering
// zeroes: with no statistics source the home page is a deterministic index. No
// rank column, no pull column, and no wording implying popularity.
func TestHomeWithoutAStatisticsSourceIsAnIndexRatherThanARanking(t *testing.T) {
	page := render(t, fixture(), "/", Route{Kind: PageHome}, nil)

	// The column model reaches the browser as an escaped attribute, so the
	// assertion is on what actually ships rather than on the Go literal.
	assert.NotContains(t, page, `label&#34;:&#34;Pulls`, "the pull column is absent, not zeroed")
	assert.NotContains(t, page, `label&#34;:&#34;#`, "there is no rank column")
	assert.NotContains(t, page, "Most pulled")
	assert.Contains(t, page, "ranks nothing")
	assert.NotContains(t, page, ">0<", "no count is rendered at all")
}

// The other shape of the same page, same route, same template.
func TestHomeWithAStatisticsSourceIsALeaderboard(t *testing.T) {
	page := render(t, fixture(), "/", Route{Kind: PageHome}, counts())

	assert.Contains(t, page, `label&#34;:&#34;Pulls`)
	assert.Contains(t, page, `label&#34;:&#34;#`)
	assert.Contains(t, page, "1284")
	assert.Contains(t, page, "Most pulled skills")
	assert.Contains(t, page, "verified")

	pdf := strings.Index(page, "demo/agent-skills/pdf")
	reviewer := strings.Index(page, "demo/agent-skills/reviewer")
	assert.Less(t, pdf, reviewer, "the leaderboard is ordered by count, descending")
}

// 4.8, asserted on the rendered output because it is a rendering rule as much
// as a data one. A repository with no row has an unknown number of pulls, and
// rendering 0 would state something the catalog does not know.
func TestASkillWithNoRowRendersAsUnknownRatherThanZero(t *testing.T) {
	page := render(t, fixture(), "/", Route{Kind: PageHome}, counts())

	reviewer := page[strings.Index(page, "demo/agent-skills/reviewer"):]
	row := reviewer[:strings.Index(reviewer, "</a>")]
	assert.Contains(t, row, "—", "an unknown count renders as an em dash")
	assert.NotContains(t, row, ">0<")
}

// D11: the provenance section exists only when the artifact declares stages. An
// empty provenance table is worse than none.
func TestProvenanceIsOmittedWhenTheArtifactCarriesNoStages(t *testing.T) {
	catalog := fixture()
	route := Route{Kind: PageDetail, Repository: "demo/agent-skills/reviewer"}

	page := render(t, catalog, "/", route, nil)
	assert.NotContains(t, page, "Provenance")

	built := render(t, catalog, "/",
		Route{Kind: PageDetail, Repository: "demo/agent-skills/pdf"}, nil)
	assert.Contains(t, built, "Provenance")
	assert.Contains(t, built, "containers")
	assert.Contains(t, built, "sha256:bbbb")
}

// Breadcrumbs on every inner page, with aria-current on the last crumb — the
// owner asked for them explicitly on the sibling issue.
func TestEveryInnerPageCarriesBreadcrumbs(t *testing.T) {
	catalog := fixture()
	for _, route := range catalog.Routes() {
		page := render(t, catalog, "/", route, nil)
		if route.Kind == PageHome {
			assert.NotContains(t, page, "ga-breadcrumbs", "the entry page is not an inner page")
			continue
		}
		assert.Contains(t, page, "<ga-breadcrumbs")
		assert.Contains(t, page, `aria-label="Breadcrumb"`)
		assert.Contains(t, page, `aria-current="page"`)
	}
}

// D2's invariant: one renderer, two drivers, identical bytes for the same base
// path, model and counts. A page only one of them can produce is a bug.
func TestServeAndExportProduceIdenticalBytes(t *testing.T) {
	catalog := fixture()
	renderer, err := NewRenderer(catalog, "/")
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().FetchContent(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(registry.Content{}, assert.AnError).AnyTimes()

	stats := NewMockStats(ctrl)
	stats.EXPECT().Pulls(gomock.Any()).Return(*counts(), nil).AnyTimes()

	server, err := NewServer(renderer, client, stats)
	require.NoError(t, err)
	handler := server.Handler(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Error("a catalog route reached the relay")
	}))

	for _, route := range catalog.Routes() {
		var exported bytes.Buffer
		var document *Skill
		if route.Kind == PageDetail {
			skill, _ := catalog.Lookup(route.Repository)
			loaded, _ := LoadDocument(t.Context(), client, skill)
			document = &loaded
		}
		require.NoError(t, renderer.Render(&exported, route, counts(), document))

		req := httptest.NewRequest(http.MethodGet, "/"+route.Path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, route.Path)
		assert.Equal(t, exported.String(), rec.Body.String(),
			"serve and export must produce identical bytes for %s", route.Path)
	}
}

// D2b: a different base path changes only the prefix.
//
// Compared between two *nested* base paths rather than against the root,
// because one link legitimately is not a prefix of anything: the header's
// "Docs" points out of the catalog at the site it is deployed under, and at the
// root there is no such site (that is `epos-registry --catalog` serving a
// registry) so it points at the published documentation instead. Every internal
// URL is still nothing but the prefix, which is what the invariant is about.
func TestADifferentBasePathChangesOnlyThePrefix(t *testing.T) {
	catalog := fixture()
	route := Route{Kind: PageList, Path: "catalog/"}

	one := render(t, catalog, "/epos/catalog/", route, nil)
	two := render(t, catalog, "/pr-preview/pr-53/catalog/", route, nil)

	// Both the base path and the site it hangs under, in that order — the
	// longer string first, or the substitution eats its own prefix.
	normalised := strings.ReplaceAll(two, "/pr-preview/pr-53/catalog/", "/epos/catalog/")
	normalised = strings.ReplaceAll(normalised, "/pr-preview/pr-53/", "/epos/")

	assert.Equal(t, one, normalised)
}

// And at the root every internal URL is still rooted, with no prefix left over.
func TestAtTheRootEveryInternalURLIsRooted(t *testing.T) {
	page := render(t, fixture(), "/", Route{Kind: PageList, Path: "catalog/"}, nil)

	assert.Contains(t, page, `href="/assets/app.css"`)
	assert.Contains(t, page, `href="/skills/demo/agent-skills/pdf/"`)
	assert.NotContains(t, page, "//assets/", "the base path is not doubled")
	assert.NotContains(t, page, "/epos/", "no prefix from another deployment survives")
}

// 12.6: the catalog links the documentation, and a preview links its own copy
// of it rather than sending the reviewer to production.
func TestTheDocsLinkStaysInsideThePreview(t *testing.T) {
	preview := render(t, fixture(), "/pr-preview/pr-53/catalog/", Route{Kind: PageHome}, nil)
	assert.Contains(t, preview, `href="/pr-preview/pr-53/"`)
	assert.NotContains(t, preview, "https://epos.garutyunov.com/",
		"an absolute link would take a reviewer out of the preview they were sent")

	live := render(t, fixture(), "/catalog/", Route{Kind: PageHome}, nil)
	assert.Contains(t, live, `href="/"`)

	// Served by a registry, where there is no documentation site alongside.
	served := render(t, fixture(), "/", Route{Kind: PageHome}, nil)
	assert.Contains(t, served, "https://epos.garutyunov.com/")
}

// D2c: an operator who mounts the catalog on the distribution API is refused
// while the process is still starting, rather than discovering it in
// production.
func TestABasePathUnderV2IsRefused(t *testing.T) {
	for _, base := range []string{"/v2", "/v2/", "/v2/catalog"} {
		require.ErrorIs(t, CheckBasePath(base), ErrReservedBasePath, base)
	}
	for _, base := range []string{"/", "/catalog", "/epos/catalog"} {
		require.NoError(t, CheckBasePath(base), base)
	}
}

// The property SPEC.md 4.4's amendment is bought with: with the catalog mounted
// at /, every distribution API path still routes to the relay and nothing the
// catalog holds is consulted to answer one.
func TestTheDistributionAPIIsMatchedBeforeAnyCatalogRoute(t *testing.T) {
	renderer, err := NewRenderer(fixture(), "/")
	require.NoError(t, err)
	ctrl := gomock.NewController(t)
	server, err := NewServer(renderer, NewMockClient(ctrl), nil)
	require.NoError(t, err)

	relayed := 0
	handler := server.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		relayed++
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{
		"/v2/",
		"/v2/demo/agent-skills/pdf/manifests/1.10.0",
		"/v2/demo/agent-skills/pdf/blobs/sha256:aaaa",
		"/v2/demo/agent-skills/pdf/tags/list",
		"/v2/_catalog",
		"/v2/demo/agent-skills/pdf/referrers/sha256:aaaa",
	} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, http.StatusOK, rec.Code, path)
	}
	assert.Equal(t, 6, relayed, "every distribution API path reached the relay")
}

// D3b: the served catalog answers only for repositories in the index built at
// startup. Anything else is a 404 and no registry request is made — without
// this a URL path is an instruction to fetch an arbitrary repository.
func TestAPathOutsideTheIndexIs404WithNoRegistryRequest(t *testing.T) {
	renderer, err := NewRenderer(fixture(), "/")
	require.NoError(t, err)

	ctrl := gomock.NewController(t)
	client := NewMockClient(ctrl)
	client.EXPECT().FetchContent(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	client.EXPECT().Manifest(gomock.Any(), gomock.Any(), gomock.Any()).Times(0)

	server, err := NewServer(renderer, client, nil)
	require.NoError(t, err)
	handler := server.Handler(http.NotFoundHandler())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/skills/somebody/else", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// D2a: a failed index leaves the registry serving and the catalog saying so.
func TestAFailedIndexStillRendersAPageThatSaysSo(t *testing.T) {
	broken := Catalog{Registry: "registry.example.com", Err: "the registry refused _catalog"}
	page := render(t, broken, "/", Route{Kind: PageHome}, nil)

	assert.Contains(t, page, "Catalog unavailable")
	assert.Contains(t, page, "the registry refused _catalog")
	assert.Contains(t, page, "The registry itself is")
}

// And it reaches the page, beside the numbers rather than in the repository.
func TestTheCountsNoteIsRenderedWhereTheNumbersAre(t *testing.T) {
	withNote := counts()
	withNote.Note = "Illustrative figures, not measured traffic."

	home := render(t, fixture(), "/", Route{Kind: PageHome}, withNote)
	assert.Contains(t, home, "Illustrative figures, not measured traffic.")

	detail := render(t, fixture(), "/",
		Route{Kind: PageDetail, Repository: "demo/agent-skills/pdf"}, withNote)
	assert.Contains(t, detail, "Illustrative figures, not measured traffic.")

	// A source that measured something leaves it empty; the capture time
	// already says when, and an empty paragraph is noise.
	plain := render(t, fixture(), "/", Route{Kind: PageHome}, counts())
	assert.NotContains(t, plain, `class="provenance"`)
}
