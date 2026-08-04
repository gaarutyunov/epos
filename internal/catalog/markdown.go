package catalog

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"

	"github.com/gaarutyunov/epos/internal/artifact"
)

// MaxDocument caps the SKILL.md the catalog will render.
//
// Independent of the 64 MiB layer cap, and deliberately much smaller: the
// layer cap stops a decompression bomb, this stops an enormous but perfectly
// legal document being parsed and rendered on every request. The digest-keyed
// document cache bounds how often that happens, which is mitigation rather than
// a fix. 1 MiB is two orders of magnitude above any SKILL.md that has ever been
// written and still a bound.
const MaxDocument = 1 << 20

// ErrDocumentTooLarge is what an oversized document fails with. The skill still
// lists; only its rendered body is missing (D3d).
var ErrDocumentTooLarge = errors.New("the document is too large to render")

// safeSchemes are the URL schemes a link, image or autolink in somebody else's
// document may carry.
//
// An allow-list, not a deny-list. `javascript:` and `data:text/html` are the
// two everybody thinks of; `vbscript:`, `blob:` and whatever the next browser
// ships are the reason the rule is written the other way round.
var safeSchemes = map[string]bool{
	"http":   true,
	"https":  true,
	"mailto": true,
}

// renderMarkdown turns a skill's SKILL.md into HTML for the detail page.
//
// This is the change's one genuine security boundary. Everything else the
// catalog renders is a short string from a manifest annotation; SKILL.md is an
// arbitrary document fetched from a registry, authored by whoever pushed the
// artifact, and rendered into the catalog's own origin. On a shared host that
// is stored XSS with a supply chain attached.
//
// Three things make it safe, and all three are asserted by tests over the
// rendered output rather than over the configuration, so a later change that
// re-enables passthrough fails the build:
//
//  1. Raw HTML stays off. It is goldmark's default and it is never switched on;
//     a `<script>` in the source renders as text.
//  2. Link, image and autolink destinations are constrained to http, https,
//     mailto and relative — as an AST transformer, before any HTML string
//     exists. This is the part "disable raw HTML" does not cover: a Markdown
//     link with a `javascript:` target is not raw HTML at all, and goldmark
//     will happily render it as an <a href>.
//  3. The document is size-capped independently of the layer.
//
// No output-side sanitiser. The transformer runs on the parsed tree, where the
// destination is a value rather than a substring of a string somebody has to
// re-parse; adding bluemonday behind it would be a second answer to the same
// question and would invite the first one to get sloppy. If an output-side pass
// ever proves necessary, the pull request that adds it should say what the
// transformer could not reach.
//
// No heading anchors. Rendered headings are not linkable — that is a decision,
// not an oversight: an anchor is an id derived from publisher-controlled text,
// which collides with the page's own ids.
//
// No syntax highlighting. Fenced code is <pre><code> styled from the kit's
// tokens; chroma embeds every lexer and goldmark-highlighting/v2 has never cut
// a semver tag (D7a).
func renderMarkdown(source []byte) (template.HTML, error) {
	// The frontmatter is metadata, not prose, and the config blob already
	// carries it. Rendering it would put a YAML block at the top of every page.
	body := artifact.Body(source)
	if len(body) > MaxDocument {
		return "", fmt.Errorf("%w: %d bytes, the limit is %d",
			ErrDocumentTooLarge, len(body), MaxDocument)
	}

	md := goldmark.New(
		// extension.Table only. SKILL.md documents use tables freely; the rest
		// of GFM — linkify, task lists, strikethrough — is not asked for, and
		// linkify in particular would turn bare text into more destinations to
		// constrain.
		goldmark.WithExtensions(extension.Table),
		goldmark.WithParserOptions(
			parser.WithASTTransformers(util.Prioritized(safeURLs{}, 100)),
		),
		// Deliberately no goldmark.WithRendererOptions(html.WithUnsafe()).
		// WithUnsafe is what turns raw HTML passthrough on, and its absence
		// here is load-bearing rather than an omission.
	)

	var out bytes.Buffer
	if err := md.Convert(body, &out); err != nil {
		return "", fmt.Errorf("render the document: %w", err)
	}
	return template.HTML(out.String()), nil //nolint:gosec // G203: the transformer above is why this is safe.
}

// safeURLs defangs every destination in a document before it becomes HTML.
type safeURLs struct{}

func (safeURLs) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	_ = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Link:
			if !safeDestination(node.Destination) {
				node.Destination = nil
			}
		case *ast.Image:
			if !safeDestination(node.Destination) {
				node.Destination = nil
			}
		case *ast.AutoLink:
			// An autolink renders its own text as its destination, so there is
			// no href to blank: the node is replaced by the text it showed.
			if !safeDestination(node.URL(reader.Source())) {
				replaceWithText(node, node.Label(reader.Source()))
			}
		}
		return ast.WalkContinue, nil
	})
}

// safeDestination reports whether a URL may be emitted.
//
// A destination with no scheme is relative and is allowed through, which on a
// catalog page means it resolves to nothing: a relative link points at a file
// inside the artifact — `references/style.md` — and the route table is home,
// the list, a detail page and the tools page (D2). None of them addresses a
// file out of a content layer, so the anchor renders inert and following it
// lands on a 404 within the catalog rather than anywhere else.
//
// The "resolve it to something we serve" branch is deliberately not
// implemented, and it is unreachable until such a route exists. An implementor
// should not spend an afternoon looking for the route: there isn't one.
func safeDestination(dest []byte) bool {
	url := strings.TrimSpace(string(dest))
	if url == "" {
		return false
	}
	scheme, rest, found := strings.Cut(url, ":")
	if !found {
		return true
	}
	// "./notes:draft.md" and "page.md#a:b" have a colon without a scheme
	// before it. A scheme cannot contain a slash, a question mark or a hash.
	if strings.ContainsAny(scheme, "/?#") {
		return true
	}
	// A protocol-relative "//host/path" is neither, and it inherits the
	// page's scheme, so it is safe by the same rule as a relative link.
	if scheme == "" && strings.HasPrefix(rest, "//") {
		return true
	}
	return safeSchemes[strings.ToLower(scheme)]
}

// replaceWithText swaps a node for a literal string, keeping the text a reader
// saw while removing the destination it carried.
func replaceWithText(n ast.Node, label []byte) {
	parent := n.Parent()
	if parent == nil {
		return
	}
	lit := ast.NewString(label)
	lit.SetCode(false)
	parent.ReplaceChild(parent, n, lit)
}
