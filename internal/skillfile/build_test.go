package skillfile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
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

	b := newBuilder(sf, buildContext(t, files), nil)
	for _, inst := range sf.Instructions {
		if err := b.apply(inst); err != nil {
			return b.current, fmt.Errorf("line %d: %s: %w", inst.Line, inst.Op, err)
		}
	}
	require.Fail(t, "the Skillfile was expected to fail but built cleanly")
	return nil, nil
}

// yamlBlockOf is a built file's YAML: the frontmatter block of a SKILL.md, or
// the whole file when it is plain YAML.
func yamlBlockOf(body []byte) string {
	if _, block, _, ok := splitFrontmatter(string(body)); ok {
		return block
	}
	return string(body)
}

// frontmatterOf decodes a built file's YAML, for assertions about the values.
func frontmatterOf(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(yamlBlockOf(body)), &out))
	return out
}

// frontmatterKeys is the same YAML's keys in document order.
//
// A map cannot express order, which is exactly the property 8.2.4 promises and
// the one a marshal round trip destroys, so this decodes into goccy's ordered
// MapSlice instead.
func frontmatterKeys(t *testing.T, body []byte) []string {
	t.Helper()
	var items yaml.MapSlice
	require.NoError(t, yaml.Unmarshal([]byte(yamlBlockOf(body)), &items))

	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprint(item.Key))
	}
	return out
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
	assert.Equal(t, "Go", frontmatterOf(t, body)["language"])
	// The Markdown after the block survives.
	assert.Contains(t, string(body), "# Reviewer")
}

func TestSetAddsAnAbsentKey(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET reviewer-level strict\n",
		map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	assert.Equal(t, "strict", frontmatterOf(t, body)["reviewer-level"])
}

func TestSetTargetsAnyYAMLWithFileFlag(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET --file=values.yaml model sonnet\n",
		map[string]string{
			"base/SKILL.md":    baseSkill,
			"base/values.yaml": "model: opus\n",
		})

	body, _ := tree.Get("values.yaml")
	assert.Equal(t, "sonnet", frontmatterOf(t, body)["model"])
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

// fussySkill is frontmatter written the way an author writes it rather than
// the way a serialiser would: a comment over one key, a comment trailing
// another, a deliberate key order, and quoting chosen by hand.
//
// 8.2.4 promises all of that survives an edit, which is why it names the
// mechanism — parser.ParseComments, AST mutation, File.String() — rather than
// leaving the implementation free to unmarshal into a map and marshal back.
// A map round trip loses every one of these properties at once.
const fussySkill = `---
# the fields an agent reads before loading anything
name: reviewer
version: "1.0.0" # pinned by hand, and a string on purpose
description: reviews code
model: sonnet # the cheap one
language: 'Python'
metadata:
  author: acme
---

# Reviewer
`

// fussyOrder is the order fussySkill writes its keys in — which is not
// alphabetical, so an implementation that sorts is visible.
var fussyOrder = []string{"name", "version", "description", "model", "language", "metadata"}

// 8.2.4: a SET that updates an existing key leaves it where it was.
func TestSetKeepsKeyOrderWhenUpdatingAKey(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET model opus\n",
		map[string]string{"base/SKILL.md": fussySkill})

	body, _ := tree.Get("SKILL.md")
	assert.Equal(t, fussyOrder, frontmatterKeys(t, body),
		"an edited key must keep its place, not move to where a sort would put it")
	assert.Equal(t, "opus", frontmatterOf(t, body)["model"])
}

// 8.2.4: a SET that adds a key appends it and disturbs nothing else.
func TestSetAppendsANewKeyWithoutReorderingTheRest(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET allowed-tools [Read,Write]\n",
		map[string]string{"base/SKILL.md": fussySkill})

	body, _ := tree.Get("SKILL.md")
	assert.Equal(t, append(append([]string{}, fussyOrder...), "allowed-tools"),
		frontmatterKeys(t, body))
	assert.Equal(t, []any{"Read", "Write"}, frontmatterOf(t, body)["allowed-tools"],
		"unquoted, so 8.2.4 parses it rather than making it the string it looks like")
}

// 8.2.4: comments survive the edit — the one on its own line above a key and
// the one trailing a key, including the trailing comment of the key being set.
func TestSetPreservesComments(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET model opus\n",
		map[string]string{"base/SKILL.md": fussySkill})

	body, _ := tree.Get("SKILL.md")
	assert.Contains(t, string(body), "# the fields an agent reads before loading anything\nname: reviewer",
		"a full-line comment stays above the key it was written for")
	assert.Contains(t, string(body), "# pinned by hand, and a string on purpose",
		"an inline comment on an untouched key survives")
	assert.Contains(t, string(body), "model: opus # the cheap one",
		"the edited key keeps the comment trailing its line")
}

// 8.2.4: quoting is part of the document, and a key the edit does not name
// must come back written exactly as its author wrote it.
func TestSetPreservesQuotingStyleOfUntouchedKeys(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET description \"reviews Go\"\n",
		map[string]string{"base/SKILL.md": fussySkill})

	body, _ := tree.Get("SKILL.md")
	assert.Contains(t, string(body), `version: "1.0.0"`,
		"a double-quoted string must not silently become the bare 1.0.0")
	assert.Contains(t, string(body), `language: 'Python'`,
		"nor must a single-quoted one change style")
	assert.Equal(t, "1.0.0", frontmatterOf(t, body)["version"],
		"and it is still the string the quotes made it")
}

// 8.2.4: an unquoted value is parsed as a YAML scalar, so it gets the type it
// looks like.
func TestSetParsesUnquotedValuesAsYAMLScalars(t *testing.T) {
	tree, _ := build(t, `FROM ./base
SET count 3
SET ratio 1.5
SET version 1.2
SET enabled true
SET nothing null
`, map[string]string{"base/SKILL.md": fussySkill})

	body, _ := tree.Get("SKILL.md")
	values := frontmatterOf(t, body)
	assert.Equal(t, uint64(3), values["count"])
	assert.Equal(t, 1.5, values["ratio"])
	assert.Equal(t, 1.2, values["version"], "8.2.4's own example: SET version 1.2 yields a float")
	assert.Equal(t, true, values["enabled"])
	assert.Contains(t, values, "nothing")
	assert.Nil(t, values["nothing"])
}

// 8.2.4: quote to force a string.
//
// The tokenizer (8.5) resolves the quotes before the value gets anywhere near
// the YAML editor, so what carries the author's intent is the record that they
// were there — without it a quoted value would be indistinguishable from a bare
// one, and `SET version "1.2"` would write the float this test exists to rule
// out.
func TestSetQuotingForcesAString(t *testing.T) {
	tree, _ := build(t, `FROM ./base
SET count "3"
SET version "1.2"
SET enabled "true"
SET nothing "null"
SET single '4'
`, map[string]string{"base/SKILL.md": fussySkill})

	body, _ := tree.Get("SKILL.md")
	values := frontmatterOf(t, body)
	assert.Equal(t, "3", values["count"], "an integer that was quoted is a string")
	assert.Equal(t, "1.2", values["version"], "and a version is not a float")
	assert.Equal(t, "true", values["enabled"], "nor is a quoted true a bool")
	assert.Equal(t, "null", values["nothing"], "nor a quoted null a null")
	assert.Equal(t, "4", values["single"], "single quotes force a string just as double quotes do")
	assert.Contains(t, string(body), `count: "3"`, "and it is written out as the string it is")
}

// 8.2.4: keys use dotted paths for nested mappings.
func TestSetWritesNestedKeysThroughDottedPaths(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nSET metadata.author globex\nSET metadata.team platform\nSET a.b.c deep\n",
		map[string]string{"base/SKILL.md": fussySkill})

	body, _ := tree.Get("SKILL.md")
	values := frontmatterOf(t, body)
	assert.Equal(t, map[string]any{"author": "globex", "team": "platform"}, values["metadata"])
	assert.Equal(t, map[string]any{"b": map[string]any{"c": "deep"}}, values["a"])
	assert.Equal(t, append(append([]string{}, fussyOrder...), "a"), frontmatterKeys(t, body),
		"writing into an existing mapping must not move it")
}

// Quoting means something to SET (8.2.4) and nothing to anyone else, so an
// otherwise identical Skillfile with every argument quoted has to build the
// same tree — the tokenizer serves every instruction, and a quoted path that
// stopped being the same path would be the change breaking them all at once.
func TestQuotingChangesNothingOutsideSet(t *testing.T) {
	files := map[string]string{
		"base/SKILL.md":         fussySkill,
		"base/stale.md":         "gone\n",
		"base/sections/list.md": "- a\n- b\n",
		"notes.md":              "in-house notes\n",
		"scripts/upper.awk":     `{ print toupper($0) }` + "\n",
	}

	bare, _ := build(t, `ARG level=strict
FROM ./base
COPY notes.md references/notes.md
RM stale.md
APPEND references/notes.md notes.md
REPLACE --count=1 SKILL.md "model: sonnet" "model: opus"
AWK sections/list.md scripts/upper.awk
SET reviewer-level $level
`, files)

	quoted, _ := build(t, `ARG "level=strict"
FROM "./base"
COPY "notes.md" "references/notes.md"
RM "stale.md"
APPEND "references/notes.md" "notes.md"
REPLACE --count="1" "SKILL.md" "model: sonnet" "model: opus"
AWK "sections/list.md" "scripts/upper.awk"
SET "reviewer-level" $level
`, files)

	assert.Equal(t, bare.Files(), quoted.Files(),
		"quoting a path, a pattern, an ARG default or a key must not change the build")
}

func TestUnsetRemovesAKey(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nUNSET language\n",
		map[string]string{"base/SKILL.md": baseSkill})

	body, _ := tree.Get("SKILL.md")
	assert.NotContains(t, frontmatterOf(t, body), "language")
}

// 8.2.4: UNSET takes its key and nothing else — every comment that stays is
// still attached to the key it was written against.
func TestUnsetRemovesOnlyItsKeyAndLeavesCommentsInPlace(t *testing.T) {
	tree, _ := build(t, "FROM ./base\nUNSET description\n",
		map[string]string{"base/SKILL.md": fussySkill})

	body, _ := tree.Get("SKILL.md")
	assert.Equal(t, []string{"name", "version", "model", "language", "metadata"},
		frontmatterKeys(t, body))
	assert.Contains(t, string(body), "# the fields an agent reads before loading anything\nname: reviewer")
	assert.Contains(t, string(body), `version: "1.0.0" # pinned by hand, and a string on purpose`)
	assert.Contains(t, string(body), "model: sonnet # the cheap one")
}

// 8.2.4: structure-aware, so no sequence of edits can produce invalid YAML —
// including on a document whose shapes defeat a byte-oriented edit.
func TestYAMLEditsCannotProduceInvalidYAML(t *testing.T) {
	const awkward = `---
name: reviewer
summary: >
  a folded scalar
  spanning two lines
allowed-tools:
  - Read
  - Write
quoted: "colons: and # hashes"
---

# Reviewer
`

	tree, _ := build(t, "FROM ./base\nSET name auditor\nSET allowed-tools [Read]\nUNSET quoted\nSET added \"a: b # c\"\n",
		map[string]string{"base/SKILL.md": awkward})

	body, _ := tree.Get("SKILL.md")
	values := frontmatterOf(t, body)
	assert.Equal(t, "auditor", values["name"])
	assert.Equal(t, []any{"Read"}, values["allowed-tools"])
	assert.Equal(t, "a: b # c", values["added"], "a value that is not a scalar on its own is written as the string it is")
	assert.NotContains(t, values, "quoted")
	assert.Equal(t, "a folded scalar spanning two lines\n", values["summary"],
		"the folded block scalar survives the edit")
}

// A second edit of an already-edited document must be a no-op when it writes
// the same values, or the build is not the pure function 8.1 claims.
func TestYAMLEditingIsIdempotent(t *testing.T) {
	src := "FROM ./base\nSET model opus\nSET reviewer-level strict\nUNSET language\n"
	files := map[string]string{"base/SKILL.md": fussySkill}

	once, _ := build(t, src, files)
	first, _ := once.Get("SKILL.md")

	twice, _ := build(t, src+"SET model opus\nSET reviewer-level strict\n", files)
	second, _ := twice.Get("SKILL.md")

	assert.Equal(t, string(first), string(second))
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

// SPEC.md 8.4's worked example writes `FROM base` after `FROM … AS base`, so a
// bare name a previous FROM bound is that stage, not a directory in the
// context.
func TestFromResolvesAPreviouslyDeclaredStage(t *testing.T) {
	tree, report := build(t, `FROM ./pdf AS pdf
FROM pdf
APPEND SKILL.md <<EOF
built from the stage
EOF
`, map[string]string{
		"pdf/SKILL.md":        baseSkill,
		"pdf/sections/pdf.md": "pdf notes\n",
	})

	assert.Equal(t, []string{"SKILL.md", "sections/pdf.md"}, tree.Paths(),
		"the stage's whole tree is the base")
	body, _ := tree.Get("SKILL.md")
	assert.Contains(t, string(body), "built from the stage")
	assert.Equal(t, "pdf", report.Base.Ref)
	assert.Empty(t, report.Base.Digest, "8.3 gives a stage no pin of its own")
}

// A stage name is checked before the filesystem, the way Docker checks it, so a
// directory that happens to share the name cannot shadow the stage the
// Skillfile plainly meant.
func TestFromPrefersAStageOverASameNamedDirectory(t *testing.T) {
	tree, _ := build(t, "FROM ./base AS base\nFROM base\n", map[string]string{
		"base/SKILL.md":  baseSkill,
		"base/stage.md":  "from the stage\n",
		"./base/only.md": "also the stage\n",
	})

	assert.Equal(t, []string{"SKILL.md", "only.md", "stage.md"}, tree.Paths())
}

// The stage is a *base*: the build that descends from it mutates a copy, so a
// COPY --from naming the same stage afterwards still sees what the stage was
// declared as. Sharing the tree would let a derived stage edit its own
// ancestor retroactively, which no later instruction could detect.
func TestFromAStageCannotMutateTheStage(t *testing.T) {
	tree, _ := build(t, `FROM ./pdf AS pdf
FROM pdf
RM sections/pdf.md
APPEND notes.md <<EOF
derived
EOF
COPY --from=pdf sections/pdf.md sections/pdf.md
COPY --from=pdf notes.md original-notes.md
`, map[string]string{
		"pdf/SKILL.md":        baseSkill,
		"pdf/notes.md":        "the stage's own notes\n",
		"pdf/sections/pdf.md": "pdf notes\n",
	})

	restored, ok := tree.Get("sections/pdf.md")
	require.True(t, ok, "the RM must not have reached the stage")
	assert.Equal(t, "pdf notes\n", string(restored))

	original, ok := tree.Get("original-notes.md")
	require.True(t, ok)
	assert.Equal(t, "the stage's own notes\n", string(original),
		"the APPEND on the derived tree must not have reached back into the stage")

	derived, _ := tree.Get("notes.md")
	assert.Equal(t, "the stage's own notes\nderived\n", string(derived))
}

// `FROM <stage> AS <stage>` is two copies, not two names for one tree: the
// derived stage's instructions make the derived stage, and reach nothing in
// the stage it was taken from.
func TestAStageDerivedFromAStageIsItsOwnTree(t *testing.T) {
	tree, _ := build(t, `FROM ./pdf AS pdf
FROM pdf AS derived
APPEND notes.md <<EOF
derived
EOF
FROM ./pdf
COPY --from=pdf notes.md from-pdf.md
COPY --from=derived notes.md from-derived.md
`, map[string]string{
		"pdf/SKILL.md": baseSkill,
		"pdf/notes.md": "base notes\n",
	})

	fromPDF, ok := tree.Get("from-pdf.md")
	require.True(t, ok)
	assert.Equal(t, "base notes\n", string(fromPDF),
		"the APPEND must not have reached the stage it was derived from")

	fromDerived, ok := tree.Get("from-derived.md")
	require.True(t, ok)
	assert.Equal(t, "base notes\nderived\n", string(fromDerived),
		"and the stage the same FROM declared is what its own instructions made of it")
}

// 8.4 follows Docker semantics, and Docker's COPY --from reads the named
// stage's *final* filesystem. A stage recorded at its FROM line would put its
// own edits out of reach, leaving it able to serve as nobody's source but its
// own — which is the one composition 8.4 exists to express.
func TestCopyFromAStageSeesTheEditsItMadeAfterItsFrom(t *testing.T) {
	tree, _ := build(t, `FROM ./pdf AS pdf
SET model opus
APPEND notes.md <<EOF
added by the stage
EOF
COPY house.md house.md
FROM ./base
COPY --from=pdf SKILL.md pdf/SKILL.md
COPY --from=pdf notes.md pdf/notes.md
COPY --from=pdf house.md pdf/house.md
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"pdf/SKILL.md":  baseSkill,
		"pdf/notes.md":  "stage notes\n",
		"house.md":      "house rules\n",
	})

	skill, ok := tree.Get("pdf/SKILL.md")
	require.True(t, ok)
	assert.Equal(t, "opus", frontmatterOf(t, skill)["model"], "the stage's SET is part of the stage")

	notes, ok := tree.Get("pdf/notes.md")
	require.True(t, ok)
	assert.Equal(t, "stage notes\nadded by the stage\n", string(notes), "so is its APPEND")

	house, ok := tree.Get("pdf/house.md")
	require.True(t, ok)
	assert.Equal(t, "house rules\n", string(house), "and a file it copied in from the context")
}

// The other direction of the same rule: what a stage removed is gone from what
// a later COPY --from can take.
func TestCopyFromAStageCannotTakeAFileTheStageRemoved(t *testing.T) {
	_, err := failedBuild(t, `FROM ./pdf AS pdf
RM stale.md
FROM ./base
COPY --from=pdf stale.md stale.md
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"pdf/SKILL.md":  baseSkill,
		"pdf/stale.md":  "gone\n",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file in the source")
}

// A stage is readable from the moment its instructions are over, and every
// earlier one stays readable: sealing one stage must not lose the ones before
// it.
func TestCopyFromReadsEveryStageThatHasFinished(t *testing.T) {
	tree, _ := build(t, `FROM ./one AS one
APPEND note.md <<EOF
one
EOF
FROM ./two AS two
APPEND note.md <<EOF
two
EOF
FROM ./base
COPY --from=one note.md from-one.md
COPY --from=two note.md from-two.md
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"one/SKILL.md":  baseSkill,
		"one/note.md":   "start\n",
		"two/SKILL.md":  baseSkill,
		"two/note.md":   "start\n",
	})

	one, ok := tree.Get("from-one.md")
	require.True(t, ok)
	assert.Equal(t, "start\none\n", string(one))

	two, ok := tree.Get("from-two.md")
	require.True(t, ok)
	assert.Equal(t, "start\ntwo\n", string(two))
}

// Order matters: a stage exists only once its own FROM has run, so a forward
// reference says so rather than silently looking for a directory of that name.
func TestFromAStageDeclaredLaterFails(t *testing.T) {
	sf, err := Parse([]byte("FROM base\nFROM ./base AS base\n"))
	require.NoError(t, err)

	_, _, err = Build(sf, buildContext(t, map[string]string{"base/SKILL.md": baseSkill}), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `stage "base" is declared later in the Skillfile`)
}

// The same for a COPY --from, which now reads finished stages: a name whose
// FROM is still ahead has to say so, not report a missing file or a typo.
func TestCopyFromAStageDeclaredLaterFails(t *testing.T) {
	_, err := failedBuild(t, "FROM ./base\nCOPY --from=later x.md .\nFROM ./base AS later\n",
		map[string]string{"base/SKILL.md": baseSkill, "base/x.md": "x\n"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `stage "later" is declared later in the Skillfile`)
}

// A stage cannot copy from itself: its contents are not settled until its
// instructions are over, and the instruction doing the copying is one of them.
func TestCopyFromTheStageBeingBuiltFails(t *testing.T) {
	_, err := failedBuild(t, "FROM ./base AS self\nCOPY --from=self SKILL.md copy.md\n",
		map[string]string{"base/SKILL.md": baseSkill})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `stage "self" is the stage being built`)
}

// 2.3 and 8.4: the artifact is the *last* stage. An earlier stage is a source
// a COPY --from names, however much its own instructions edited it, and naming
// one of them as the base would misreport the lineage.
func TestTheLastStageIsWhatGetsBuilt(t *testing.T) {
	tree, report := build(t, `FROM ./pdf AS pdf
SET name pdf-stage
APPEND only-in-pdf.md <<EOF
stage
EOF
FROM ./base
SET name reviewer
`, map[string]string{
		"base/SKILL.md": baseSkill,
		"pdf/SKILL.md":  baseSkill,
	})

	assert.Equal(t, []string{"SKILL.md"}, tree.Paths(),
		"the finished stage's own files are not merged into the result")

	body, _ := tree.Get("SKILL.md")
	assert.Equal(t, "reviewer", frontmatterOf(t, body)["name"],
		"and the name the artifact is tagged by comes from the last stage")
	assert.Equal(t, "./base", report.Base.Ref)
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
	assert.Equal(t, "Rust", frontmatterOf(t, body)["language"])
}

func TestArgDefaultAndOverride(t *testing.T) {
	src := "ARG lang=Python\nFROM ./base\nSET language $lang\n"
	files := map[string]string{"base/SKILL.md": baseSkill}

	sf, err := Parse([]byte(src))
	require.NoError(t, err)

	tree, _, err := Build(sf, buildContext(t, files), nil)
	require.NoError(t, err)
	body, _ := tree.Get("SKILL.md")
	assert.Equal(t, "Python", frontmatterOf(t, body)["language"], "the ARG default applies")

	sf2, _ := Parse([]byte(src))
	tree2, _, err := Build(sf2, buildContext(t, files), map[string]string{"lang": "Go"})
	require.NoError(t, err)
	body2, _ := tree2.Get("SKILL.md")
	assert.Equal(t, "Go", frontmatterOf(t, body2)["language"], "--build-arg beats the default")
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
