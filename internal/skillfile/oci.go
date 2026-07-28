package skillfile

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	// go-digest resolves algorithms through a registry each hash populates in
	// its init, and a digest reference — `…/pdf@sha256:…` — is parsed here. A
	// binary that never imports sha256 elsewhere would reject every one of them.
	_ "crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

// ociTimeout bounds one OCI FROM's network work. Same reasoning as gitTimeout:
// a build must not hang forever on somebody else's server.
const ociTimeout = 5 * time.Minute

// maxBaseLayer caps how much a base's content layer may decompress to.
//
// A gzip stream is a few kilobytes of input away from gigabytes of output, and
// a FROM names a registry the author does not control. 64 MiB is orders of
// magnitude above any real skill — 8.1 makes a skill kilobytes of Markdown and
// YAML — and is still a bound.
const maxBaseLayer = 64 << 20

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
// Nothing is written to disk. The base is pulled into memory and unpacked
// there, for the same reason a git base is never checked out: a filesystem is
// where line-ending conversion and mode handling would creep into bytes that
// 2.4 needs to be identical on every platform.
func fetchOCIBase(ctx context.Context, r registry.Reference, plainHTTP bool) (*Tree, OCIBase, error) {
	repo, err := remote.NewRepository(r.Registry + "/" + r.Repository)
	if err != nil {
		return nil, OCIBase{}, fmt.Errorf("%s: %w", r, err)
	}
	repo.PlainHTTP = plainHTTP

	// Resolved once, and everything after this point goes by the descriptor.
	// Asking the registry for the reference a second time could land on
	// different content than the one the pin was taken from, which is exactly
	// the mutability 8.3 pins against.
	desc, err := repo.Resolve(ctx, r.Reference)
	if err != nil {
		return nil, OCIBase{}, fmt.Errorf("resolve %s: %w", r, err)
	}

	body, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		return nil, OCIBase{}, fmt.Errorf("fetch %s: %w", r, err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, OCIBase{}, fmt.Errorf("%s: parse the base manifest: %w", r, err)
	}
	// 2.1: a skill artifact has exactly one content layer. A reference that
	// resolves to something else — a container image, an index — is not a base
	// a skill can be built from, and guessing which layer was meant would be
	// worse than saying so.
	if len(manifest.Layers) != 1 {
		return nil, OCIBase{}, fmt.Errorf("%s: the base has %d layers, want exactly 1",
			r, len(manifest.Layers))
	}

	layer, err := content.FetchAll(ctx, repo, manifest.Layers[0])
	if err != nil {
		return nil, OCIBase{}, fmt.Errorf("fetch the content layer of %s: %w", r, err)
	}
	tree, err := ociTreeFiles(layer)
	if err != nil {
		return nil, OCIBase{}, fmt.Errorf("%s: %w", r, err)
	}

	return tree, OCIBase{
		Registry:   r.Registry,
		Repository: r.Repository,
		Reference:  r.Reference,
		Digest:     desc.Digest.String(),
	}, nil
}

// ociTreeFiles reads a skill artifact's content layer into a Tree.
//
// The layer is a tar+gzip rooted at `<skill-name>/` (2.1) and that root is
// stripped, because a base enters the stage as the skill it is rather than as a
// directory named after it: `FROM …/agent-skills/pdf:1.2.0` puts the base's
// SKILL.md at SKILL.md, which is what every instruction in 8.2 then addresses.
//
// 2.5's validation is deliberately permissive and stays that way here. A
// third-party base may carry `aux.md`, `notes:draft.md` or a name ending in a
// dot — all legal on Linux, all accepted by every other tool in the ecosystem,
// and none of them fixable by the consumer deriving from the base. Rejecting
// them at build would refuse skills that `oras pull` accepts. Only what 2.5
// actually rejects is rejected — `..`, absolute paths and symlinks — and every
// one of those is a write outside the skill root rather than a portability
// question.
func ociTreeFiles(layer []byte) (*Tree, error) {
	gr, err := gzip.NewReader(bytes.NewReader(layer))
	if err != nil {
		return nil, fmt.Errorf("read the content layer: %w", err)
	}
	defer func() { _ = gr.Close() }()

	out := NewTree()
	root := ""
	// Bounded on the decompressed side: the compressed layer's size is whatever
	// the descriptor claimed, and what it expands to is not.
	tr := tar.NewReader(io.LimitReader(gr, maxBaseLayer))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read the content layer: %w", err)
		}

		name := strings.TrimSuffix(h.Name, "/")
		switch h.Typeflag {
		case tar.TypeDir:
			// Directories are implied by the paths of the files in them; the
			// tree holds files only.
			continue
		case tar.TypeSymlink, tar.TypeLink:
			return nil, fmt.Errorf("%s: symlinks are not allowed in a skill", name)
		case tar.TypeReg:
		default:
			return nil, fmt.Errorf("%s: only regular files can be built from", name)
		}

		// Checked whole, before the root is stripped. Stripping first would let
		// `../etc/passwd` through: `..` would be taken for the skill root and
		// what remained of the name would look perfectly ordinary.
		if err := checkPath(name); err != nil {
			return nil, err
		}

		prefix, rest, nested := strings.Cut(name, "/")
		if !nested || rest == "" {
			return nil, fmt.Errorf("%s: the content layer is not rooted at a skill directory", name)
		}
		if root == "" {
			root = prefix
		}
		// 2.1 roots the layer at one `<skill-name>/`. Two roots would mean the
		// artifact holds two skills, and stripping either would silently merge
		// them.
		if prefix != root {
			return nil, fmt.Errorf("the content layer is rooted at both %s/ and %s/", root, prefix)
		}

		body, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		// Through Tree.Set, and so through checkPath, because a base pulled
		// from a registry is somebody else's input (2.5).
		if err := out.Set(rest, body); err != nil {
			return nil, err
		}
	}
	if root == "" {
		return nil, fmt.Errorf("the content layer holds no files")
	}
	return out, nil
}
