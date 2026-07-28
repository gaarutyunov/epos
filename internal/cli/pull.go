package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"

	"github.com/gaarutyunov/epos/internal/artifact"
	"github.com/gaarutyunov/epos/internal/store"
)

func newPullCommand() *cobra.Command {
	var plainHTTP bool

	cmd := &cobra.Command{
		Use:   "pull <ref>",
		Short: "Pull a skill from a registry into the local store",
		Long: "pull fetches a skill and tags it in the local store. It sends\n" +
			"Epos-Download, so a registry fronted by epos-registry records the\n" +
			"download as verified.",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPull(cmd.Context(), cmd.OutOrStdout(), args[0], plainHTTP)
		},
	}
	cmd.Flags().BoolVar(&plainHTTP, "plain-http", false, "talk to the registry over HTTP")
	return cmd
}

func runPull(ctx context.Context, out io.Writer, ref string, plainHTTP bool) error {
	repo, srcTag, err := newRepository(ref, plainHTTP)
	if err != nil {
		return err
	}
	// The store tags what it holds (9.1) and a digest is not a tag: `sha256:…`
	// is not even in the character set a tag is allowed. Rather than invent a
	// tag for the caller, pull says what it needs. `verify`, which only
	// resolves, takes a digest reference happily.
	if strings.Contains(srcTag, ":") {
		return fmt.Errorf("pull needs a tag; %s names a digest", ref)
	}

	// SPEC.md 5.2: the epos CLI sends Epos-Download and stock oras does not,
	// which is what lets epos-registry tell a verified download from an
	// inflated one. The skill name comes from the repository, per 2.1.
	name := repo.Reference.Repository
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	repo.Client = downloadClient(name + "@" + srcTag)

	s, err := store.Default()
	if err != nil {
		return err
	}

	tag := name + ":" + srcTag
	var pulled ocispec.Descriptor
	err = s.Push(ctx, tag, func(ctx context.Context, st *oci.Store) (ocispec.Descriptor, error) {
		// Copied straight to the store's own tag. Handing Copy the source tag
		// instead would leave a bare "1.0.0" behind alongside
		// "reviewer:1.0.0", and the store would list the same skill twice.
		pulled, err = oras.Copy(ctx, repo, srcTag, st, tag, oras.DefaultCopyOptions)
		return pulled, err
	})
	if err != nil {
		return fmt.Errorf("pull %s: %w", ref, err)
	}

	fmt.Fprintf(out, "%s %s\n", tag, pulled.Digest)
	return nil
}

// newRepository splits a registry reference and returns a client for it, along
// with the tag or digest it named.
//
// oras-go's own parser does the splitting. Three things in a reference are
// separated by a colon — the registry's port, the tag, and the digest algorithm
// — and no single rule about which colon to cut at gets all three right. "The
// last colon after the last slash" handles the port and the tag, and cuts
// "…/pdf@sha256:abcd" in the middle of the digest, which is precisely the
// reference an 8.3 pin is written as.
func newRepository(ref string, plainHTTP bool) (*remote.Repository, string, error) {
	parsed, err := registry.ParseReference(ref)
	if err != nil {
		return nil, "", fmt.Errorf("reference %q: %w", ref, err)
	}
	if parsed.Reference == "" {
		return nil, "", fmt.Errorf("reference %q has no tag or digest", ref)
	}

	// Rebuilt from the parsed parts rather than handed the whole string: the
	// repository is the thing requests are addressed to, and the tag or digest
	// is passed per call.
	repo, err := remote.NewRepository(parsed.Registry + "/" + parsed.Repository)
	if err != nil {
		return nil, "", fmt.Errorf("reference %q: %w", ref, err)
	}
	repo.PlainHTTP = plainHTTP
	return repo, parsed.Reference, nil
}

// downloadClient adds Epos-Download to every request.
//
// Sent on all of them rather than only blob GETs: the header is a reporting
// hint, epos-registry counts only what 5.1 says counts, and a client that
// tried to guess which request is "the" blob fetch would have to model the
// registry's redirect behaviour to get it right.
func downloadClient(value string) remote.Client {
	return &http.Client{Transport: headerTransport{
		header: artifact.DownloadHeader,
		value:  value,
		next:   http.DefaultTransport,
	}}
}

type headerTransport struct {
	header string
	value  string
	next   http.RoundTripper
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Cloned: RoundTrip must not mutate the request it is given.
	clone := req.Clone(req.Context())
	clone.Header.Set(t.header, t.value)
	return t.next.RoundTrip(clone)
}
