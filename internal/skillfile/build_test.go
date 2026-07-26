package skillfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const baseSkill = `---
name: reviewer
version: 1.0.0
description: reviews code
language: Python
---

# Reviewer
`

// context writes a build context and returns its directory.
func context(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}
	return dir
}

// build parses and runs a Skillfile against a context.
func build(t *testing.T, src string, files map[string]string) (*Tree, *Report) {
	t.Helper()
	sf, err := Parse([]byte(src))
	require.NoError(t, err)
	tree, report, err := Build(sf, context(t, files), nil)
	require.NoError(t, err)
	return tree, report
}

func TestFromLocalLoadsTheBase(t *testing.T) {
	tree, _ := build(t, "FROM ./base\n", map[string]string{
		"base/SKILL.md":              baseSkill,
		"base/sections/checklist.md": "- a\n",
	})
	assert.Equal(t, []string{"SKILL.md", "sections/checklist.md"}, tree.Paths())
}

func TestRmRemovesFilesAndDirectories(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nRM sections/house-style.md\nRM reference\n", map[string]string{
		"base/SKILL.md":                baseSkill,
		"base/sections/house-style.md": "style\n",
		"base/sections/checklist.md":   "- a\n",
		"base/reference/one.md":        "1\n",
		"base/reference/two.md":        "2\n",
	})
	assert.Equal(t, []string{"SKILL.md", "sections/checklist.md"}, tree.Paths())
}

// Removing something already gone usually means the base moved underneath the
// Skillfile, so it is worth saying rather than passing silently.
func TestRmOnAnAbsentPathFails(t *testing.T) {
	sf, err := Parse([]byte("FROM ./base\nRM nope.md\n"))
	require.NoError(t, err)
	_, _, err = Build(sf, context(t, map[string]string{"base/SKILL.md": baseSkill}), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file")
}

func TestAppendHeredoc(t *testing.T) {
	tree, _ := build(t, `FROM ./base
APPEND sections/checklist.md <<EOF
- table-driven tests
EOF
`, map[string]string{
		"base/SKILL.md":              baseSkill,
		"base/sections/checklist.md": "- a\n",
	})

	body, ok := tree.Get("sections/checklist.md")
	require.True(t, ok)
	assert.Equal(t, "- a\n- table-driven tests\n", string(body))
}

// SPEC.md 8.6: a payload containing {{ }} must survive the build untouched.
func TestAppendPreservesTemplates(t *testing.T) {
	tree, _ := build(t, `FROM ./base
APPEND SKILL.md <<EOF
Model: {{ .Values.model }}
EOF
`, map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	assert.Contains(t, string(body), "{{ .Values.model }}")
}

func TestAppendCreatesAnAbsentFile(t *testing.T) {
	tree, _ := build(t, `FROM ./base
APPEND notes.md <<EOF
first line
EOF
`, map[string]string{"base/SKILL.md": baseSkill})

	body, ok := tree.Get("notes.md")
	require.True(t, ok)
	assert.Equal(t, "first line\n", string(body))
}

func TestReplaceWithSubmatchExpansion(t *testing.T) {
	tree, _ := build(t, `FROM ./base
REPLACE SKILL.md "language: (\w+)" "language: Go # was $1"
`, map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	assert.Contains(t, string(body), "language: Go # was Python")
}

// SPEC.md 8.2.2: --count limits to the first N.
func TestReplaceCount(t *testing.T) {
	tree, _ := build(t, `FROM ./base
REPLACE notes.md --count=2 "x" "y"
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": "x x x x\n",
	})

	body, _ := tree.Get("notes.md")
	assert.Equal(t, "y y x x\n", string(body))
}

// 8.2.2: zero matches is a warning, not an error — that is what makes
// idempotent and defensive edits expressible — but it is counted.
func TestReplaceZeroMatchesWarnsAndIsReported(t *testing.T) {
	tree, report := build(t, `FROM ./base
REPLACE SKILL.md "nothing matches this" "x"
`, map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	assert.Equal(t, baseSkill, string(body), "the file is left unchanged")
	require.Len(t, report.NoOpReplaces, 1)
	assert.Contains(t, report.NoOpReplaces[0], "matched nothing")
}

// SPEC.md 8.2.4: structure-aware, so it cannot produce invalid YAML.
func TestSetEditsFrontmatter(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET language Go\n",
		map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	doc, err := openYAML("SKILL.md", body)
	require.NoError(t, err)
	assert.Equal(t, "Go", doc.values["language"])
	// The Markdown after the block survives.
	assert.Contains(t, string(body), "# Reviewer")
}

func TestSetAddsAnAbsentKey(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET reviewer-level strict\n",
		map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	doc, _ := openYAML("SKILL.md", body)
	assert.Equal(t, "strict", doc.values["reviewer-level"])
}

func TestSetTargetsAnyYAMLWithFileFlag(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET --file=values.yaml model sonnet\n",
		map[string]string{
			"base/SKILL.md":    baseSkill,
			"base/values.yaml": "model: opus\n",
		})

	body, _ := tree.Get("values.yaml")
	doc, err := openYAML("values.yaml", body)
	require.NoError(t, err)
	assert.Equal(t, "sonnet", doc.values["model"])
}

// 8.2.4: untargeted files are never re-serialised and stay byte-identical.
func TestUntargetedFilesAreByteIdentical(t *testing.T) {
	const fussy = "key:   value    # spacing that a re-serialise would normalise\nlist:\n    - deeply indented\n"

	tree, _ := build(t, "FROM ./base\nSET language Go\n", map[string]string{
		"base/SKILL.md":    baseSkill,
		"base/values.yaml": fussy,
	})

	body, _ := tree.Get("values.yaml")
	assert.Equal(t, fussy, string(body))
}

func TestUnsetRemovesAKey(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nUNSET language\n",
		map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	doc, _ := openYAML("SKILL.md", body)
	assert.NotContains(t, doc.values, "language")
}

// 8.2.4: UNSET on an absent key warns and continues.
func TestUnsetOnAnAbsentKeyWarns(t *testing.T) {
	_, report := build(t, "FROM ./base\nUNSET nope\n",
		map[string]string{"base/SKILL.md": baseSkill})

	require.Len(t, report.MissingUnsets, 1)
	assert.Contains(t, report.MissingUnsets[0], "already absent")
}

// SPEC.md 8.4: multiple FROM … AS, composed by explicit COPY --from.
func TestMultiStageCopy(t *testing.T) {
	tree, _ := build(t, `FROM ./pdf AS pdf
FROM ./base
COPY --from=pdf sections/pdf.md sections/
`, map[string]string{
		"base/SKILL.md":       baseSkill,
		"pdf/SKILL.md":        baseSkill,
		"pdf/sections/pdf.md": "pdf notes\n",
	})

	body, ok := tree.Get("sections/pdf.md")
	require.True(t, ok)
	assert.Equal(t, "pdf notes\n", string(body))
	// Explicit enumeration, not merge-by-default: nothing else came across.
	assert.Equal(t, []string{"SKILL.md", "sections/pdf.md"}, tree.Paths())
}

func TestCopyFromAnUnknownStageFails(t *testing.T) {
	sf, err := Parse([]byte("FROM ./base\nCOPY --from=ghost x.md .\n"))
	require.NoError(t, err)
	_, _, err = Build(sf, context(t, map[string]string{"base/SKILL.md": baseSkill}), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no stage named")
}

// A later instruction wins over an earlier one touching the same bytes (8.2).
func TestLaterInstructionWins(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET language Go\nSET language Rust\n",
		map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	doc, _ := openYAML("SKILL.md", body)
	assert.Equal(t, "Rust", doc.values["language"])
}

func TestArgDefaultAndOverride(t *testing.T) {
	src := "ARG lang=Python\nFROM ./base\nSET language $lang\n"
	files := map[string]string{"base/SKILL.md": baseSkill}

	sf, err := Parse([]byte(src))
	require.NoError(t, err)

	tree, _, err := Build(sf, context(t, files), nil)
	require.NoError(t, err)
	body, _ := tree.Get("SKILL.md")
	doc, _ := openYAML("SKILL.md", body)
	assert.Equal(t, "Python", doc.values["language"], "the ARG default applies")

	sf2, _ := Parse([]byte(src))
	tree2, _, err := Build(sf2, context(t, files), map[string]string{"lang": "Go"})
	require.NoError(t, err)
	body2, _ := tree2.Get("SKILL.md")
	doc2, _ := openYAML("SKILL.md", body2)
	assert.Equal(t, "Go", doc2.values["language"], "--build-arg beats the default")
}

// 2.5's path rules hold at build time too: a base that could escape its root
// here would escape it at install.
func TestBuildCannotWriteOutsideTheSkill(t *testing.T) {
	sf, err := Parse([]byte("FROM ./base\nAPPEND ../escape.md <<EOF\nx\nEOF\n"))
	require.NoError(t, err)
	_, _, err = Build(sf, context(t, map[string]string{"base/SKILL.md": baseSkill}), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the skill root")
}

func TestSkillfileWithoutFrom(t *testing.T) {
	sf, err := Parse([]byte("RM x.md\n"))
	require.NoError(t, err)
	_, _, err = Build(sf, t.TempDir(), nil)
	require.Error(t, err)
}

// SPEC.md 8.1: a build is a pure function of its inputs.
func TestBuildIsDeterministic(t *testing.T) {
	src := `FROM ./base
RM sections/house-style.md
SET language Go
APPEND sections/checklist.md <<EOF
- table-driven tests
EOF
`
	files := map[string]string{
		"base/SKILL.md":                baseSkill,
		"base/sections/house-style.md": "style\n",
		"base/sections/checklist.md":   "- a\n",
	}

	first, _ := build(t, src, files)
	second, _ := build(t, src, files)

	require.Equal(t, first.Paths(), second.Paths())
	for _, p := range first.Paths() {
		a, _ := first.Get(p)
		b, _ := second.Get(p)
		assert.Equal(t, string(a), string(b), "%s differs between builds", p)
	}
}
