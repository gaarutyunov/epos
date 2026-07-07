package oci

import (
	"context"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// registry builds an ORAS remote registry handle for a host base URL.
func (c *Client) registry(host string) (*remote.Registry, error) {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		return nil, err
	}
	reg.PlainHTTP = c.PlainHTTP
	if c.Auth != nil {
		reg.Client = &auth.Client{
			Client: retry.DefaultClient,
			Cache:  auth.NewCache(),
			Credential: auth.StaticCredential(host, auth.Credential{
				Username: c.Auth.Username,
				Password: c.Auth.Password,
			}),
		}
	}
	return reg, nil
}

// Catalog lists repositories via /v2/_catalog. Returns an error when the
// registry does not implement the catalog (used by the discovery probe to fall
// back to registered mode, SPEC §8.1.1).
func (c *Client) Catalog(ctx context.Context, host string) ([]string, error) {
	reg, err := c.registry(host)
	if err != nil {
		return nil, err
	}
	var repos []string
	err = reg.Repositories(ctx, "", func(page []string) error {
		repos = append(repos, page...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("oci: catalog %q: %w", host, err)
	}
	return repos, nil
}

// Tags lists the tags of a repository (registry/repo).
func (c *Client) Tags(ctx context.Context, repoRef string) ([]string, error) {
	repo, err := c.Repository(repoRef)
	if err != nil {
		return nil, err
	}
	var tags []string
	err = repo.Tags(ctx, "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("oci: tags %q: %w", repoRef, err)
	}
	return tags, nil
}

// Referrers returns the referrer descriptors of a subject digest, optionally
// filtered by artifactType. Used to discover cosign signatures attached via the
// OCI 1.1 subject/referrers mechanism (SPEC §7.1).
func (c *Client) Referrers(ctx context.Context, repoRef string, subject ocispec.Descriptor, artifactType string) ([]ocispec.Descriptor, error) {
	repo, err := c.Repository(repoRef)
	if err != nil {
		return nil, err
	}
	var refs []ocispec.Descriptor
	err = repo.Referrers(ctx, subject, artifactType, func(page []ocispec.Descriptor) error {
		refs = append(refs, page...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("oci: referrers %q: %w", repoRef, err)
	}
	return refs, nil
}

// PushReferrer uploads a blob artifact whose manifest carries a `subject`
// pointing at the given subject descriptor (the referrers mechanism). Used to
// attach a signature to a skill artifact.
func (c *Client) PushReferrer(ctx context.Context, repoRef, artifactType string, payload []byte, subject ocispec.Descriptor) (ocispec.Descriptor, error) {
	// The referrer's config is the artifact-type marker; its single layer is the
	// signature payload. The subject links it to the signed manifest.
	return c.pushWithSubject(ctx, repoRef, artifactType, payload, subject)
}
