// Package skillfile parses and applies a Skillfile (SPEC.md 8).
//
// A build is a pure function from (bases, Skillfile, context) to one
// conformant single-layer artifact. There is no RUN, no ENTRYPOINT, no CMD;
// nothing executes. Removing execution removes the sandbox problem and keeps
// the build pure Go, cgo-free, daemon-free and cross-platform.
//
// The parser is written here rather than taken from BuildKit, which 8.5
// names. BuildKit's dockerfile parser only understands its own instruction
// set: for Epos's instructions it drops arguments (8.5 says as much), mangles
// `--flag value` into a bare flag, and — the part 8.5 does not anticipate —
// does not collect heredocs, so an `APPEND … <<EOF` body arrives as a run of
// bogus top-level nodes. What survives is line continuations and comment
// attachment, which are a few lines each. See the note on the B1 issue.
package skillfile

import (
	"fmt"
	"strings"
)

// Instruction is one Skillfile line, with its payload already attached.
type Instruction struct {
	// Op is the instruction name, upper-cased: FROM, COPY, RM, …
	Op string
	// Args are the positional arguments, quotes resolved.
	Args []string
	// Quoted says, for each argument in Args, whether the Skillfile wrote any
	// part of it inside quotes. Parallel to Args, and read through quoted.
	//
	// Resolving the quotes is what makes `REPLACE SKILL.md "two words" x` work,
	// but it also throws away the one thing 8.2.4 needs: `SET version "1.2"`
	// and `SET version 1.2` arrive as the same three characters, and 8.2.4 says
	// the first is a string and the second a float. Keeping the fact of the
	// quoting alongside the argument is what lets SET tell them apart without
	// changing what any other instruction receives — to COPY or RM a quoted
	// path is still just a path.
	Quoted []bool
	// Flags are the --name=value options, keyed by name without the dashes.
	Flags map[string]string
	// Heredoc is the payload of an `<<EOF … EOF` form, empty otherwise. It is
	// kept verbatim: 8.6 requires a payload containing {{ }} to survive the
	// build untouched.
	Heredoc string
	// Line is the 1-based line the instruction started on, for diagnostics.
	Line int
}

// quoted reports whether the i-th argument was written quoted.
//
// Tolerant of a short Quoted slice so an Instruction assembled by hand — a
// test, or a caller building one instruction — behaves as if nothing was
// quoted rather than panicking.
func (inst Instruction) quoted(i int) bool {
	return i < len(inst.Quoted) && inst.Quoted[i]
}

// Skillfile is a parsed build recipe: instructions in file order.
//
// Order is the whole semantics — 8.2 says instructions apply in file order and
// the later of two touching the same bytes wins — so nothing here sorts,
// groups or deduplicates.
type Skillfile struct {
	Instructions []Instruction
}

// Parse reads a Skillfile.
func Parse(src []byte) (*Skillfile, error) {
	lines := splitLines(string(src))
	var out Skillfile

	for i := 0; i < len(lines); {
		raw, next := joinContinuations(lines, i)
		lineNo := i + 1
		i = next

		text := strings.TrimSpace(stripComment(raw))
		if text == "" {
			continue
		}

		inst, err := parseInstruction(text, lineNo)
		if err != nil {
			return nil, err
		}

		// A heredoc marker consumes the following lines up to its terminator.
		if marker, rest, ok := cutHeredoc(inst.Args); ok {
			body, after, err := readHeredoc(lines, i, marker, lineNo)
			if err != nil {
				return nil, err
			}
			// Quoted is parallel to Args, so dropping the marker from one drops
			// it from the other.
			inst.Args, inst.Quoted, inst.Heredoc, i = rest, inst.Quoted[:len(rest)], body, after
		}

		out.Instructions = append(out.Instructions, inst)
	}

	return &out, nil
}

// parseInstruction splits one logical line into op, flags and arguments.
func parseInstruction(text string, line int) (Instruction, error) {
	fields, err := tokenize(text)
	if err != nil {
		return Instruction{}, fmt.Errorf("line %d: %w", line, err)
	}

	inst := Instruction{Op: strings.ToUpper(fields[0].text), Line: line, Flags: map[string]string{}}
	for _, f := range fields[1:] {
		// Only --name=value is a flag. A bare `--name` would have to consume
		// the next token, which makes `SET --file values.yaml model x`
		// ambiguous with a positional argument — 8.2's flags all take values,
		// so requiring `=` keeps the grammar unambiguous.
		if name, value, ok := strings.Cut(f.text, "="); ok && strings.HasPrefix(name, "--") {
			inst.Flags[strings.TrimPrefix(name, "--")] = value
			continue
		}
		if strings.HasPrefix(f.text, "--") {
			return Instruction{}, fmt.Errorf(
				"line %d: flag %s needs a value, written --%s=<value>",
				line, f.text, strings.TrimPrefix(f.text, "--"))
		}
		// Appended together, which is what keeps the two slices parallel: a
		// flag consumes neither.
		inst.Args = append(inst.Args, f.text)
		inst.Quoted = append(inst.Quoted, f.quoted)
	}

	if inst.Op == "" {
		return Instruction{}, fmt.Errorf("line %d: no instruction", line)
	}
	return inst, nil
}

// cutHeredoc reports whether the last argument opens a heredoc, returning the
// terminator and the arguments without it.
func cutHeredoc(args []string) (marker string, rest []string, ok bool) {
	if len(args) == 0 {
		return "", args, false
	}
	last := args[len(args)-1]
	if !strings.HasPrefix(last, "<<") {
		return "", args, false
	}
	return strings.TrimPrefix(last, "<<"), args[:len(args)-1], true
}

// readHeredoc collects lines up to the terminator, verbatim.
func readHeredoc(lines []string, from int, marker string, opened int) (string, int, error) {
	var body []string
	for i := from; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t\r") == marker {
			return strings.Join(body, "\n"), i + 1, nil
		}
		// Verbatim: no comment stripping, no continuation joining, no
		// expansion. 8.6 requires {{ }} to survive, and an AWK script or a
		// patch is not Skillfile syntax.
		body = append(body, lines[i])
	}
	return "", 0, fmt.Errorf("line %d: heredoc %s is never closed", opened, marker)
}

// joinContinuations returns the logical line starting at i, and the index
// after it.
func joinContinuations(lines []string, i int) (string, int) {
	var b strings.Builder
	for ; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if strings.HasSuffix(line, `\`) {
			b.WriteString(strings.TrimSuffix(line, `\`))
			continue
		}
		b.WriteString(line)
		return b.String(), i + 1
	}
	return b.String(), i
}

// stripComment removes a trailing comment.
//
// Only a # that starts a line (after whitespace) is a comment: a # inside a
// path, a regex or a git ref — `git+https://host/o/r#v1.2.0` — is not.
func stripComment(line string) string {
	if strings.HasPrefix(strings.TrimSpace(line), "#") {
		return ""
	}
	return line
}

func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Split(s, "\n")
}

// token is one field of an instruction line: the text with its quotes resolved,
// and whether it had any.
//
// The quoting is remembered rather than merely consumed because 8.2.4 gives it
// meaning that outlives the split — see Instruction.Quoted. Partial quoting
// counts: `SET v 1."2"` is quoted, on the grounds that an author who reached
// for quotes anywhere in a value meant them to do something.
type token struct {
	text   string
	quoted bool
}

// tokenize splits on whitespace, honouring double and single quotes so a
// REPLACE pattern can contain spaces.
func tokenize(text string) ([]token, error) {
	var (
		fields []token
		cur    strings.Builder
		quote  rune
		had    bool
	)

	flush := func() {
		if had || cur.Len() > 0 {
			fields = append(fields, token{text: cur.String(), quoted: had})
			cur.Reset()
			had = false
		}
	}

	for _, r := range text {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				had = true // "" is an argument, not nothing
				continue
			}
			cur.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote", quote)
	}
	flush()

	if len(fields) == 0 {
		return nil, fmt.Errorf("no instruction")
	}
	return fields, nil
}
