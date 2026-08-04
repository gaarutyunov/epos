package skillfile

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oras.land/oras-go/v2/registry"

	eposregistry "github.com/gaarutyunov/epos/internal/registry"
)

// ociTimeout bounds one OCI FROM's network work. Same reasoning as gitTimeout:
// a build must not hang forever on somebody else's server.
const ociTimeout = 5 * time.Minute

// OCIBase is a resolved OCI base: what 8.3 calls the pin.
//
// A tag is mutable — `1.2.0` can be re-pushed over entirely different content
// — so recording the tag is not a pin at all. The manifest digest names the
// bytes and nothing else, which is why 8.3 makes it this scheme's pin and 2.3
// records it as the base digest.
type OCIBase struct {
	// Ref is the reference exactly as the Skillfile wrote it, after ARG
	// expansion.
	Ref string
	// Registry, Repository and Reference are the parsed reference; Reference is
	// the tag or the digest, as written.
	Registry   string
	Repository string
	Reference  string
	// Digest is the manifest digest the reference resolved to. This is the pin:
	// it is what a rebuild is checked against, and it is what `FROM` by digest
	// would have named directly.
	Digest string
}

// Pin renders the SHA 8.3 pins an OCI base with, for the provenance annotation
// 2.3 defines as "resolved base digest".
func (o OCIBase) Pin() string { return o.Digest }

// ParseOCIRef splits an OCI FROM reference into registry, repository and
// tag-or-digest.
//
// oras-go's own parser does the splitting rather than anything hand-rolled. A
// reference can carry a port, a tag and a digest, and all three collide on the
// colon: `127.0.0.1:5000/o/agent-skills/pdf@sha256:abc…` holds three of them,
// and none of the obvious rules — first colon, last colon, last colon after the
// last slash — picks the right one in every case.
func ParseOCIRef(ref string) (registry.Reference, error) {
	parsed, err := registry.ParseReference(ref)
	if err != nil {
		return registry.Reference{}, fmt.Errorf("%s: %w", ref, err)
	}
	// 8.3 pins by manifest digest, and a repository with no tag and no digest
	// names no manifest to pin.
	if parsed.Reference == "" {
		return registry.Reference{}, fmt.Errorf("%s: an OCI source needs a tag or a digest", ref)
	}
	return parsed, nil
}

// looksLikeOCIRef reports whether ref names a registry rather than a directory
// in the build context.
//
// The test is Docker's own for telling `ubuntu` from `myregistry.io/ubuntu`:
// what precedes the first slash is a registry host only if it looks like one —
// it carries a dot or a port, or it is `localhost`. So
// `ghcr.io/o/agent-skills/pdf:1.2.0` is a registry reference and `./skills/base`
// and `bases/pdf` are directories.
//
// Deliberately not "does it parse as a reference": `skills/base` parses
// perfectly well as the repository `base` under a registry called `skills`, and
// taking that reading would put 8.3's local scheme out of reach of any path
// with a slash in it.
func looksLikeOCIRef(ref string) bool {
	// A path that says it is a path is one. `.` is also the one character a
	// host is recognised by, so these have to be answered before the host test.
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		return false
	}
	host, rest, ok := strings.Cut(ref, "/")
	if !ok || rest == "" {
		return false
	}
	return host == "localhost" || strings.ContainsAny(host, ".:")
}

// resolveOCI fetches an OCI base and records its pin on the report.
func (b *builder) resolveOCI(ref string) (*Tree, string, error) {
	parsed, err := ParseOCIRef(ref)
	if err != nil {
		return nil, "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), ociTimeout)
	defer cancel()

	tree, base, err := fetchOCIBase(ctx, parsed, b.plainHTTP)
	if err != nil {
		return nil, "", err
	}
	base.Ref = ref

	// 8.3 makes the pin the point of the OCI scheme, so it is recorded rather
	// than discarded once the bytes are in hand: the tag it was reached by can
	// be moved afterwards, and the digest is the only record of what this
	// artifact actually descended from.
	b.report.OCIBases = append(b.report.OCIBases, base)
	return tree, base.Pin(), nil
}

// fetchOCIBase resolves a reference to its manifest digest and reads the
// artifact's content layer into a tree.
//
// The fetch itself, the one-layer assertion, the 64 MiB cap and the path,
// symlink and hardlink guards live in internal/registry now: epos-registry
// needs the same routine for a catalog detail page and cannot link this package
// without linking go-git, goawk, go-gitdiff and goccy/go-yaml with it. What
// stays here is the Tree, which is the Skillfile build's own type and nothing a
// registry client should know about.
func fetchOCIBase(ctx context.Context, r registry.Reference, plainHTTP bool) (*Tree, OCIBase, error) {
	content, err := eposregistry.FetchReferenceContent(ctx, r, eposregistry.Options{
		PlainHTTP: plainHTTP,
	})
	if err != nil {
		return nil, OCIBase{}, err
	}

	tree := NewTree()
	for p, body := range content.Files {
		// Through Tree.Set, and so through checkPath, because a base pulled
		// from a registry is somebody else's input (2.5).
		if err := tree.Set(p, body); err != nil {
			return nil, OCIBase{}, fmt.Errorf("%s: %w", r, err)
		}
	}

	return tree, OCIBase{
		Registry:   r.Registry,
		Repository: r.Repository,
		Reference:  r.Reference,
		Digest:     content.Digest,
	}, nil
}
