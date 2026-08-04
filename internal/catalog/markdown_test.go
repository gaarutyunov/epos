package catalog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every assertion here is on the *rendered output*, deliberately, rather than
// on the renderer's configuration. A test that asserted "WithUnsafe was not
// passed" would keep passing the day somebody adds a second renderer; a test
// that asserts a <script> in the source is not a <script> in the output fails
// on the day it stops being true.
func TestAHostileDocumentRendersNothingExecutable(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		rejected []string
	}{
		{
			name:     "a raw script tag",
			source:   "---\nname: x\n---\n\n<script>alert(1)</script>\n",
			rejected: []string{"<script>"},
		},
		{
			name:     "an image with an onerror handler",
			source:   "---\nname: x\n---\n\n<img src=x onerror=\"alert(1)\">\n",
			rejected: []string{"<img src=x", "onerror"},
		},
		{
			name:     "an inline SVG with a handler",
			source:   "---\nname: x\n---\n\n<svg onload=\"alert(1)\"><circle r=\"1\"/></svg>\n",
			rejected: []string{"<svg", "onload"},
		},
		{
			name:     "an iframe",
			source:   "---\nname: x\n---\n\n<iframe src=\"https://evil.example\"></iframe>\n",
			rejected: []string{"<iframe"},
		},
		{
			name: "a javascript: link, which is not raw HTML and survives disabling it",
			source: "---\nname: x\n---\n\n[click me](javascript:alert(1))\n" +
				"[upper case](JAVASCRIPT:alert(1))\n" +
				"[padded](  javascript:alert(1))\n",
			rejected: []string{"javascript:", "JAVASCRIPT:"},
		},
		{
			name:     "a data:text/html image",
			source:   "---\nname: x\n---\n\n![boom](data:text/html;base64,PHNjcmlwdD4=)\n",
			rejected: []string{"data:text/html"},
		},
		{
			name:     "a vbscript: link",
			source:   "---\nname: x\n---\n\n[old](vbscript:msgbox)\n",
			rejected: []string{"vbscript:"},
		},
		{
			name:     "a javascript: autolink",
			source:   "---\nname: x\n---\n\n<javascript:alert(1)>\n",
			rejected: []string{"href=\"javascript:"},
		},
		{
			name:     "an html entity that would reconstitute a tag",
			source:   "---\nname: x\n---\n\n&lt;script&gt;alert(1)&lt;/script&gt;\n",
			rejected: []string{"<script>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := renderMarkdown([]byte(tt.source))
			require.NoError(t, err, "a hostile document renders; it does not fail the page")

			html := string(out)
			for _, rejected := range tt.rejected {
				assert.NotContains(t, html, rejected)
			}
			assert.NotContains(t, html, "onerror=")
			assert.NotContains(t, html, "onload=")
		})
	}
}

// A document that is nothing but frontmatter has no body. It must render as
// empty rather than as the YAML.
func TestADocumentThatIsOnlyFrontmatterRendersNothing(t *testing.T) {
	out, err := renderMarkdown([]byte("---\nname: x\ndescription: y\n---\n"))
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)))
	assert.NotContains(t, string(out), "description")
}

// The other half of the same cause: a sanitiser that eats the content is a
// different bug. Everything a SKILL.md actually uses has to survive.
func TestAnExpressiveDocumentSurvives(t *testing.T) {
	source := `---
name: pdf
---

# Heading one

## Heading two

Prose with **bold**, *italic* and ` + "`code`" + `.

- a bullet
  - a nested bullet
- another

1. first
2. second

| Input | Output |
| --- | --- |
| a PDF | Markdown |

` + "```bash\nepos pull demo/pdf:1.0.0\n```" + `

> A quotation.

[a link](https://example.com/docs), [a relative one](references/style.md),
<https://example.com/auto> and <mailto:someone@example.com>.
`

	out, err := renderMarkdown([]byte(source))
	require.NoError(t, err)
	html := string(out)

	for _, want := range []string{
		"<h1>Heading one</h1>",
		"<h2>Heading two</h2>",
		"<strong>bold</strong>",
		"<em>italic</em>",
		"<code>code</code>",
		"<ul>", "<ol>", "<li>",
		"<table>", "<th>", "<td>",
		"<pre><code",
		"<blockquote>",
		`href="https://example.com/docs"`,
		`href="references/style.md"`,
		`href="https://example.com/auto"`,
		"mailto:someone@example.com",
	} {
		assert.Contains(t, html, want)
	}

	// No heading anchors. That is a decision (D7), not an oversight: an anchor
	// is an id derived from publisher-controlled text.
	assert.NotContains(t, html, `id="heading-one"`)
	assert.NotContains(t, html, "<a class=\"anchor\"")
}

// 6.4a: the document cap is independent of the layer cap, because an enormous
// but perfectly legal SKILL.md would otherwise be parsed on every request.
func TestAnOversizedDocumentIsRefusedWithoutFailingTheSkill(t *testing.T) {
	source := "---\nname: x\n---\n" + strings.Repeat("a", MaxDocument+1)

	_, err := renderMarkdown([]byte(source))
	require.ErrorIs(t, err, ErrDocumentTooLarge)
}

// The scheme rule, at the unit it is decided in. Relative destinations pass:
// they resolve to nothing the catalog serves, which is inert rather than
// dangerous.
func TestSafeDestination(t *testing.T) {
	allowed := []string{
		"https://example.com", "http://example.com", "mailto:a@b.c",
		"references/style.md", "./style.md", "../style.md", "#section",
		"./notes:draft.md", "page.md#a:b", "//example.com/protocol-relative",
	}
	for _, url := range allowed {
		assert.True(t, safeDestination([]byte(url)), url)
	}

	refused := []string{
		"javascript:alert(1)", "JavaScript:alert(1)", " javascript:alert(1)",
		"data:text/html,<script>", "vbscript:x", "file:///etc/passwd", "",
		// A bare `notes:draft.md` parses as the scheme `notes`, and a browser
		// reads it that way too. An author who means the file writes
		// `./notes:draft.md`, which is in the allowed set above.
		"notes:draft.md",
	}
	for _, url := range refused {
		assert.False(t, safeDestination([]byte(url)), url)
	}
}
