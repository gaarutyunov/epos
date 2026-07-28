package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/epos/internal/skillfile"
)

// committed is the page in the repository, from this package's directory.
func committed() string {
	return filepath.Join("..", "..", filepath.FromSlash(defaultOut))
}

// TestCommittedPageIsUpToDate is the drift check of SPEC.md 14.1, in the form
// that runs on every platform of the test matrix.
//
// CI runs the same check as an explicit step, so a reviewer reading the
// workflow can see it; this is what makes it fail for anyone who edits the
// instruction table and runs `go test` before pushing.
func TestCommittedPageIsUpToDate(t *testing.T) {
	require.NoError(t, run(committed(), true),
		"the committed reference page no longer matches the instruction table")
}

// TestCheckFailsOnAHandEditedPage is the other half, and the one worth having:
// a drift check that cannot fail is the failure mode.
func TestCheckFailsOnAHandEditedPage(t *testing.T) {
	original, err := os.ReadFile(committed())
	require.NoError(t, err)

	// One word, changed the way a reader who spotted a wording nit would change
	// it. Nothing about the edit needs to be malformed for the check to catch
	// it — that is the property being pinned.
	edited := filepath.Join(t.TempDir(), "skillfile.astro")
	require.NoError(t, os.WriteFile(edited,
		[]byte(strings.Replace(string(original), "Skillfile reference", "Skillfile handbook", 1)), 0o644))

	err = run(edited, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of date")
}

// TestRenderIsByteStable is what makes the drift check meaningful. A generator
// that ranged a Go map would produce a different file run to run, and the check
// would fail for everyone regardless of what they changed.
func TestRenderIsByteStable(t *testing.T) {
	first := render()
	for range 5 {
		assert.Equal(t, string(first), string(render()))
	}
}

// TestRenderUsesOneLineEnding pins the output's newlines to LF on every host.
// Windows is in the CI matrix, and a page whose line endings came from the
// machine that generated it would fail the drift check on one leg of it.
func TestRenderUsesOneLineEnding(t *testing.T) {
	assert.NotContains(t, string(render()), "\r")
}

// TestEveryInstructionHasASection is the surface the issue's checklist asks
// for: every instruction, with its syntax and a worked example, on the page.
func TestEveryInstructionHasASection(t *testing.T) {
	out := string(render())

	for _, doc := range skillfile.NewReference().Instructions {
		assert.Contains(t, out, `<section id="`+strings.ToLower(doc.Op)+`">`,
			"%s has no section", doc.Op)
		assert.Contains(t, out, escape(doc.Syntax), "%s has no syntax line", doc.Op)
		assert.Contains(t, out, escape(strings.TrimRight(doc.Example.Skillfile, "\n")),
			"%s has no worked example", doc.Op)
	}
}

// TestMultiStageAndValuesAreCovered pins the two composed subjects, which are
// checklist items in their own right and are not any one instruction.
func TestMultiStageAndValuesAreCovered(t *testing.T) {
	out := string(render())

	assert.Contains(t, out, `<section id="multi-stage">`)
	assert.Contains(t, out, escape("COPY --from=shared reference.md references/shared.md"))
	assert.Contains(t, out, escape("FROM ./shared AS shared"))
	assert.Contains(t, out, `<section id="values-and-templating">`)
	assert.Contains(t, out, escape("model: '{{ .Values.model }}'"))
}

// TestMarkupCarriesNoBareBraces is why escape exists. Astro reads a bare `{` in
// markup as the start of a JavaScript expression, and this page documents a
// template language made of them — an unescaped one would either break the
// build or silently swallow the text it opened.
//
// The frontmatter fence and the <style> element are excluded: the first is
// TypeScript and the second is raw text to Astro, so braces there are meant.
func TestMarkupCarriesNoBareBraces(t *testing.T) {
	out := string(render())

	_, afterFrontmatter, ok := strings.Cut(strings.TrimPrefix(out, "---\n"), "\n---\n")
	require.True(t, ok, "the page has no frontmatter fence")

	markup, _, ok := strings.Cut(afterFrontmatter, "<style>")
	require.True(t, ok, "the page has no style element")

	// The one expression the page writes on purpose: the base-relative link
	// back to the landing page.
	markup = strings.ReplaceAll(markup, `{href("")}`, "")

	assert.NotContains(t, markup, "{")
	assert.NotContains(t, markup, "}")
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
