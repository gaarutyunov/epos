package skillfile

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseInstructionsInFileOrder(t *testing.T) {
	sf, err := Parse([]byte(`# derive a reviewer
ARG model=sonnet
FROM ./skills/base AS base
RM sections/house-style.md
SET language "Go"
`))
	require.NoError(t, err)

	var ops []string
	for _, i := range sf.Instructions {
		ops = append(ops, i.Op)
	}
	// 8.2: instructions apply in file order and the later of two touching the
	// same bytes wins, so order is the semantics.
	assert.Equal(t, []string{"ARG", "FROM", "RM", "SET"}, ops)
}

func TestParseFlags(t *testing.T) {
	sf, err := Parse([]byte(`COPY --from=pdf sections/x.md sections/
REPLACE --count=2 SKILL.md "foo" "bar"
SET --file=values.yaml model sonnet
`))
	require.NoError(t, err)
	require.Len(t, sf.Instructions, 3)

	assert.Equal(t, "pdf", sf.Instructions[0].Flags["from"])
	assert.Equal(t, []string{"sections/x.md", "sections/"}, sf.Instructions[0].Args)

	assert.Equal(t, "2", sf.Instructions[1].Flags["count"])
	assert.Equal(t, []string{"SKILL.md", "foo", "bar"}, sf.Instructions[1].Args)

	assert.Equal(t, "values.yaml", sf.Instructions[2].Flags["file"])
	assert.Equal(t, []string{"model", "sonnet"}, sf.Instructions[2].Args)
}

// A bare --flag would have to consume the next token, making it ambiguous with
// a positional argument. Saying so beats guessing.
func TestBareFlagIsRejected(t *testing.T) {
	_, err := Parse([]byte("SET --file values.yaml model sonnet\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--file=<value>")
}

// SPEC.md 8.6: a payload containing {{ }} must survive the build untouched.
func TestHeredocIsVerbatim(t *testing.T) {
	sf, err := Parse([]byte(`APPEND sections/checklist.md <<EOF
- table-driven tests
- {{ .Values.model }} stays untouched
  # not a comment, it is content
EOF
RM stale.md
`))
	require.NoError(t, err)
	require.Len(t, sf.Instructions, 2)

	appended := sf.Instructions[0]
	assert.Equal(t, "APPEND", appended.Op)
	assert.Equal(t, []string{"sections/checklist.md"}, appended.Args)
	assert.Equal(t,
		"- table-driven tests\n- {{ .Values.model }} stays untouched\n  # not a comment, it is content",
		appended.Heredoc)

	// Parsing resumes after the terminator.
	assert.Equal(t, "RM", sf.Instructions[1].Op)
}

// An AWK script is not Skillfile syntax: braces, $0 and a trailing backslash
// must all reach goawk unchanged.
func TestHeredocDoesNotInterpretItsBody(t *testing.T) {
	sf, err := Parse([]byte(`AWK SKILL.md <<AWKEOF
{ print toupper($0) }
/ends with a backslash/ { print "x" \
}
AWKEOF
`))
	require.NoError(t, err)
	require.Len(t, sf.Instructions, 1)
	assert.Equal(t,
		"{ print toupper($0) }\n/ends with a backslash/ { print \"x\" \\\n}",
		sf.Instructions[0].Heredoc)
}

func TestUnterminatedHeredoc(t *testing.T) {
	_, err := Parse([]byte("APPEND notes.md <<EOF\nbody\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "never closed")
}

func TestLineContinuations(t *testing.T) {
	sf, err := Parse([]byte(`COPY --from=base \
    sections/a.md \
    sections/
`))
	require.NoError(t, err)
	require.Len(t, sf.Instructions, 1)
	assert.Equal(t, []string{"sections/a.md", "sections/"}, sf.Instructions[0].Args)
	assert.Equal(t, "base", sf.Instructions[0].Flags["from"])
}

// A # inside a git ref or a regex is content, not a comment.
func TestOnlyLeadingHashIsAComment(t *testing.T) {
	sf, err := Parse([]byte(`# a real comment
   # an indented comment
FROM git+https://github.com/o/r#v1.2.0:skills/pdf AS pdf
REPLACE SKILL.md "#heading" "## heading"
`))
	require.NoError(t, err)
	require.Len(t, sf.Instructions, 2)

	assert.Equal(t, []string{"git+https://github.com/o/r#v1.2.0:skills/pdf", "AS", "pdf"},
		sf.Instructions[0].Args)
	assert.Equal(t, []string{"SKILL.md", "#heading", "## heading"},
		sf.Instructions[1].Args)
}

func TestQuoting(t *testing.T) {
	sf, err := Parse([]byte(`REPLACE SKILL.md "two words" 'single quoted'
SET description ""
`))
	require.NoError(t, err)

	assert.Equal(t, []string{"SKILL.md", "two words", "single quoted"}, sf.Instructions[0].Args)
	// An empty string is an argument, not an absence.
	assert.Equal(t, []string{"description", ""}, sf.Instructions[1].Args)
}

// 8.2.4 needs to know which values were quoted, so the tokenizer records it
// alongside every argument — for every instruction, since the tokenizer serves
// them all. Quoted stays parallel to Args: a flag consumes neither, and a
// heredoc marker leaves both.
func TestQuotingIsRecordedAlongsideEveryArgument(t *testing.T) {
	sf, err := Parse([]byte(`COPY --from=pdf "sections/a b.md" docs/
REPLACE SKILL.md "two words" 'single quoted'
SET version "1.2"
SET version 1.2
APPEND "notes.md" <<EOF
body
EOF
`))
	require.NoError(t, err)
	require.Len(t, sf.Instructions, 5)

	assert.Equal(t, []string{"sections/a b.md", "docs/"}, sf.Instructions[0].Args)
	assert.Equal(t, []bool{true, false}, sf.Instructions[0].Quoted,
		"the --from flag is not an argument, so it shifts nothing")

	assert.Equal(t, []bool{false, true, true}, sf.Instructions[1].Quoted,
		"single quotes are quotes too")

	assert.Equal(t, []string{"version", "1.2"}, sf.Instructions[2].Args)
	assert.Equal(t, []bool{false, true}, sf.Instructions[2].Quoted)
	assert.Equal(t, sf.Instructions[2].Args, sf.Instructions[3].Args,
		"quoted or not, the argument itself is the same text")
	assert.Equal(t, []bool{false, false}, sf.Instructions[3].Quoted)

	assert.Equal(t, []string{"notes.md"}, sf.Instructions[4].Args)
	assert.Equal(t, []bool{true}, sf.Instructions[4].Quoted,
		"the heredoc marker left Args, so it left Quoted with it")
}

// The record is SET's alone (8.2.4). Every other instruction sees exactly what
// it saw before: a quoted path is the same path.
func TestQuotingDoesNotChangeWhatOtherInstructionsReceive(t *testing.T) {
	quoted, err := Parse([]byte(`ARG "language=Go"
FROM "./base" AS "base"
COPY "notes.md" "references/notes.md"
RM "stale.md"
REPLACE --count="1" "SKILL.md" "model: sonnet" "model: opus"
PATCH "docs/guide.md" "patches/guide.diff"
AWK "sections/list.md" "scripts/upper.awk"
`))
	require.NoError(t, err)

	bare, err := Parse([]byte(`ARG language=Go
FROM ./base AS base
COPY notes.md references/notes.md
RM stale.md
REPLACE --count=1 SKILL.md "model: sonnet" "model: opus"
PATCH docs/guide.md patches/guide.diff
AWK sections/list.md scripts/upper.awk
`))
	require.NoError(t, err)

	require.Len(t, quoted.Instructions, len(bare.Instructions))
	for i, inst := range quoted.Instructions {
		assert.Equal(t, bare.Instructions[i].Op, inst.Op)
		assert.Equal(t, bare.Instructions[i].Args, inst.Args,
			"line %d: quoting must not change the arguments an instruction gets", inst.Line)
		assert.Equal(t, bare.Instructions[i].Flags, inst.Flags,
			"line %d: nor its flags", inst.Line)
	}
}

func TestUnterminatedQuote(t *testing.T) {
	_, err := Parse([]byte(`REPLACE SKILL.md "unclosed bar` + "\n"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated")
}

func TestBlankAndCommentOnlyFilesParse(t *testing.T) {
	sf, err := Parse([]byte("\n\n# nothing to do\n\n"))
	require.NoError(t, err)
	assert.Empty(t, sf.Instructions)
}

func TestCRLF(t *testing.T) {
	sf, err := Parse([]byte("FROM ./base AS base\r\nRM stale.md\r\n"))
	require.NoError(t, err)
	require.Len(t, sf.Instructions, 2)
	assert.Equal(t, []string{"./base", "AS", "base"}, sf.Instructions[0].Args)
}

func TestLineNumbersAreReported(t *testing.T) {
	sf, err := Parse([]byte("# comment\n\nFROM ./base\n\nRM stale.md\n"))
	require.NoError(t, err)
	require.Len(t, sf.Instructions, 2)
	assert.Equal(t, 3, sf.Instructions[0].Line)
	assert.Equal(t, 5, sf.Instructions[1].Line)
}
