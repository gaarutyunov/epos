package registry

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	// go-digest resolves algorithms through a registry each hash populates in
	// its init, and a digest reference — `…/pdf@sha256:…` — is parsed here. A
	// binary that never imports sha256 elsewhere would reject every one of them.
	_ "crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"oras.land/oras-go/v2/content"
	orasregistry "oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
)

// MaxContentLayer caps how much a content layer may decompress to.
//
// A gzip stream is a few kilobytes of input away from gigabytes of output, and
// the artifacts this reads are somebody else's: a Skillfile FROM names a
// registry the author does not control, and a catalog points at arbitrary
// registries by definition. 64 MiB is orders of magnitude above any real skill
// — 8.1 makes a skill kilobytes of Markdown and YAML — and is still a bound.
const MaxContentLayer = 64 << 20

// Content is a skill artifact's content layer, unpacked in memory.
type Content struct {
	// Digest is the manifest digest the reference resolved to. This is the pin
	// SPEC.md 8.3 records: a tag is mutable, and the digest names the bytes.
	Digest string
	// Root is the `<skill-name>/` directory the layer was rooted at (2.1),
	// stripped from every path in Files.
	Root string
	// Files is the layer's regular files, keyed by slash-separated path
	// relative to Root.
	Files map[string][]byte
}

// FetchReferenceContent resolves a reference and reads its content layer.
//
// Nothing is written to disk. The artifact is pulled into memory and unpacked
// there, for the same reason a git base is never checked out: a filesystem is
// where line-ending conversion and mode handling would creep into bytes that
// SPEC.md 2.4 needs to be identical on every platform.
//
// This is the routine internal/skillfile used to hold as fetchOCIBase. It moved
// here rather than being exported where it stood because epos-registry needs it
// for a catalog detail page and must not link internal/skillfile, which carries
// go-git, goawk, go-gitdiff and goccy/go-yaml. Moving it, rather than copying
// it, is the whole point: a second copy is how one of them loses a guard.
//
// It also gained a credential. As fetchOCIBase it built its repository with no
// client at all, so it was anonymous everywhere — which for a catalog would
// have meant list pages rendering against an authenticated registry and every
// detail page 401ing. Options.Client is now threaded through, which
// incidentally gives a Skillfile FROM an auth path it did not have.
func FetchReferenceContent(ctx context.Context, r orasregistry.Reference, opts Options) (Content, error) {
	repo, err := remote.NewRepository(r.Registry + "/" + r.Repository)
	if err != nil {
		return Content{}, fmt.Errorf("%s: %w", r, err)
	}
	repo.PlainHTTP = opts.PlainHTTP
	if opts.Client != nil {
		repo.Client = opts.Client
	}

	c, err := fetchContent(ctx, repo, r.String(), r.Reference)
	if err != nil {
		return Content{}, err
	}
	return c, nil
}

// fetchContent reads one artifact's content layer through an open repository.
//
// name is what errors call the artifact — a full reference for a Skillfile
// FROM, a repository name for a catalog page — so the message reads the way the
// caller's user wrote the thing.
func fetchContent(ctx context.Context, repo *remote.Repository, name, reference string) (Content, error) {
	// Resolved once, and everything after this point goes by the descriptor.
	// Asking the registry for the reference a second time could land on
	// different content than the one the pin was taken from, which is exactly
	// the mutability 8.3 pins against.
	desc, err := repo.Resolve(ctx, reference)
	if err != nil {
		return Content{}, fmt.Errorf("resolve %s: %w", name, err)
	}

	body, err := content.FetchAll(ctx, repo, desc)
	if err != nil {
		return Content{}, fmt.Errorf("fetch %s: %w", name, err)
	}
	manifest, err := decodeManifest(body, desc.Digest.String(), name, reference)
	if err != nil {
		return Content{}, fmt.Errorf("%s: parse the base manifest: %w", name, err)
	}
	// 2.1: a skill artifact has exactly one content layer. A reference that
	// resolves to something else — a container image, an index — is not a base
	// a skill can be built from, and guessing which layer was meant would be
	// worse than saying so.
	if len(manifest.Layers) != 1 {
		return Content{}, fmt.Errorf("%s: the base has %d layers, want exactly 1",
			name, len(manifest.Layers))
	}

	layer, err := content.FetchAll(ctx, repo, manifest.Layers[0])
	if err != nil {
		return Content{}, fmt.Errorf("fetch the content layer of %s: %w", name, err)
	}
	unpacked, err := UnpackContent(layer)
	if err != nil {
		return Content{}, fmt.Errorf("%s: %w", name, err)
	}
	unpacked.Digest = desc.Digest.String()
	return unpacked, nil
}

// UnpackContent reads a skill artifact's content layer into a file map.
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
func UnpackContent(layer []byte) (Content, error) {
	gr, err := gzip.NewReader(bytes.NewReader(layer))
	if err != nil {
		return Content{}, fmt.Errorf("read the content layer: %w", err)
	}
	defer func() { _ = gr.Close() }()

	out := Content{Files: map[string][]byte{}}
	// Bounded on the decompressed side: the compressed layer's size is whatever
	// the descriptor claimed, and what it expands to is not.
	tr := tar.NewReader(io.LimitReader(gr, MaxContentLayer))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Content{}, fmt.Errorf("read the content layer: %w", err)
		}

		name := strings.TrimSuffix(h.Name, "/")
		switch h.Typeflag {
		case tar.TypeDir:
			// Directories are implied by the paths of the files in them; the
			// map holds files only.
			continue
		case tar.TypeSymlink, tar.TypeLink:
			return Content{}, fmt.Errorf("%s: symlinks are not allowed in a skill", name)
		case tar.TypeReg:
		default:
			return Content{}, fmt.Errorf("%s: only regular files can be built from", name)
		}

		// Checked whole, before the root is stripped. Stripping first would let
		// `../etc/passwd` through: `..` would be taken for the skill root and
		// what remained of the name would look perfectly ordinary.
		if err := CheckPath(name); err != nil {
			return Content{}, err
		}

		prefix, rest, nested := strings.Cut(name, "/")
		if !nested || rest == "" {
			return Content{}, fmt.Errorf("%s: the content layer is not rooted at a skill directory", name)
		}
		if out.Root == "" {
			out.Root = prefix
		}
		// 2.1 roots the layer at one `<skill-name>/`. Two roots would mean the
		// artifact holds two skills, and stripping either would silently merge
		// them.
		if prefix != out.Root {
			return Content{}, fmt.Errorf("the content layer is rooted at both %s/ and %s/",
				out.Root, prefix)
		}

		body, err := io.ReadAll(tr)
		if err != nil {
			return Content{}, fmt.Errorf("read %s: %w", name, err)
		}
		// Checked again after the strip, because a path that was canonical
		// whole is not necessarily canonical in its tail.
		if err := CheckPath(rest); err != nil {
			return Content{}, err
		}
		out.Files[rest] = body
	}
	if out.Root == "" {
		return Content{}, fmt.Errorf("the content layer holds no files")
	}
	return out, nil
}

// CheckPath rejects what SPEC.md 2.5 rejects, so nothing read out of an
// artifact can name a location outside the skill it belongs to.
//
// One implementation, here, because it guards three callers with the same
// exposure: a Skillfile FROM naming any registry, the packer reading a
// directory, and a catalog pointed at arbitrary registries. A second copy is
// how one of them loses a rule.
func CheckPath(slash string) error {
	switch {
	case slash == "":
		return fmt.Errorf("empty path")
	case path.IsAbs(slash):
		return fmt.Errorf("%s: absolute paths are not allowed", slash)
	case strings.HasPrefix(slash, "../") || slash == ".." || strings.Contains(slash, "/../"):
		return fmt.Errorf("%s: .. escapes the skill root", slash)
	}
	if cleaned := path.Clean(slash); cleaned != slash {
		return fmt.Errorf("%s: path is not in canonical form (%s)", slash, cleaned)
	}
	return nil
}
