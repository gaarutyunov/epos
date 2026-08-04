package catalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"strconv"
)

// The repository's only //go:embed, and it lives here so that the binary an
// operator deploys carries the frontend and the binary a user installs does
// not (D2, D6). cmd/epos/imports_test.go asserts the second half.
//
// Embedded paths are always forward-slashed, on every platform — an embed.FS
// is not a filesystem path — which is the detail a Windows unit run would
// otherwise be the first to notice.
//
//go:embed templates/*.html
var templateFS embed.FS

//go:embed assets/app.css assets/app.js assets/vendor/ui-kit/ga-ui-kit.min.js assets/vendor/ui-kit/ga-ui-kit.css
var assetFS embed.FS

// Assets are the files both drivers serve, keyed by their path under the base.
//
// The two vendored files are flattened next to the catalog's own, so a page
// links four stylesheets and scripts by name rather than by a vendor path that
// says nothing to a reader of the HTML.
func Assets() (map[string][]byte, error) {
	names := map[string]string{
		"assets/app.css":          "assets/app.css",
		"assets/app.js":           "assets/app.js",
		"assets/ga-ui-kit.css":    "assets/vendor/ui-kit/ga-ui-kit.css",
		"assets/ga-ui-kit.min.js": "assets/vendor/ui-kit/ga-ui-kit.min.js",
	}
	out := make(map[string][]byte, len(names))
	for served, embedded := range names {
		body, err := fs.ReadFile(assetFS, embedded)
		if err != nil {
			return nil, fmt.Errorf("read the embedded asset %s: %w", embedded, err)
		}
		out[served] = body
	}
	return out, nil
}

// parseTemplates builds the template set both drivers render through.
func parseTemplates() (*template.Template, error) {
	tmpl := template.New("catalog").Funcs(template.FuncMap{
		"crumbsJSON":   crumbsJSON,
		"boardColumns": boardColumns,
		"indexColumns": indexColumns,
		"rowHref":      func(base string, r Row) string { return r.Href(base) },
		"pulls":        pullsCell,
		"totalPulls":   totalPulls,
	})
	tmpl, err := tmpl.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse the catalog templates: %w", err)
	}
	return tmpl, nil
}

// crumbsJSON renders the breadcrumb trail for ga-breadcrumbs' items attribute.
//
// html/template escapes the result into the attribute, and the browser decodes
// it before the component's JSON.parse ever sees it. The labels are
// registry-supplied, so this is the one place they leave Go as JSON rather than
// as text — and json.Marshal is what keeps a quote in a skill name from ending
// the attribute.
func crumbsJSON(crumbs []Crumb) string {
	type item struct {
		Label string `json:"label"`
		Href  string `json:"href,omitempty"`
	}
	items := make([]item, 0, len(crumbs))
	for _, c := range crumbs {
		if c.Current {
			items = append(items, item{Label: c.Label})
			continue
		}
		items = append(items, item{Label: c.Label, Href: c.Href})
	}
	body, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(body)
}

// boardColumns is the leaderboard's column model: rank, skill, pulls.
//
// skills.sh's home page, minus the sparkline column, which is deliberately not
// taken: ga-chart-frame draws no data by design and Stats returns totals, so a
// column claiming a trend would be drawing one.
func boardColumns() string {
	return `[{"label":"#","width":"56px","align":"right","mono":true},` +
		`{"label":"Skill"},` +
		`{"label":"Pulls","width":"96px","align":"right","mono":true}]`
}

// indexColumns is the same table with nothing ranked: a catalog, not a
// leaderboard.
func indexColumns() string {
	return `[{"label":"Skill"},{"label":"Version","width":"120px","align":"right","mono":true}]`
}

// pullsCell renders a count, or says the number is unknown.
//
// Unknown is not zero (4.8). A skill with no row in the store has not been
// pulled zero times — nothing is known about it — and rendering 0 would state
// something the catalog cannot support. This is a rendering rule as much as a
// data one, which is why it lives in a template function and is asserted in the
// renderer's test.
func pullsCell(r Row) string {
	if r.Pulls == nil {
		return "—"
	}
	return strconv.FormatInt(*r.Pulls, 10)
}

// totalPulls sums the known counts. Rows with none contribute nothing, which is
// the same rule one level up.
func totalPulls(rows []Row) string {
	var total int64
	for _, r := range rows {
		if r.Pulls != nil {
			total += *r.Pulls
		}
	}
	return strconv.FormatInt(total, 10)
}
