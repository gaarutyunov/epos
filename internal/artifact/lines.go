package artifact

import "strings"

// splitLines splits src into lines and reports the newline it used, so the
// frontmatter block can be handed to the YAML parser exactly as authored. A
// CRLF file whose lines were rejoined with "\n" would parse, but any block
// scalar in it would silently change.
func splitLines(src []byte) (lines []string, newline string) {
	s := string(src)
	newline = "\n"
	if strings.Contains(s, "\r\n") {
		newline = "\r\n"
	}
	return strings.Split(s, newline), newline
}

func joinLines(lines []string, newline string) string {
	return strings.Join(lines, newline)
}

// isFence reports whether a line is a frontmatter delimiter. Trailing
// whitespace is tolerated because editors add it and the fence is still
// unambiguous.
func isFence(line string) bool {
	return strings.TrimRight(line, " \t\r") == "---"
}
