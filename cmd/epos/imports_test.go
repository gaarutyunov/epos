package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The mechanical form of the owner's review comment: *"Catalog is not part of
// CLI it's part of the registry. Like zot has a UI. People downloading CLI
// don't need ui artifacts."*
//
// Without this assertion that decision lasts exactly until the first convenient
// import — someone adds `epos catalog serve` "for local browsing", the CLI
// links internal/catalog, and 104 KB of vendored JavaScript, a stylesheet and
// four template trees go back into the binary a user installs to pack and push
// a skill. The failure would be invisible in review and visible only in a
// release artifact's size.
//
// `go list -deps` is the whole mechanism. Its answer is the link graph, which
// is the thing being asserted, rather than a grep over import lines.
func TestTheCLILinksNoneOfTheCatalog(t *testing.T) {
	deps := transitiveImports(t, "./")

	assert.NotContains(t, deps, "github.com/gaarutyunov/epos/internal/catalog",
		"the CLI must not link the catalog: it carries the embedded ui-kit bundle, "+
			"the templates and the stylesheet")
	assert.NotContains(t, deps, "github.com/yuin/goldmark",
		"nor the Markdown renderer, which exists for the catalog's detail page")
}

// transitiveImports is every package the named main package links.
func transitiveImports(t *testing.T, pkg string) []string {
	t.Helper()

	out, err := exec.Command("go", "list", "-deps", pkg).CombinedOutput()
	require.NoError(t, err, "go list -deps %s: %s", pkg, out)
	return strings.Fields(string(out))
}
