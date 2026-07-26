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
	// Flags are the --name=value options, keyed by name without the dashes.
	Flags map[string]string
	// Heredoc is the payload of an `<<EOF … EOF` form, empty otherwise. It is
	// kept verbatim: 8.6 requires a payload containing {{ }} to survive the
	// build untouched.
	Heredoc string
	// Line is the 1-based line the instruction started on, for diagnostics.
	Line int
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
			inst.Args, inst.Heredoc, i = rest, body, after
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

	inst := Instruction{Op: strings.ToUpper(fields[0]), Line: line, Flags: map[string]string{}}
	for _, f := range fields[1:] {
		// Only --name=value is a flag. A bare `--name` would have to consume
		// the next token, which makes `SET --file values.yaml model x`
		// ambiguous with a positional argument — 8.2's flags all take values,
		// so requiring `=` keeps the grammar unambiguous.
		if name, value, ok := strings.Cut(f, "="); ok && strings.HasPrefix(name, "--") {
			inst.Flags[strings.TrimPrefix(name, "--")] = value
			continue
		}
		if strings.HasPrefix(f, "--") {
			return Instruction{}, fmt.Errorf(
				"line %d: flag %s needs a value, written --%s=<value>",
				line, f, strings.TrimPrefix(f, "--"))
		}
		inst.Args = append(inst.Args, f)
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

// tokenize splits on whitespace, honouring double and single quotes so a
// REPLACE pattern can contain spaces.
func tokenize(text string) ([]string, error) {
	var (
		fields []string
		cur    strings.Builder
		quote  rune
		had    bool
	)

	flush := func() {
		if had || cur.Len() > 0 {
			fields = append(fields, cur.String())
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
