package install

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderSubstitutesValues(t *testing.T) {
	got, err := Render("SKILL.md", []byte("model: {{ .Values.model }}\n"),
		map[string]any{"model": "opus"})
	require.NoError(t, err)
	assert.Equal(t, "model: opus\n", string(got))
}

// A file with no action is not parsed at all, so a binary asset and a document
// full of braces-that-are-not-actions both survive byte for byte.
func TestRenderLeavesAFileWithoutActionsAlone(t *testing.T) {
	src := []byte("a { brace } and a \x00 byte\n")

	got, err := Render("reference/notes.md", src, nil)
	require.NoError(t, err)
	assert.Equal(t, src, got)
}

// SPEC.md 10.3: no custom functions. sprig's are the ones a skill author is
// most likely to reach for, so their absence is what the test pins.
func TestRenderHasNoCustomFunctions(t *testing.T) {
	for _, fn := range []string{"upper", "quote", "default", "toYaml", "b64enc"} {
		_, err := Render("SKILL.md", []byte("{{ "+fn+" .Values.title }}"),
			map[string]any{"title": "reviewer"})
		require.Error(t, err, "%s must not be defined", fn)
		assert.Contains(t, err.Error(), "not defined")
	}
}

// The built-ins text/template itself provides are still there: they are the
// language, not an added function set.
func TestRenderKeepsTheTemplateLanguage(t *testing.T) {
	got, err := Render("SKILL.md",
		[]byte("{{ if .Values.verbose }}long{{ else }}short{{ end }}\n"),
		map[string]any{"verbose": ""})
	require.NoError(t, err)
	assert.Equal(t, "short\n", string(got))
}

// A value nobody supplied is an error, not the `<no value>` Go prints by
// default: a skill with a hole in it is worse than an install that stops.
func TestRenderRefusesAMissingValue(t *testing.T) {
	_, err := Render("SKILL.md", []byte("model: {{ .Values.model }}\n"), map[string]any{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SKILL.md")
	assert.Contains(t, err.Error(), "not supplied")
}

// The check counts rather than searches, so a skill that writes the string
// itself is not accused of a hole it does not have.
func TestRenderAllowsTheLiteralNoValueText(t *testing.T) {
	got, err := Render("SKILL.md",
		[]byte("Print <no value> when the key is absent. Model: {{ .Values.model }}\n"),
		map[string]any{"model": "opus"})
	require.NoError(t, err)
	assert.Contains(t, string(got), "<no value>")
	assert.Contains(t, string(got), "opus")
}

// An absent key still tests false in a condition, which is what makes an
// optional value expressible without a helper function.
func TestRenderTreatsAnAbsentKeyAsFalse(t *testing.T) {
	got, err := Render("SKILL.md",
		[]byte("{{ if .Values.extra }}{{ .Values.extra }}{{ end }}done\n"), map[string]any{})
	require.NoError(t, err)
	assert.Equal(t, "done\n", string(got))
}

func TestRenderReportsABrokenTemplate(t *testing.T) {
	_, err := Render("SKILL.md", []byte("{{ .Values.model "), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SKILL.md")
}

// The two halves of 10.3's scoping, at the level rendering sees them.
func TestRenderUsesTheScopeItIsGiven(t *testing.T) {
	values, err := LoadValues(nil,
		[]string{"title=Final", "shared.title=Shared", "global.org=Acme"})
	require.NoError(t, err)

	src := []byte("{{ .Values.title }} at {{ .Values.global.org }}")

	final, err := Render("SKILL.md", src, values.Scope(""))
	require.NoError(t, err)
	assert.Equal(t, "Final at Acme", string(final))

	shared, err := Render("references/shared.md", src, values.Scope("shared"))
	require.NoError(t, err)
	assert.Equal(t, "Shared at Acme", string(shared))
}
