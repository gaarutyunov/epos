// Command docsgen writes the generated reference pages of the documentation
// site: the CLI reference from the cobra command tree in internal/cli, and the
// Skillfile reference from the instruction table in internal/skillfile.
//
// SPEC.md 14.1 requires the reference pages to be generated from the same
// source as the implementation, "so they cannot drift". A hand-written page
// that happens to be accurate today does not satisfy that; what does is this
// program plus the CI step that runs it and fails when a committed page
// differs.
//
//	go run ./internal/docsgen            # rewrite the pages
//	go run ./internal/docsgen -check     # fail if one of them is out of date
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// target is one generated page.
type target struct {
	// path is where the page lives, in slash form, relative to the repository
	// root.
	path string
	// render builds the whole file.
	render func() []byte
}

// targets is every page docsgen owns.
//
// Both pages are written by one invocation and checked by one CI step. A
// second generator with a drift check of its own is how two pages start
// disagreeing about what "generated" means, and how one of them quietly stops
// being checked at all.
func targets() []target {
	return []target{
		{path: "docs/src/pages/cli.astro", render: renderCLI},
		{path: "docs/src/pages/skillfile.astro", render: renderSkillfile},
	}
}

func main() {
	root := flag.String("root", ".", "repository root the pages are written under")
	check := flag.Bool("check", false, "do not write; exit non-zero if a page on disk is out of date")
	flag.Parse()

	if err := run(*root, *check); err != nil {
		fmt.Fprintln(os.Stderr, "docsgen:", err)
		os.Exit(1)
	}
}

// run renders every page and either writes it or compares it.
func run(root string, check bool) error {
	for _, t := range targets() {
		if err := t.apply(root, check); err != nil {
			return err
		}
	}
	return nil
}

// apply writes one page, or reports that the committed one is stale.
func (t target) apply(root string, check bool) error {
	out := filepath.Join(root, filepath.FromSlash(t.path))
	want := t.render()

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
				"%s is out of date; run `go run ./internal/docsgen` and commit the result", t.path)
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
