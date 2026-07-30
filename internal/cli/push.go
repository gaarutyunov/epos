package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/gaarutyunov/epos/internal/store"
)

// newPushCommand publishes a skill the local store already holds.
//
// The operands are helm's, in helm's order: the artifact, then where it goes.
// The name and the version come out of the store tag, never from a flag, the
// same way `helm push` takes them out of the chart rather than from the command
// line — there is no --version to disagree with SKILL.md's frontmatter.
//
// It takes no directory. `epos pack ./reviewer && epos push reviewer:1.0.0 …`
// is two words longer than a pack-and-push and has one meaning: a directory
// that packed to a different digest than the one in the store would publish
// silently, and SPEC.md 2.4 makes that digest the artifact's identity. Helm
// made the same split for the same reason.
func newPushCommand() *cobra.Command {
	var opts registryOptions

	cmd := &cobra.Command{
		Use:   "push <name>:<version> <destination>",
		Short: "Publish a skill from the local store to a registry",
		Long: "push copies a skill the local store already holds to an OCI registry,\n" +
			"byte for byte. Nothing is repacked and nothing is re-derived, so the\n" +
			"digest `epos pack` printed is the digest that arrives.\n\n" +
			"The destination names a namespace and the skill's name is appended:\n\n" +
			"  epos push reviewer:1.0.0 oci://ghcr.io/acme/agent-skills\n\n" +
			"publishes ghcr.io/acme/agent-skills/reviewer, tagged 1.0.0. An oci://\n" +
			"prefix is accepted and is not required. The reference push reports is\n" +
			"the one it resolved, so a destination that already ends in the skill's\n" +
			"own name shows the doubled segment straight away.\n\n" +
			"A credential stored for the registry is sent; a registry that permits\n" +
			"anonymous writes needs none. Log in with `epos registry login`.\n\n" +
			"This is a direct client-to-registry copy. epos-registry serves no write\n" +
			"path (SPEC.md 4.5) and no bytes pass through it.",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPush(cmd.Context(), cmd.OutOrStdout(), args[0], args[1], opts)
		},
	}
	opts.bind(cmd)
	return cmd
}

func runPush(ctx context.Context, out io.Writer, tag, destination string,
	opts registryOptions) error {
	name, version, err := splitStoreTag(tag)
	if err != nil {
		return err
	}

	resolved, err := pushReference(destination, name, version)
	if err != nil {
		return err
	}

	s, err := store.Default()
	if err != nil {
		return err
	}

	repo, err := remote.NewRepository(resolved.Registry + "/" + resolved.Repository)
	if err != nil {
		return fmt.Errorf("destination %q: %w", destination, err)
	}
	repo.PlainHTTP = opts.plainHTTP
	// Deliberately not the Epos-Download client `pull` uses: a publish is not a
	// download, and SPEC.md 5.1 counts only blob GETs.
	if repo.Client, err = opts.client(nil); err != nil {
		return err
	}

	var (
		pushed  ocispec.Descriptor
		unknown bool
	)
	// The shared lock is held across the whole upload, so a large push blocks
	// `pack`, `pull` and `prune` in another terminal for its duration. That is
	// what Read's own doc comment anticipates, and it is the correct trade:
	// buffering the artifact into memory to release the lock earlier would put
	// a second copy of every layer in RAM to avoid a wait nobody has
	// complained about.
	err = s.Read(ctx, func(ctx context.Context, st *oci.Store) error {
		// Resolved before the copy, so a tag the store does not hold costs no
		// request at all.
		if _, err := st.Resolve(ctx, tag); err != nil {
			unknown = true
			return err
		}
		var err error
		// The store tags a skill <name>:<version> because one flat layout holds
		// many skills; a registry repository holds exactly one, so the remote
		// tag is the version alone. This is the mirror image of runPull, which
		// maps the other way.
		pushed, err = oras.Copy(ctx, st, tag, repo, version, oras.DefaultCopyOptions)
		return err
	})
	if unknown {
		return fmt.Errorf("the local store holds no %s; `epos store ls` lists what it holds", tag)
	}
	if err != nil {
		return fmt.Errorf("push %s to %s: %w", tag, resolved,
			opts.explainAuth(ctx, resolved.Registry, err))
	}

	// One line, two fields — what `pack` and `pull` print. The information is
	// helm's; only the framing differs, and a user piping epos into cut should
	// not have to special-case one command.
	fmt.Fprintf(out, "%s %s\n", resolved, pushed.Digest)
	return nil
}

// splitStoreTag reads the skill's name and version out of a local store tag.
//
// Both come from the tag by construction (SPEC.md 9.1), which is why push has
// neither a --name nor a --version.
func splitStoreTag(operand string) (name, version string, err error) {
	// The store is tag-addressed, and `sha256:…` is not even in the character
	// set a tag allows. `pull` refuses a digest in the same words rather than
	// inventing a tag for the caller.
	if strings.Contains(operand, "@") || digest.Digest(operand).Validate() == nil {
		return "", "", fmt.Errorf("push needs a tag; %s names a digest", operand)
	}

	name, version, ok := strings.Cut(operand, ":")
	if !ok || name == "" || version == "" {
		return "", "", fmt.Errorf(
			"push needs a <name>:<version> tag; %s names no version. "+
				"`epos store ls` lists what the store holds", operand)
	}
	return name, version, nil
}

// pushReference resolves the destination into the repository and tag a skill is
// published to.
//
// The destination names a namespace and the skill's name is appended, always —
// including when the last path segment already equals the skill's name. Three
// things agree on this. Helm does exactly it (`helm push mychart-0.1.0.tgz
// oci://reg/ns` lands at reg/ns/mychart:0.1.0). SPEC.md 2.1 fixes the
// repository convention as <registry>/<namespace>/agent-skills/<skill-name>,
// so "the repository name therefore identifies the skill without any manifest
// lookup". And runPull already reads the skill's name back out of the last path
// segment, which makes appending the exact inverse of what pull does — a round
// trip that holds by construction rather than by convention.
//
// Detecting and de-duplicating a destination that already ends in the skill's
// name is not safe: `…/reviewer/reviewer` is a legal repository somebody may
// genuinely want. Push prints the reference it resolved instead, which makes
// the mistake visible on the first run rather than on the first failed pull.
func pushReference(destination, name, version string) (registry.Reference, error) {
	// oci:// costs one TrimPrefix and makes a command line typed from helm
	// muscle memory work. It is not required, because helm needs the scheme to
	// tell OCI registries from classic HTTP chart repositories and epos has
	// only the one transport — requiring it would make push the only epos
	// command whose reference is written differently from every other.
	trimmed := strings.TrimSuffix(strings.TrimPrefix(destination, "oci://"), "/")
	if trimmed == "" {
		return registry.Reference{}, fmt.Errorf("destination %q names no registry", destination)
	}

	// Parsed rather than split by hand, so a host carrying a port survives:
	// "localhost:5000/demo" must not have its port read as a tag.
	ref, err := registry.ParseReference(trimmed + "/" + name)
	if err != nil {
		return registry.Reference{}, fmt.Errorf(
			"destination %q: %w; it names a namespace, not a repository or a tag", destination, err)
	}
	ref.Reference = version
	if err := ref.ValidateReferenceAsTag(); err != nil {
		return registry.Reference{}, fmt.Errorf("version %q: %w", version, err)
	}
	return ref, nil
}
