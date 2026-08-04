package catalog

import (
	"context"
	"fmt"
	"html/template"
	"io"
	"path"
	"sort"
	"strings"
	"time"
)

// Page kinds. Four shapes, in skills.sh's information architecture (D8).
const (
	// PageHome is the catalog's entry page: a ranked leaderboard when a
	// statistics source is configured, a deterministic index when one is not
	// (D4h). Same template, same route, different sections — not a second home
	// page, and not a leaderboard rendering zeroes.
	PageHome = "home"
	// PageList is the catalog list, with the client-side filter over the
	// already-delivered index.
	PageList = "list"
	// PageDetail is one skill: the rendered SKILL.md in the main column, the
	// metadata in the aside.
	PageDetail = "detail"
	// PageTools is a capability table, not a logo wall (D9).
	PageTools = "tools"
)

// Route is one page of the site.
//
// The table is shared by both drivers: export walks it, serve serves it. The
// two produce identical bytes for the same base path, model and counts — that
// is the invariant D2 exists to keep, and it is asserted by a test rather than
// intended.
type Route struct {
	// Kind is the page shape.
	Kind string
	// Path is the URL path relative to the base, always ending in "/" so a
	// directory index resolves the same way on a static host and on the server.
	Path string
	// Repository is set on a detail route.
	Repository string
}

// File is where export writes the route.
func (r Route) File() string { return path.Join(r.Path, "index.html") }

// Routes returns every page the catalog holds, in a deterministic order.
func (c Catalog) Routes() []Route {
	routes := []Route{
		{Kind: PageHome, Path: ""},
		{Kind: PageList, Path: "catalog/"},
		{Kind: PageTools, Path: "tools/"},
	}
	for _, s := range c.Skills {
		routes = append(routes, Route{
			Kind:       PageDetail,
			Path:       "skills/" + s.Repository + "/",
			Repository: s.Repository,
		})
	}
	return routes
}

// Renderer turns the model into pages.
type Renderer struct {
	catalog  Catalog
	basePath string
	tmpl     *template.Template
}

// NewRenderer builds a renderer for a catalog at a base path.
//
// The base path prefixes every internal URL the templates emit, and it is the
// same setting in both modes (D2c). It is not a nicety: the demo is served from
// a subdirectory of a project Pages site, and a template emitting /skills/foo
// or /assets/app.css resolves at the domain root and 404s on every page.
func NewRenderer(catalog Catalog, basePath string) (*Renderer, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Renderer{catalog: catalog, basePath: normaliseBasePath(basePath), tmpl: tmpl}, nil
}

// BasePath is the normalised prefix, always leading and trailing with "/".
func (r *Renderer) BasePath() string { return r.basePath }

// Catalog is the index the renderer was built over.
func (r *Renderer) Catalog() Catalog { return r.catalog }

// normaliseBasePath turns whatever an operator typed into one canonical form.
//
// "", "/", "epos/catalog", "/epos/catalog" and "/epos/catalog/" are all the
// same site, and a renderer that emitted five different sets of URLs for them
// would be a base-path bug per configuration.
func normaliseBasePath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" {
		return "/"
	}
	return "/" + p + "/"
}

// pageData is everything a template may read. Deliberately a flat struct rather
// than the model itself: a template that could reach the whole Catalog would
// grow a page's worth of logic in it.
type pageData struct {
	Kind        string
	Title       string
	Description string
	BasePath    string
	Registry    string
	IndexError  string

	Breadcrumbs []Crumb
	Rows        []Row
	Skill       *Skill

	// HasCounts is the switch between the two home-page shapes and between a
	// list with a pulls column and one without. It is false for `none`, which
	// is a supported configuration and not a degraded mode: the column is
	// absent, never zeroed (D4h).
	HasCounts  bool
	CapturedAt string

	Registries []ToolRow
	Agents     []AgentRow
}

// Crumb is one breadcrumb. Every inner page carries them.
type Crumb struct {
	Label string
	Href  string
	// Current marks the last crumb, which is rendered as text with
	// aria-current="page" rather than as a link.
	Current bool
}

// Row is one leaderboard or list row.
type Row struct {
	Rank  int
	Skill Skill
	// Pulls is nil when nothing is known about this repository. Unknown is not
	// zero (4.8): a skill with no row in the store has not been pulled zero
	// times, it has an unknown number of pulls, and rendering 0 states
	// something the catalog does not know.
	Pulls *int64
}

// Href is the detail page of the row's skill.
func (r Row) Href(basePath string) string {
	return basePath + "skills/" + r.Skill.Repository + "/"
}

// URL prefixes a site-relative path with the base path.
func (r *Renderer) URL(p string) string { return r.basePath + strings.TrimPrefix(p, "/") }

// Render writes one page.
//
// counts is nil when no statistics source is configured, which is the default
// and a first-class mode. A source that failed is also nil here: a failing
// source degrades to absent counts and the page still serves (4.7).
//
// document is the skill with its SKILL.md already fetched and rendered, for a
// detail page; nil everywhere else. It is a parameter rather than something the
// renderer stores because the served catalog renders concurrently: a driver
// that wrote the document back into the shared index would be a data race the
// -race build finds on the second request.
func (r *Renderer) Render(w io.Writer, route Route, counts *Counts, document *Skill) error {
	data, err := r.data(route, counts, document)
	if err != nil {
		return err
	}
	return r.tmpl.ExecuteTemplate(w, "page", data)
}

func (r *Renderer) data(route Route, counts *Counts, document *Skill) (pageData, error) {
	data := pageData{
		Kind:       route.Kind,
		BasePath:   r.basePath,
		Registry:   r.catalog.Registry,
		IndexError: r.catalog.Err,
		HasCounts:  counts != nil,
	}
	if counts != nil && !counts.CapturedAt.IsZero() {
		data.CapturedAt = counts.CapturedAt.UTC().Format(time.RFC3339)
	}

	switch route.Kind {
	case PageHome:
		data.Title = "Epos catalog"
		data.Description = "Agent skills published to " + r.catalog.Registry
		data.Rows = r.rows(counts, true)
	case PageList:
		data.Title = "Skills — Epos catalog"
		data.Description = "Every skill in the catalog"
		data.Rows = r.rows(counts, false)
		data.Breadcrumbs = r.crumbs(Crumb{Label: "Skills", Current: true})
	case PageTools:
		data.Title = "Tools — Epos catalog"
		data.Description = "Registries and agents epos was verified against"
		data.Registries = registryRows()
		data.Agents = agentRows()
		data.Breadcrumbs = r.crumbs(Crumb{Label: "Tools", Current: true})
	case PageDetail:
		skill, ok := r.catalog.Lookup(route.Repository)
		if !ok {
			return pageData{}, fmt.Errorf("no skill named %s is in the catalog", route.Repository)
		}
		if document != nil && document.Repository == route.Repository {
			skill = *document
		}
		data.Title = skill.Name + " — Epos catalog"
		data.Description = skill.Description
		data.Skill = &skill
		data.Breadcrumbs = r.crumbs(
			Crumb{Label: "Skills", Href: r.URL("catalog/")},
			Crumb{Label: skill.Name, Current: true},
		)
		if counts != nil {
			if pulls, ok := counts.Rows[skill.Repository]; ok {
				verified := pulls.Verified
				data.Rows = []Row{{Skill: skill, Pulls: &verified}}
			} else {
				data.Rows = []Row{{Skill: skill}}
			}
		}
	default:
		return pageData{}, fmt.Errorf("unknown page %q", route.Kind)
	}
	return data, nil
}

// crumbs prefixes the trail with the catalog's own entry page.
func (r *Renderer) crumbs(rest ...Crumb) []Crumb {
	return append([]Crumb{{Label: "Catalog", Href: r.basePath}}, rest...)
}

// rows builds the leaderboard or the list.
//
// ranked orders by the verified count, descending, and numbers the rows. That
// is the home page's shape when a statistics source is configured. Without one
// — and on the list page, which is a catalog rather than a ranking — the order
// is the index's own, which is repository name: deterministic, and claiming
// nothing about popularity.
//
// The leaderboard ranks the *verified* side. Unverified is known-inflated
// (every `epos verify` fetches a signature blob out of the skill's own
// repository and counts as a download of it), and the verified side is the one
// number the system can defend. skills.sh does exactly the same thing for the
// same reason: its counts come from its own CLI, not from registry traffic.
func (r *Renderer) rows(counts *Counts, ranked bool) []Row {
	rows := make([]Row, 0, len(r.catalog.Skills))
	for _, s := range r.catalog.Skills {
		row := Row{Skill: s}
		if counts != nil {
			if pulls, ok := counts.Rows[s.Repository]; ok {
				verified := pulls.Verified
				row.Pulls = &verified
			}
		}
		rows = append(rows, row)
	}

	if ranked && counts != nil {
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i].Pulls, rows[j].Pulls
			switch {
			case a == nil && b == nil:
				return rows[i].Skill.Repository < rows[j].Skill.Repository
			case a == nil:
				// Unknown sorts below every known count, and below zero: a
				// skill nothing is known about has not out-ranked one that was
				// measured and found unpopular.
				return false
			case b == nil:
				return true
			case *a != *b:
				return *a > *b
			default:
				return rows[i].Skill.Repository < rows[j].Skill.Repository
			}
		})
		for i := range rows {
			rows[i].Rank = i + 1
		}
	}
	return rows
}

// StatsOrNil reads a source, degrading to no counts on failure.
//
// A failing source must never render as zero and must never fail a page: every
// page still serves, the counts go absent, and the failure is logged (4.7).
// That is also what keeps a slow store off the relay — the catalog now shares a
// process with /v2/, so a source that hangs must cost the numbers and nothing
// else (D4g).
func StatsOrNil(ctx context.Context, stats Stats, onError func(error)) *Counts {
	if stats == nil {
		return nil
	}
	counts, err := stats.Pulls(ctx)
	if err != nil {
		if onError != nil {
			onError(err)
		}
		return nil
	}
	return &counts
}
