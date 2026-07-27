package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeValues drops a values.yaml in a directory the test owns.
func writeValues(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadValuesReadsYAML(t *testing.T) {
	path := writeValues(t, "title: Reviewer\nmodel: opus\n")

	values, err := LoadValues([]string{path}, nil)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"title": "Reviewer", "model": "opus"}, values.Scope(""))
}

func TestLoadValuesMergesFilesInOrder(t *testing.T) {
	first := writeValues(t, "title: First\nnested:\n  a: 1\n  b: 2\n")
	second := writeValues(t, "title: Second\nnested:\n  b: 3\n")

	values, err := LoadValues([]string{first, second}, nil)
	require.NoError(t, err)

	scope := values.Scope("")
	assert.Equal(t, "Second", scope["title"])
	// Merged key by key, not file by file: `a` came from the first file and
	// the second file's `nested` must not have replaced the whole block.
	assert.Equal(t, map[string]any{"a": uint64(1), "b": uint64(3)}, scope["nested"])
}

func TestSetBeatsAValuesFile(t *testing.T) {
	path := writeValues(t, "title: FromFile\n")

	values, err := LoadValues([]string{path}, []string{"title=FromFlag"})
	require.NoError(t, err)

	assert.Equal(t, "FromFlag", values.Scope("")["title"])
}

func TestSetNamesNestedKeysWithDots(t *testing.T) {
	values, err := LoadValues(nil, []string{"shared.title=Shared", "global.org=Acme"})
	require.NoError(t, err)

	assert.Equal(t, "Shared", values.Scope("shared")["title"])
	assert.Equal(t, map[string]any{"org": "Acme"}, values.Scope("shared")[GlobalKey])
}

func TestSetRejectsWhatIsNotKeyValue(t *testing.T) {
	for _, set := range []string{"title", "=Reviewer", "a..b=c"} {
		_, err := LoadValues(nil, []string{set})
		assert.Error(t, err, "--set %q", set)
	}
}

// SPEC.md 10.3: the whole reason values nest under the stage name. Two stages
// both writing .Values.title is the case that breaks without scoping, and the
// case that must keep working with it.
func TestTwoStagesUseTheSameKeyWithoutColliding(t *testing.T) {
	path := writeValues(t, `
global:
  org: Acme
title: The final stage
shared:
  title: The shared stage
docs:
  title: The docs stage
`)

	values, err := LoadValues([]string{path}, nil)
	require.NoError(t, err)

	assert.Equal(t, "The final stage", values.Scope("")["title"])
	assert.Equal(t, "The shared stage", values.Scope("shared")["title"])
	assert.Equal(t, "The docs stage", values.Scope("docs")["title"])
}

// The other half of 10.3: global is the deliberate cross-stage channel, so it
// is the one thing every scope can see.
func TestGlobalIsVisibleFromEveryScope(t *testing.T) {
	path := writeValues(t, "global:\n  org: Acme\nshared:\n  title: Shared\n")

	values, err := LoadValues([]string{path}, nil)
	require.NoError(t, err)

	want := map[string]any{"org": "Acme"}
	for _, stage := range []string{"", "shared", "never-declared"} {
		assert.Equal(t, want, values.Scope(stage)[GlobalKey], "scope %q", stage)
	}
}

// A stage scope carries the stage's keys and nothing from its neighbours.
func TestAStageScopeDoesNotLeakIntoAnother(t *testing.T) {
	path := writeValues(t, "shared:\n  title: Shared\ndocs:\n  heading: Docs\n")

	values, err := LoadValues([]string{path}, nil)
	require.NoError(t, err)

	assert.NotContains(t, values.Scope("shared"), "heading")
	assert.NotContains(t, values.Scope("docs"), "title")
}

// A stage cannot cut itself off from global by declaring a key of that name.
func TestAStageCannotShadowGlobal(t *testing.T) {
	path := writeValues(t, "global:\n  org: Acme\nshared:\n  global:\n    org: Impostor\n")

	values, err := LoadValues([]string{path}, nil)
	require.NoError(t, err)

	assert.Equal(t, map[string]any{"org": "Acme"}, values.Scope("shared")[GlobalKey])
}

// A YAML document whose keys are not all strings decodes to map[any]any, which
// text/template cannot index by field name. The conversion happens on the way
// in, so the failure never reaches a render.
func TestNonStringKeysBecomeStringKeys(t *testing.T) {
	path := writeValues(t, "shared:\n  1: one\n  true: yes please\n")

	values, err := LoadValues([]string{path}, nil)
	require.NoError(t, err)

	scope := values.Scope("shared")
	assert.Equal(t, "one", scope["1"])
	assert.Equal(t, "yes please", scope["true"])
}

func TestLoadValuesReportsAMissingFile(t *testing.T) {
	_, err := LoadValues([]string{filepath.Join(t.TempDir(), "nope.yaml")}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope.yaml")
}

func TestLoadValuesReportsBrokenYAML(t *testing.T) {
	path := writeValues(t, "title: [unterminated\n")

	_, err := LoadValues([]string{path}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "values.yaml")
}
