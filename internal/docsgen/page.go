package main

import "strings"

// page accumulates one file.
//
// Byte stability is the property everything here exists to preserve: the drift
// check is worthless if the same inputs can produce two different files. No
// method below ranges a map, and the only newline written anywhere is "\n" — a
// page whose line endings came from runtime.GOOS would fail the check on
// exactly one leg of the CI matrix.
type page struct {
	b strings.Builder
	// pad is prefixed to every non-empty line. The section emitters were
	// written when the page had no wrapper; the content column added two
	// levels, and indenting from here keeps that a one-line change rather than
	// a re-indent of every string in the package.
	pad string
}

// w writes one line.
func (p *page) w(lines ...string) {
	for _, line := range lines {
		if line != "" {
			p.b.WriteString(p.pad)
		}
		p.b.WriteString(line)
		p.b.WriteString("\n")
	}
}

// frontmatter opens the Astro file.
//
// banner is the do-not-edit comment, which names the source the page was
// generated from so a reader who wants to change the text knows where to go.
func (p *page) frontmatter(banner string) {
	p.w(
		"---",
		strings.TrimRight(banner, "\n"),
		`import Base from "../layouts/Base.astro";`,
		"",
		"// BASE_URL carries a trailing slash only when `base` was written with one,",
		"// so joining by hand is what keeps a link off /pr-preview/pr-1skillfile.",
		"const href = (path: string) =>",
		"  `${import.meta.env.BASE_URL.replace(/\\/$/, \"\")}/${path}`;",
		"---",
		"",
	)
}

// docOpen writes the layout call and opens the content column.
//
// crumb is the second breadcrumb. The layout renders it, which is why the
// generated pages gain one without a line of Astro being hand-written: the
// generator learned the capability, and the drift check still owns the file.
func (p *page) docOpen(title, description, crumb string) {
	p.w(
		`<Base`,
		`  title="`+escape(title)+`"`,
		`  description="`+escape(description)+`"`,
		`  crumb="`+escape(crumb)+`"`,
		`>`,
		`  <div class="doc">`,
		`    <div class="doc-main">`,
	)
	p.pad = "    "
}

// heading writes the page's visible h1 and its lede.
func (p *page) heading(title string, lede []string) {
	p.w(`  <h1>` + escape(title) + `</h1>`)
	p.w(`  <p class="lede">`)
	p.w(lede...)
	p.w(`  </p>`, "")
}

// asideLink is one row of the sidebar.
//
// attr is the whole href value as it is written in the markup — `"#syntax"` for
// an in-page anchor, `{href("cli")}` for a page the base path has to reach —
// because those two forms differ in Astro and the generator emits both.
type asideLink struct {
	attr  string
	label string
	// code renders the label monospaced, for a command or an instruction.
	code bool
	// arrow is the trailing glyph: → within the site, ↗ off it.
	arrow string
}

// asideStart closes the content column and opens the sidebar.
func (p *page) asideStart() {
	p.pad = ""
	p.w(
		`    </div>`,
		"",
		`    <aside class="doc-aside">`,
	)
}

// asideSection writes one labelled group of links.
func (p *page) asideSection(title string, links []asideLink) {
	p.w(
		`      <div>`,
		`        <h2 class="section-label">`+escape(title)+`</h2>`,
		`        <ul class="rows">`,
	)
	for _, link := range links {
		label := escape(link.label)
		if link.code {
			label = `<code>` + label + `</code>`
		}
		arrow := link.arrow
		if arrow == "" {
			arrow = "&rarr;"
		}
		p.w(
			`          <li>`,
			`            <a href=`+link.attr+`>`,
			`              <span>`+label+`</span>`,
			`              <span class="arrow">`+arrow+`</span>`,
			`            </a>`,
			`          </li>`,
		)
	}
	p.w(`        </ul>`, `      </div>`)
}

// docClose closes the sidebar, the grid and the layout.
//
// No page footer of its own: the layout carries the repository, spec and
// licence links every page needs, and a second footer here would be the same
// three links twice.
func (p *page) docClose() {
	p.w(
		`    </aside>`,
		`  </div>`,
		`</Base>`,
		"",
	)
}

// escape makes text safe to drop into the Astro template.
//
// The braces are the reason this exists rather than html.EscapeString: Astro
// reads a bare `{` in markup as the start of a JavaScript expression, and the
// generated pages are full of them — `{{ .Values.model }}` in a template,
// `{ print }` in an AWK script, `{name}` in a syntax line. As numeric
// references they reach the HTML parser as ordinary text, so they render and
// copy as the braces they are.
func escape(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		case '{':
			b.WriteString("&#123;")
		case '}':
			b.WriteString("&#125;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// inline escapes prose and renders the two markers the Skillfile instruction
// table's text uses: `code` spans and **strong** runs.
//
// Not used on anything cobra owns: help text is printed to a terminal verbatim,
// so a backtick there is a backtick, and rendering it as markup would make the
// page say something the CLI does not.
func inline(s string) string {
	return emphasise(emphasise(escape(s), "`", "code"), "**", "strong")
}

// emphasise wraps every delimited run in tag.
//
// An unpaired delimiter leaves the text exactly as it was rather than emitting
// half an element: a stray backtick in a note is a typo, and unbalanced markup
// would be a broken page.
func emphasise(s, delim, tag string) string {
	parts := strings.Split(s, delim)
	if len(parts)%2 == 0 {
		return s
	}

	var b strings.Builder
	for i, part := range parts {
		if i%2 == 0 {
			b.WriteString(part)
			continue
		}
		b.WriteString("<" + tag + ">")
		b.WriteString(part)
		b.WriteString("</" + tag + ">")
	}
	return b.String()
}
