package skillfile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The instruction table is the source the reference page is generated from
// (SPEC.md 14.1), so its rows have to be true of the builder rather than of the
// prose. These tests are what makes that hold: every documented example is run
// against the real builder, and every instruction the builder dispatches has to
// be a row.

// TestDocumentedExamplesBuild runs every worked example on the page.
//
// A documented result that stopped being true fails here, which is the whole
// point of holding examples as data: an example in a hand-written page is
// checked by whoever happens to read it.
func TestDocumentedExamplesBuild(t *testing.T) {
	ref := NewReference()

	for _, doc := range ref.Instructions {
		t.Run(doc.Op, func(t *testing.T) {
			runExample(t, doc.Example)
		})
	}
	for _, topic := range ref.Topics {
		if topic.Example == nil {
			continue
		}
		t.Run(topic.Slug, func(t *testing.T) {
			runExample(t, *topic.Example)
		})
	}
}

// runExample builds one example and checks it produced what the page claims.
func runExample(t *testing.T, ex Example) {
	t.Helper()

	files := map[string]string{}
	for _, f := range ex.Context {
		files[f.Path] = f.Body
	}
	dir := buildContext(t, files)

	sf, err := Parse([]byte(ex.Skillfile))
	require.NoError(t, err)

	tree, report, err := Build(sf, dir, ex.BuildArgs)
	require.NoError(t, err)

	if ex.Result.Path != "" {
		body, ok := tree.Get(ex.Result.Path)
		require.True(t, ok, "the example produced no %s", ex.Result.Path)
		assert.Equal(t, ex.Result.Body, string(body))
	}
	for _, p := range ex.Absent {
		_, ok := tree.Get(p)
		assert.False(t, ok, "%s should not be in the built tree", p)
	}

	warnings := append(append([]string{}, report.NoOpReplaces...), report.MissingUnsets...)
	assert.Equal(t, ex.Warnings, emptyToNil(warnings))
}

// emptyToNil makes an empty slice compare equal to an unset field, so an
// example with nothing to warn about writes no Warnings at all.
func emptyToNil(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

// TestEveryDispatchedInstructionIsDocumented is the other half of the one-table
// rule. Dispatch already goes through the table, so an undocumented instruction
// cannot run — but a row with no example, or a syntax line that has stopped
// naming the instruction, would still reach the page.
func TestEveryDispatchedInstructionIsDocumented(t *testing.T) {
	ref := NewReference()
	require.Len(t, ref.Instructions, len(instructionByOp))

	seen := map[string]bool{}
	for _, doc := range ref.Instructions {
		assert.NotEmpty(t, doc.Summary, "%s has no summary", doc.Op)
		assert.NotEmpty(t, doc.Notes, "%s has no notes", doc.Op)
		assert.True(t, strings.HasPrefix(doc.Syntax, doc.Op+" ") || doc.Syntax == doc.Op,
			"%s: syntax %q does not start with the instruction", doc.Op, doc.Syntax)
		assert.NotEmpty(t, doc.Example.Skillfile, "%s has no worked example", doc.Op)
		assert.Contains(t, doc.Example.Skillfile, doc.Op,
			"%s: the worked example does not use the instruction", doc.Op)

		assert.False(t, seen[doc.Op], "%s is in the table twice", doc.Op)
		seen[doc.Op] = true

		table, ok := instructionByOp[doc.Op]
		require.True(t, ok, "%s is documented but not dispatched", doc.Op)
		require.NotNil(t, table.apply, "%s is documented but bound to nothing", doc.Op)
	}
}

// TestEveryInstructionInTheSpecIsInTheTable pins the instruction set itself.
//
// The list is SPEC.md 8.2's, written out rather than derived, so dropping an
// instruction from the table fails here instead of quietly shrinking the
// reference page.
func TestEveryInstructionInTheSpecIsInTheTable(t *testing.T) {
	for _, op := range []string{
		"ARG", "FROM", "COPY", "RM", "APPEND", "REPLACE", "PATCH", "AWK", "SET", "UNSET",
	} {
		_, ok := instructionByOp[op]
		assert.True(t, ok, "%s is in SPEC.md 8.2 but not in the instruction table", op)
	}
}

// TestReferenceIsACopy guards the table against a generator that edits what it
// renders. Dispatch reads the same rows.
func TestReferenceIsACopy(t *testing.T) {
	ref := NewReference()
	require.NotEmpty(t, ref.Instructions)

	ref.Instructions[0].Op = "MANGLED"
	assert.Equal(t, "ARG", NewReference().Instructions[0].Op)
}

// TestFromSourcesCoverEveryScheme checks the FROM table against the schemes
// resolve actually distinguishes (8.3, plus 8.4's stage reference).
func TestFromSourcesCoverEveryScheme(t *testing.T) {
	want := []string{"Local", "Git", "OCI", "Stage"}

	var got []string
	for _, s := range NewReference().Sources {
		got = append(got, s.Scheme)
		assert.NotEmpty(t, s.Example, "%s has no example reference", s.Scheme)
		assert.NotEmpty(t, s.Pin, "%s does not say what it pins", s.Scheme)
	}
	assert.Equal(t, want, got)
}

// TestUnquotedTemplateInFrontmatterFails is the caveat the Values and
// templating topic documents, held to the behaviour rather than to the note.
//
// A bare `{` opens a YAML flow mapping, so an unquoted template in a
// frontmatter *value* is a YAML syntax error — which surfaces the moment
// anything reads the frontmatter, and `epos build` reads it to name the
// artifact. Quoting it is what the topic tells the reader to do, and the
// example above proves the quoted form survives the build.
func TestUnquotedTemplateInFrontmatterIsNotValidYAML(t *testing.T) {
	dir := buildContext(t, map[string]string{
		"base/SKILL.md": "---\nname: reviewer\nmodel: {{ .Values.model }}\n---\nReviews changes.\n",
	})

	sf, err := Parse([]byte("FROM ./base\nSET description x\n"))
	require.NoError(t, err)

	_, _, err = Build(sf, dir, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SKILL.md")
}

// TestQuotedTemplateInFrontmatterSurvivesAYAMLEdit is the other half: the form
// the topic recommends is not merely parseable, it comes back written as it was
// after an unrelated SET.
func TestQuotedTemplateInFrontmatterSurvivesAYAMLEdit(t *testing.T) {
	dir := buildContext(t, map[string]string{
		"base/SKILL.md": "---\nname: reviewer\nmodel: '{{ .Values.model }}'\n---\nReviews changes.\n",
	})

	sf, err := Parse([]byte("FROM ./base\nSET description x\n"))
	require.NoError(t, err)

	tree, _, err := Build(sf, dir, nil)
	require.NoError(t, err)

	body, ok := tree.Get("SKILL.md")
	require.True(t, ok)
	assert.Contains(t, string(body), "model: '{{ .Values.model }}'")
}
