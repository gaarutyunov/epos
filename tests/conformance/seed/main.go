// Command seed publishes one skill artifact into a registry and prints the
// digests the OCI conformance suite needs to find it.
//
// epos-registry serves no write path in A1 (SPEC.md 4.5 is a later milestone),
// so the conformance suite cannot push its own fixtures through it. The suite
// handles exactly this case: given OCI_TAG_NAME, OCI_MANIFEST_DIGEST and
// OCI_BLOB_DIGEST it runs the pull workflow against content that already
// exists. This seeds that content directly into the upstream registry, which
// epos-registry then fronts.
//
// Output is shell-friendly `key=value` lines for the workflow to eval.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: seed <registry-host:port> <repo:tag>")
		os.Exit(2)
	}

	if err := run(context.Background(), os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, host, ref string) error {
	name, tag, ok := strings.Cut(ref, ":")
	if !ok {
		return fmt.Errorf("reference %q is not <repo>:<tag>", ref)
	}

	store := memory.New()
	layer, err := oras.PushBytes(ctx, store,
		"application/vnd.agentskills.skill.layer.v1.tar",
		[]byte("SKILL.md for "+name+" "+tag))
	if err != nil {
		return fmt.Errorf("push layer: %w", err)
	}

	manifest, err := oras.PackManifest(ctx, store, oras.PackManifestVersion1_1,
		"application/vnd.agentskills.skill.v1", oras.PackManifestOptions{
			Layers: []ocispec.Descriptor{layer},
		})
	if err != nil {
		return fmt.Errorf("pack manifest: %w", err)
	}
	if err := store.Tag(ctx, manifest, tag); err != nil {
		return fmt.Errorf("tag manifest: %w", err)
	}

	repo, err := remote.NewRepository(strings.TrimPrefix(host, "http://") + "/" + name)
	if err != nil {
		return fmt.Errorf("upstream repository: %w", err)
	}
	repo.PlainHTTP = true

	if _, err := oras.Copy(ctx, store, tag, repo, tag, oras.DefaultCopyOptions); err != nil {
		return fmt.Errorf("copy to upstream: %w", err)
	}

	fmt.Printf("OCI_TAG_NAME=%s\n", tag)
	fmt.Printf("OCI_MANIFEST_DIGEST=%s\n", manifest.Digest.String())
	fmt.Printf("OCI_BLOB_DIGEST=%s\n", layer.Digest.String())
	return nil
}
