package skillfile

import (
	"fmt"
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

// buildContext writes a build context and returns its directory. Named for the
// package it is not: `context` would collide with the standard library import
// AWK's deadline needs, since a test file shares the package's scope.
func buildContext(t *testing.T, files map[string]string) string {
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
	tree, report, err := Build(sf, buildContext(t, files), nil)
	require.NoError(t, err)
	return tree, report
}

// failedBuild runs a Skillfile expected to fail, returning the error and the
// tree as it stood when the failing instruction gave up.
//
// Build itself returns no tree on failure, which is the right contract but
// hides the thing a fatal instruction has to be checked for: that it left the
// tree alone rather than writing half of its work.
func failedBuild(t *testing.T, src string, files map[string]string) (*Tree, error) {
	t.Helper()
	sf, err := Parse([]byte(src))
	require.NoError(t, err)

	b := &builder{
		contextDir: buildContext(t, files),
		args:       map[string]string{},
		stages:     map[string]*Tree{},
		report:     &Report{},
	}
	for _, inst := range sf.Instructions {
		if err := b.apply(inst); err != nil {
			return b.current, fmt.Errorf("line %d: %s: %w", inst.Line, inst.Op, err)
		}
	}
	require.Fail(t, "the Skillfile was expected to fail but built cleanly")
	return nil, nil
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
	_, _, err = Build(sf, buildContext(t, map[string]string{"base/SKILL.md": baseSkill}), nil)
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

const notes = "line one\nline two\nline three\n"

// A `git diff` of notes, rewriting its middle line.
const notesDiff = `--- a/notes.md
+++ b/notes.md
@@ -1,3 +1,3 @@
 line one
-line two
+line 2
 line three
`

// SPEC.md 8.2.1: PATCH is the precise instruction, applied with go-gitdiff.
func TestPatchAppliesAUnifiedDiff(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nPATCH notes.md notes.diff\n", map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": notes,
		"notes.diff":    notesDiff,
	})

	body, ok := tree.Get("notes.md")
	require.True(t, ok)
	assert.Equal(t, "line one\nline 2\nline three\n", string(body))
}

// 8.2.1: the hunk applies at the line its header records — no offset search,
// no fuzz. An unrelated insertion above it fails the build even though every
// context line still matches, which is stricter than `git apply` on purpose:
// a patch that quietly relocates would change the artifact's digest without
// anything in the Skillfile changing.
func TestPatchFailsOnALineNumberShift(t *testing.T) {
	shifted := "inserted upstream\n" + notes

	tree, err := failedBuild(t, "FROM ./base\nPATCH notes.md notes.diff\n", map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": shifted,
		"notes.diff":    notesDiff,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "conflict")

	body, ok := tree.Get("notes.md")
	require.True(t, ok)
	assert.Equal(t, shifted, string(body),
		"a failed PATCH is fatal and partial: nothing may reach the tree")
}

func TestPatchFailsOnAMalformedDiff(t *testing.T) {
	_, err := failedBuild(t, "FROM ./base\nPATCH notes.md notes.diff\n", map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": notes,
		"notes.diff":    "--- a/notes.md\n+++ b/notes.md\n@@ -1,3 +1,3 @@\nnot a hunk line\n",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid line operation")
}

// go-gitdiff treats anything it does not recognise as a mail preamble and
// returns no files and no error, so a payload that is not a diff would be a
// silent no-op if the instruction did not check for it.
func TestPatchFailsWhenThePayloadIsNotADiff(t *testing.T) {
	_, err := failedBuild(t, "FROM ./base\nPATCH notes.md notes.diff\n", map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": notes,
		"notes.diff":    "this is prose, not a diff\n",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no file diff")
}

// SPEC.md 8.2.3: the file's content is stdin, the program's stdout is the file.
func TestAWKFiltersAFileThroughStdinAndStdout(t *testing.T) {
	tree, _ := build(t, `FROM ./base
AWK notes.md <<EOF
{ print NR ": " $0 }
EOF
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": "alpha\nbeta\n",
	})

	body, ok := tree.Get("notes.md")
	require.True(t, ok)
	assert.Equal(t, "1: alpha\n2: beta\n", string(body))
}

// SPEC.md 2.4: identical inputs must produce an identical digest on every
// platform. goawk's NewlineOutput defaults to SmartNewlineMode, which sets its
// CRLF flag from runtime.GOOS and rewrites every \n it prints as \r\n on
// Windows — so until runAWK pinned the mode to Raw, this build produced a
// different layer, and so a different digest, on Windows than on Linux.
//
// Nothing here may be relaxed to accept either line ending. Accepting both is
// accepting two digests, which is the bug rather than the fix.
func TestAWKOutputLineEndingsDoNotDependOnTheHost(t *testing.T) {
	const lf = "alpha\nbeta\ngamma\n"

	tree, _ := build(t, `FROM ./base
AWK notes.md <<EOF
{ print }
EOF
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": lf,
	})

	body, ok := tree.Get("notes.md")
	require.True(t, ok)
	assert.Equal(t, lf, string(body))
	assert.NotContains(t, string(body), "\r",
		"a CR here means the host OS leaked into the artifact")
}

// The other half of that decision, pinned so it reads as a choice rather than
// an accident. goawk strips a trailing CR at the record boundary, so $0 never
// carries it and a CRLF file comes back LF. That is a normalisation, not a
// round trip — and it is the one goawk leaves available, since the only mode
// that re-emits CRLF re-emits it for every line and would corrupt an LF file.
func TestAWKNormalisesCRLFInputToLF(t *testing.T) {
	tree, _ := build(t, `FROM ./base
AWK notes.md <<EOF
{ print }
EOF
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": "alpha\r\nbeta\r\n",
	})

	body, _ := tree.Get("notes.md")
	assert.Equal(t, "alpha\nbeta\n", string(body))
}

// Raw mode is a switch on goawk, not a scrub of its output: a CR the script
// asks for by name still survives. This is exactly what a \r\n → \n pass over
// the captured stdout would eat, so it guards the fix against being
// "simplified" into post-processing later.
func TestAWKKeepsCarriageReturnsAScriptWritesItself(t *testing.T) {
	tree, _ := build(t, `FROM ./base
AWK notes.md <<EOF
BEGIN { printf "alpha\r\nbeta\r\n" }
EOF
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": notes,
	})

	body, _ := tree.Get("notes.md")
	assert.Equal(t, "alpha\r\nbeta\r\n", string(body))
}

// 8.2.3: rejected by a post-parse check, because a digest that varies across
// builds of identical inputs breaks 2.4.
func TestAWKRejectsTheNondeterministicBuiltins(t *testing.T) {
	for _, tt := range []struct{ name, script, wants string }{
		{"rand", "{ print rand() }", "rand()"},
		{"srand", "BEGIN { srand() }\n{ print }", "srand()"},
		{"srand with a seed", "BEGIN { srand(42) }\n{ print }", "srand()"},
		{"systime", "{ print systime() }", "systime()"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := failedBuild(t, "FROM ./base\nAWK notes.md <<EOF\n"+tt.script+"\nEOF\n",
				map[string]string{
					"base/SKILL.md": baseSkill,
					"base/notes.md": notes,
				})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wants)
		})
	}
}

// The check reads the compiled program, not the script text, so the same words
// inside a string literal or a regex are left alone.
func TestAWKAllowsTheRejectedNamesAsText(t *testing.T) {
	tree, _ := build(t, `FROM ./base
AWK notes.md <<EOF
/rand/ { print "srand systime" }
!/rand/ { print }
EOF
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": "keep me\nrandom line\n",
	})

	body, _ := tree.Get("notes.md")
	assert.Equal(t, "keep me\nsrand systime\n", string(body))
}

func TestAWKFailsOnAParseError(t *testing.T) {
	_, err := failedBuild(t, "FROM ./base\nAWK notes.md <<EOF\n{ print $1\nEOF\n",
		map[string]string{
			"base/SKILL.md": baseSkill,
			"base/notes.md": notes,
		})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse error")
}

// 8.2.3: AWK is Turing-complete, so execution is bound to a deadline. The
// default is 10s; --timeout shortens it here so the test does not wait.
func TestAWKTimesOutOnAnInfiniteLoop(t *testing.T) {
	_, err := failedBuild(t, "FROM ./base\nAWK notes.md --timeout=100ms <<EOF\nBEGIN { while (1) x++ }\nEOF\n",
		map[string]string{
			"base/SKILL.md": baseSkill,
			"base/notes.md": notes,
		})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not finish within 100ms")
}

// The sandbox is mandatory and not configurable: with NoExec, NoFileWrites,
// NoFileReads and an empty Environ the program is a pure stdin→stdout
// function, which is what keeps AWK compatible with 8.1's no-RUN rule.
func TestAWKIsSandboxed(t *testing.T) {
	for _, tt := range []struct{ name, script, wants string }{
		{"no system()", `BEGIN { system("echo hi") }`, "NoExec"},
		{"no pipes", `BEGIN { "echo hi" | getline x }`, "NoExec"},
		{"no file writes", `BEGIN { print "x" > "escape.txt" }`, "NoFileWrites"},
		{"no file reads", `BEGIN { getline x < "SKILL.md" }`, "NoFileReads"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := failedBuild(t, "FROM ./base\nAWK notes.md <<EOF\n"+tt.script+"\nEOF\n",
				map[string]string{
					"base/SKILL.md": baseSkill,
					"base/notes.md": notes,
				})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wants)
		})
	}
}

// An empty Environ is not the same as no Environ: nil would inherit the
// process environment and make the build depend on where it ran.
func TestAWKCannotReadTheEnvironment(t *testing.T) {
	t.Setenv("EPOS_TEST_LEAK", "leaked")

	tree, _ := build(t, `FROM ./base
AWK notes.md <<EOF
BEGIN { print "env=[" ENVIRON["EPOS_TEST_LEAK"] "]" }
EOF
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"base/notes.md": notes,
	})

	body, _ := tree.Get("notes.md")
	assert.Equal(t, "env=[]\n", string(body))
}

// SPEC.md 8.6: a heredoc payload containing {{ }} reaches install untouched —
// the build neither expands it nor lets AWK's own $ syntax collide with it.
func TestAWKPreservesTemplates(t *testing.T) {
	tree, _ := build(t, `FROM ./base
AWK SKILL.md <<EOF
{ print }
END { print "Model: {{ .Values.model }}" }
EOF
`, map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	assert.Equal(t, baseSkill+"Model: {{ .Values.model }}\n", string(body))
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
	_, _, err = Build(sf, buildContext(t, map[string]string{"base/SKILL.md": baseSkill}), nil)
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

	tree, _, err := Build(sf, buildContext(t, files), nil)
	require.NoError(t, err)
	body, _ := tree.Get("SKILL.md")
	doc, _ := openYAML("SKILL.md", body)
	assert.Equal(t, "Python", doc.values["language"], "the ARG default applies")

	sf2, _ := Parse([]byte(src))
	tree2, _, err := Build(sf2, buildContext(t, files), map[string]string{"lang": "Go"})
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
	_, _, err = Build(sf, buildContext(t, map[string]string{"base/SKILL.md": baseSkill}), nil)
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
