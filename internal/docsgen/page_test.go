package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// repoRoot is the repository, from this package's directory.
func repoRoot() string {
	return filepath.Join("..", "..")
}

// TestCommittedPagesAreUpToDate is the drift check of SPEC.md 14.1, in the form
// that runs on every platform of the test matrix.
//
// CI runs the same check as an explicit step, so a reviewer reading the
// workflow can see it; this is what makes it fail for anyone who changes a
// command or the instruction table and runs `go test` before pushing.
func TestCommittedPagesAreUpToDate(t *testing.T) {
	require.NoError(t, run(repoRoot(), true),
		"a committed reference page no longer matches what generates it")
}

// TestCheckFailsOnAHandEditedPage is the other half, and the one worth having:
// a drift check that cannot fail is the failure mode.
//
// Every page is checked, so a target added to the set without a way to notice
// it went stale fails here rather than shipping.
func TestCheckFailsOnAHandEditedPage(t *testing.T) {
	for _, target := range targets() {
		t.Run(target.path, func(t *testing.T) {
			root := t.TempDir()
			for _, other := range targets() {
				write(t, root, other.path, string(other.render()))
			}

			// One word, changed the way a reader who spotted a wording nit
			// would change it. Nothing about the edit needs to be malformed for
			// the check to catch it — that is the property being pinned.
			edited := strings.Replace(string(target.render()), "reference", "handbook", 1)
			write(t, root, target.path, edited)

			err := run(root, true)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "out of date")
			assert.Contains(t, err.Error(), target.path)
		})
	}
}

// TestCheckFailsOnAMissingPage pins the other way a page goes stale: deleted,
// or never written by a generator somebody added a target for.
func TestCheckFailsOnAMissingPage(t *testing.T) {
	assert.Error(t, run(t.TempDir(), true))
}

// TestWriteThenCheckAgrees pins that what the writer produces is what the
// checker accepts. The two reading the render differently is a check that
// passes only until someone regenerates.
func TestWriteThenCheckAgrees(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, run(root, false))
	assert.NoError(t, run(root, true))
}

// TestRenderIsByteStable is what makes the drift check meaningful. A generator
// that ranged a Go map would produce a different file run to run, and the check
// would fail for everyone regardless of what they changed.
func TestRenderIsByteStable(t *testing.T) {
	for _, target := range targets() {
		t.Run(target.path, func(t *testing.T) {
			first := string(target.render())
			for range 5 {
				assert.Equal(t, first, string(target.render()))
			}
		})
	}
}

// TestRenderUsesOneLineEnding pins the output's newlines to LF on every host.
// Windows is in the CI matrix, and a page whose line endings came from the
// machine that generated it would fail the drift check on one leg of it.
func TestRenderUsesOneLineEnding(t *testing.T) {
	for _, target := range targets() {
		t.Run(target.path, func(t *testing.T) {
			assert.NotContains(t, string(target.render()), "\r")
		})
	}
}

// TestMarkupCarriesNoBareBraces is why escape exists. Astro reads a bare `{` in
// markup as the start of a JavaScript expression, and these pages document a
// template language and a shell surface made of them — an unescaped one would
// either break the build or silently swallow the text it opened.
//
// The frontmatter fence and the <style> element are excluded: the first is
// TypeScript and the second is raw text to Astro, so braces there are meant.
func TestMarkupCarriesNoBareBraces(t *testing.T) {
	for _, target := range targets() {
		t.Run(target.path, func(t *testing.T) {
			out := string(target.render())

			_, afterFrontmatter, ok := strings.Cut(strings.TrimPrefix(out, "---\n"), "\n---\n")
			require.True(t, ok, "the page has no frontmatter fence")

			markup, _, ok := strings.Cut(afterFrontmatter, "<style>")
			require.True(t, ok, "the page has no style element")

			// The expressions the pages write on purpose: base-relative links.
			for _, expr := range []string{
				`{href("")}`, `{href("cli")}`, `{href("quickstart")}`, `{href("skillfile")}`,
			} {
				markup = strings.ReplaceAll(markup, expr, "")
			}

			assert.NotContains(t, markup, "{")
			assert.NotContains(t, markup, "}")
		})
	}
}

// TestEmphasiseLeavesUnbalancedMarkersAlone pins the choice not to emit half an
// element when a note has a stray delimiter.
func TestEmphasiseLeavesUnbalancedMarkersAlone(t *testing.T) {
	assert.Equal(t, "a <code>b</code> c", emphasise("a `b` c", "`", "code"))
	assert.Equal(t, "a `b c", emphasise("a `b c", "`", "code"))
}

// TestEscapeCoversTheAstroHazards is a unit check on the escaping itself, so a
// future simplification to html.EscapeString — which leaves braces alone — is
// caught here rather than by a broken page.
func TestEscapeCoversTheAstroHazards(t *testing.T) {
	assert.Equal(t, "&#123;&#123; .Values.model &#125;&#125;", escape("{{ .Values.model }}"))
	assert.Equal(t, "&lt;path&gt; &amp; &quot;x&quot;", escape(`<path> & "x"`))
}

// write puts one page under root, creating the directories it needs.
func write(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
}

// TestGeneratedPagesCarryTheSharedChrome pins the capabilities the generator
// gained rather than the Astro it emits.
//
// Both reference pages have to reach the shared layout through this program:
// the moment one of them is easier to fix by hand, the drift gate stops being
// worth having. These are the four things a hand edit would have added — the
// breadcrumb, the visible title, the content-and-aside grid, and a sidebar that
// makes a 2000-word page navigable — so a refactor that quietly drops one fails
// here.
func TestGeneratedPagesCarryTheSharedChrome(t *testing.T) {
	for _, target := range targets() {
		t.Run(target.path, func(t *testing.T) {
			page := string(target.render())

			assert.Contains(t, page, `  crumb="`,
				"the layout renders the breadcrumb from this prop")
			assert.Contains(t, page, `<div class="doc">`)
			assert.Contains(t, page, `<div class="doc-main">`)
			assert.Contains(t, page, `<aside class="doc-aside">`)
			assert.Contains(t, page, "<h1>")
			assert.Contains(t, page, `<h2 class="section-label">On this page</h2>`)

			// The chrome moved into the layout's global block. A page that
			// still carried its own copy would be styling itself twice and
			// drifting from the other three.
			assert.NotContains(t, page, "--ga-fg-muted")
			assert.NotContains(t, page, "#a1a1a1")
			assert.NotContains(t, page, "#3b82f6")
			assert.NotContains(t, page, `class="back"`)
		})
	}
}

// TestSidebarIndexesEverySection is the assertion that keeps the aside honest.
//
// It is derived from the same walk the page is built from, so the way it goes
// wrong is not a missing entry but a silently empty list.
func TestSidebarIndexesEverySection(t *testing.T) {
	for _, target := range targets() {
		t.Run(target.path, func(t *testing.T) {
			page := string(target.render())
			_, aside, ok := strings.Cut(page, `<aside class="doc-aside">`)
			require.True(t, ok)

			for _, id := range sectionIDs(page) {
				assert.Contains(t, aside, `href="#`+id+`"`,
					"section %q is not reachable from the sidebar", id)
			}
		})
	}
}

// sectionIDs is every `<section id="…">` on a page, in order.
func sectionIDs(page string) []string {
	var out []string
	for _, rest := range strings.Split(page, `<section id="`)[1:] {
		if id, _, ok := strings.Cut(rest, `"`); ok {
			out = append(out, id)
		}
	}
	return out
}
