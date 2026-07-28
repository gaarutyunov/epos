// Command docsgen writes the Skillfile reference page of the documentation
// site from the instruction table in internal/skillfile.
//
// SPEC.md 14.1 requires the reference pages to be generated from the same
// source as the implementation, "so they cannot drift". A hand-written page
// that happens to be accurate today does not satisfy that; what does is this
// program plus the CI step that runs it and fails when the committed output
// differs.
//
//	go run ./internal/docsgen            # rewrite the page
//	go run ./internal/docsgen -check     # fail if it is out of date
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// defaultOut is where the page lives, relative to the repository root.
const defaultOut = "docs/src/pages/skillfile.astro"

func main() {
	out := flag.String("out", defaultOut, "path of the page to write")
	check := flag.Bool("check", false, "do not write; exit non-zero if the file on disk is out of date")
	flag.Parse()

	if err := run(*out, *check); err != nil {
		fmt.Fprintln(os.Stderr, "docsgen:", err)
		os.Exit(1)
	}
}

// run renders the page and either writes it or compares it.
func run(out string, check bool) error {
	want := render()

	if check {
		got, err := os.ReadFile(out)
		if err != nil {
			return err
		}
		// Compared after normalising line endings, because a Windows checkout
		// can hold the committed file with CRLF while the generator only ever
		// emits LF. Normalising here keeps the check honest about content and
		// silent about how git wrote the file out.
		if normalise(got) != normalise(want) {
			return fmt.Errorf(
				"%s is out of date; run `go run ./internal/docsgen` and commit the result", out)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	return os.WriteFile(out, want, 0o644)
}

// normalise makes a comparison independent of the host's line endings.
func normalise(b []byte) string {
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}
