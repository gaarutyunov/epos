package registry

import (
	"context"
	"fmt"

	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
)

// Options is how a program reaches a registry.
//
// Deliberately plain: no cobra, no koanf, no environment reading. The CLI's
// registryOptions owns the flag binding and the Docker credential store and
// builds one of these; epos-registry builds one from its own configuration.
// Two binaries with two configuration mechanisms and one client.
type Options struct {
	// PlainHTTP talks to the registry over HTTP.
	PlainHTTP bool
	// Client is what requests go through — typically an *auth.Client carrying
	// stored credentials. A nil Client is an anonymous one, which is the whole
	// of what a public catalog needs.
	Client remote.Client
	// Explain turns a registry's refusal to serve a request into a message
	// that says what to do about it. Optional: without it, errors surface as
	// they come.
	//
	// A hook rather than a behaviour here because the two messages the CLI
	// gives — "no credential is stored" and "the stored credential was
	// rejected" — are the user-facing text of an auth failure, and they need
	// the credential store to choose between them. The store is the CLI's.
	Explain func(ctx context.Context, host string, err error) error
}

// OCIRegistry is Client over the plain OCI Distribution API.
type OCIRegistry struct {
	reg  *remote.Registry
	opts Options
}

// NewOCIRegistry returns a client for a registry host.
//
// remote.NewRegistry parses the host as an OCI reference, so a host carrying a
// port — "127.0.0.1:45100" — stays intact rather than being split at the colon
// as if the port were a tag.
func NewOCIRegistry(host string, opts Options) (*OCIRegistry, error) {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		return nil, fmt.Errorf("registry %q: %w", host, err)
	}
	reg.PlainHTTP = opts.PlainHTTP
	if opts.Client != nil {
		reg.Client = opts.Client
	}
	return &OCIRegistry{reg: reg, opts: opts}, nil
}

// Host is the registry this client enumerates.
func (o *OCIRegistry) Host() string { return o.reg.Reference.Registry }

func (o *OCIRegistry) Catalog(ctx context.Context) ([]string, error) {
	var repositories []string
	err := o.reg.Repositories(ctx, "", func(page []string) error {
		repositories = append(repositories, page...)
		return nil
	})
	if err != nil {
		if Unsupported(err) {
			return nil, ErrNoCatalog
		}
		return nil, fmt.Errorf("list the repositories of %s: %w", o.reg.Reference.Registry,
			o.explain(ctx, err))
	}
	return repositories, nil
}

func (o *OCIRegistry) Tags(ctx context.Context, repository string) ([]string, error) {
	repo, err := o.repository(ctx, repository)
	if err != nil {
		return nil, err
	}

	var tags []string
	if err := repo.Tags(ctx, "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	}); err != nil {
		return nil, o.explain(ctx, err)
	}
	return tags, nil
}

func (o *OCIRegistry) Annotations(ctx context.Context, repository, reference string) (map[string]string, error) {
	// Only the annotations are read. SPEC.md 7.2 has the name and description
	// come from the frontmatter-derived annotations, and 2.1 puts them on the
	// manifest, so a listing costs one fetch per version and never touches a
	// config blob or a layer.
	manifest, err := o.Manifest(ctx, repository, reference)
	if err != nil {
		return nil, err
	}
	return manifest.Annotations, nil
}

func (o *OCIRegistry) Manifest(ctx context.Context, repository, reference string) (Manifest, error) {
	repo, err := o.repository(ctx, repository)
	if err != nil {
		return Manifest{}, err
	}

	desc, body, err := repo.FetchReference(ctx, reference)
	if err != nil {
		return Manifest{}, o.explain(ctx, err)
	}
	defer func() { _ = body.Close() }()

	manifest, err := content.ReadAll(body, desc)
	if err != nil {
		return Manifest{}, err
	}
	return decodeManifest(manifest, desc.Digest.String(), repository, reference)
}

func (o *OCIRegistry) FetchContent(ctx context.Context, repository, reference string) (Content, error) {
	repo, err := o.repository(ctx, repository)
	if err != nil {
		return Content{}, err
	}
	return fetchContent(ctx, repo, repository, reference)
}

func (o *OCIRegistry) repository(ctx context.Context, repository string) (*remote.Repository, error) {
	repo, err := o.reg.Repository(ctx, repository)
	if err != nil {
		return nil, err
	}
	// reg.Repository hands back a repository configured from the registry, so
	// PlainHTTP and the credential-bearing client come with it. The assertion
	// is here rather than assumed because FetchContent needs a concrete
	// *remote.Repository to read a layer through.
	concrete, ok := repo.(*remote.Repository)
	if !ok {
		return nil, fmt.Errorf("registry %s returned an unexpected repository type %T",
			o.reg.Reference.Registry, repo)
	}
	return concrete, nil
}

// explain names the registry and the cause when it answered 401.
func (o *OCIRegistry) explain(ctx context.Context, err error) error {
	if o.opts.Explain == nil {
		return err
	}
	return o.opts.Explain(ctx, o.reg.Reference.Registry, err)
}

// compile-time assertion that the OCI client satisfies Client.
var _ Client = (*OCIRegistry)(nil)
