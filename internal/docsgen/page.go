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
}

// w writes one line.
func (p *page) w(lines ...string) {
	for _, line := range lines {
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
		"// so joining by hand is what keeps a link off /eposskillfile.",
		"const href = (path: string) =>",
		"  `${import.meta.env.BASE_URL.replace(/\\/$/, \"\")}/${path}`;",
		"---",
		"",
	)
}

func (p *page) footer() {
	p.w(
		`  <footer>`,
		`    <p>`,
		`      <ga-button variant="primary" href="https://github.com/gaarutyunov/epos">`,
		`        Source on GitHub`,
		`      </ga-button>`,
		`    </p>`,
		`  </footer>`,
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
