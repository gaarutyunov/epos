package main

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

// newRepository splits "<registry>/<repo>:<tag>" and returns a client for it.
//
// The tag separator is the last colon *after* the last slash: a registry may
// carry a port, so cutting at the first colon splits "127.0.0.1:45100/…" in
// the wrong place.
func newRepository(ref string, plainHTTP bool) (*remote.Repository, string, error) {
	colon := strings.LastIndex(ref, ":")
	if colon < 0 || colon < strings.LastIndex(ref, "/") {
		return nil, "", fmt.Errorf("reference %q has no tag", ref)
	}
	base, tag := ref[:colon], ref[colon+1:]
	if tag == "" {
		return nil, "", fmt.Errorf("reference %q has no tag", ref)
	}

	repo, err := remote.NewRepository(base)
	if err != nil {
		return nil, "", fmt.Errorf("reference %q: %w", ref, err)
	}
	repo.PlainHTTP = plainHTTP
	return repo, tag, nil
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
