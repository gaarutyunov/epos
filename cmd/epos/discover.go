package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/spf13/cobra"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

//go:generate go tool mockgen -source=discover.go -destination=mocks_test.go -package=main

// errNoCatalog reports a registry that does not implement GET /v2/_catalog.
//
// SPEC.md 7.1: there is no other way to enumerate a registry, and Epos does not
// compensate for the gap — no hand-authored catalog file, no scanning of
// configured prefixes, no published collection index. list and search say so
// and exit non-zero; a direct reference needs no catalog and is unaffected.
var errNoCatalog = errors.New("registry does not support catalog enumeration")

// registryClient is the whole of what discovery asks a registry for.
//
// It is an interface because laziness is a behavioural requirement rather than
// an optimisation (SPEC.md 7.2): `epos list` without --versions must issue no
// per-repository request at all, and the only way to assert that is over the
// calls actually made.
type registryClient interface {
	// Catalog returns the registry's repository names — step 1 of 7.2. It
	// returns errNoCatalog when the registry does not implement the endpoint.
	Catalog(ctx context.Context) ([]string, error)
	// Tags returns one repository's versions — step 3.
	Tags(ctx context.Context, repository string) ([]string, error)
	// Annotations returns one manifest's annotations — step 4.
	Annotations(ctx context.Context, repository, reference string) (map[string]string, error)
}

// skill is one row of a listing.
//
// A row without a version is the lazy case: the catalog knows the repository
// name and nothing else has been asked for.
type skill struct {
	repository  string
	version     string
	name        string
	description string
}

// discover runs the client-side pipeline of SPEC.md 7.2.
//
// Entirely client-side: no discovery endpoint, no content negotiation, no
// Epos-specific media type. The catalog is filtered to the skill namespace,
// and only then — and only when versions is set — is each repository asked for
// its tags and each manifest for its annotations.
func discover(ctx context.Context, c registryClient, namespace string, versions bool) ([]skill, error) {
	repositories, err := c.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	repositories = withinNamespace(repositories, namespace)

	// The catalog arrives in whatever order the registry chose, and the
	// namespace filter preserves it. Sorting here is what makes two runs
	// against the same registry print the same thing.
	sort.Strings(repositories)

	if !versions {
		// Step 2 is the end of the road without --versions. Nothing below this
		// line may run: a per-repository request here would be one round trip
		// per skill on a command that was asked only which skills exist.
		listing := make([]skill, 0, len(repositories))
		for _, repository := range repositories {
			listing = append(listing, skill{repository: repository})
		}
		return listing, nil
	}

	var listing []skill
	for _, repository := range repositories {
		tags, err := c.Tags(ctx, repository)
		if err != nil {
			return nil, fmt.Errorf("list the versions of %s: %w", repository, err)
		}
		// tags/list is specified as sorted, but a listing that depends on the
		// registry honouring that would differ between registries.
		sort.Strings(tags)

		for _, tag := range tags {
			annotations, err := c.Annotations(ctx, repository, tag)
			if err != nil {
				return nil, fmt.Errorf("resolve %s:%s: %w", repository, tag, err)
			}
			listing = append(listing, skill{
				repository:  repository,
				version:     tag,
				name:        annotations[ocispec.AnnotationTitle],
				description: annotations[ocispec.AnnotationDescription],
			})
		}
	}
	return listing, nil
}

// withinNamespace keeps the repositories under the configured skill namespace.
//
// An empty namespace means the whole registry: a registry that holds nothing
// but skills needs no filter, and requiring one would make the common case
// harder for nothing.
func withinNamespace(repositories []string, namespace string) []string {
	namespace = strings.Trim(namespace, "/")
	if namespace == "" {
		return repositories
	}

	prefix := namespace + "/"
	kept := make([]string, 0, len(repositories))
	for _, repository := range repositories {
		if repository == namespace || strings.HasPrefix(repository, prefix) {
			kept = append(kept, repository)
		}
	}
	return kept
}

// matches is the search filter of SPEC.md 7.3: repository name, skill name and
// description, case-insensitively.
//
// A client-side filter over the enumeration, not a server-side query — the OCI
// Distribution API has no search endpoint and epos-registry does not add one.
func (s skill) matches(query string) bool {
	query = strings.ToLower(query)
	for _, field := range []string{s.repository, s.name, s.description} {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

// printSkills writes a listing, one skill per line.
//
// Tab-separated rather than aligned: the columns are a repository reference, a
// name and a description, and a listing that shifts its own layout as
// descriptions get longer is worse to diff and worse to pipe into cut.
func printSkills(out io.Writer, listing []skill) {
	for _, s := range listing {
		if s.version == "" {
			fmt.Fprintln(out, s.repository)
			continue
		}
		fmt.Fprintf(out, "%s:%s\t%s\t%s\n", s.repository, s.version, s.name, s.description)
	}
}

// catalogUnavailable is what SPEC.md 7.1 requires the user to be told.
//
// Deliberately not a bare 404 or a generic HTTP error: the registry answered
// correctly, it simply does not offer the endpoint discovery is built on, and
// the fix is to use a direct reference rather than to retry.
func catalogUnavailable(registry string) error {
	return fmt.Errorf("%s does not support catalog enumeration (GET /v2/_catalog), "+
		"so its skills cannot be listed or searched; direct references need no catalog, "+
		"for example: epos pull %s/<repository>:<version>", registry, registry)
}

// discoveryFlags configure which registry the discovery commands enumerate.
type discoveryFlags struct {
	registry  string
	namespace string
	plainHTTP bool
}

func (f *discoveryFlags) bind(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVar(&f.registry, "registry", "",
		"registry to enumerate, as host[:port] (required)")
	flags.StringVar(&f.namespace, "namespace", "",
		"only enumerate repositories under this namespace (default: the whole registry)")
	flags.BoolVar(&f.plainHTTP, "plain-http", false, "talk to the registry over HTTP")
}

// open resolves the flags into a client.
//
// The commands take the client as an argument rather than building it inside
// themselves, so a test can drive the whole of runList and runSearch — the
// laziness of 7.2 and the missing-capability message of 7.1 included — without
// a registry.
func (f *discoveryFlags) open() (registryClient, error) {
	if f.registry == "" {
		return nil, errors.New("a registry is required: pass --registry")
	}
	return newOCIRegistry(f.registry, f.plainHTTP)
}

// ociRegistry is registryClient over the plain OCI Distribution API.
type ociRegistry struct {
	reg *remote.Registry
}

// newOCIRegistry returns a client for a registry host.
//
// remote.NewRegistry parses the host as an OCI reference, so a host carrying a
// port — "127.0.0.1:45100" — stays intact rather than being split at the colon
// as if the port were a tag.
func newOCIRegistry(host string, plainHTTP bool) (*ociRegistry, error) {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		return nil, fmt.Errorf("registry %q: %w", host, err)
	}
	reg.PlainHTTP = plainHTTP
	return &ociRegistry{reg: reg}, nil
}

func (o *ociRegistry) Catalog(ctx context.Context) ([]string, error) {
	var repositories []string
	err := o.reg.Repositories(ctx, "", func(page []string) error {
		repositories = append(repositories, page...)
		return nil
	})
	if err != nil {
		if unsupported(err) {
			return nil, errNoCatalog
		}
		return nil, fmt.Errorf("list the repositories of %s: %w", o.reg.Reference.Registry, err)
	}
	return repositories, nil
}

func (o *ociRegistry) Tags(ctx context.Context, repository string) ([]string, error) {
	repo, err := o.reg.Repository(ctx, repository)
	if err != nil {
		return nil, err
	}

	var tags []string
	if err := repo.Tags(ctx, "", func(page []string) error {
		tags = append(tags, page...)
		return nil
	}); err != nil {
		return nil, err
	}
	return tags, nil
}

func (o *ociRegistry) Annotations(ctx context.Context, repository, reference string) (map[string]string, error) {
	repo, err := o.reg.Repository(ctx, repository)
	if err != nil {
		return nil, err
	}

	desc, body, err := repo.FetchReference(ctx, reference)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	manifest, err := content.ReadAll(body, desc)
	if err != nil {
		return nil, err
	}

	// Only the annotations are read. SPEC.md 7.2 has the name and description
	// come from the frontmatter-derived annotations, and 2.1 puts them on the
	// manifest, so a listing costs one fetch per version and never touches a
	// config blob or a layer.
	var parsed struct {
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.Unmarshal(manifest, &parsed); err != nil {
		return nil, fmt.Errorf("parse the manifest of %s:%s: %w", repository, reference, err)
	}
	return parsed.Annotations, nil
}

// unsupported reports whether err is the registry declining to serve _catalog
// at all, rather than failing to answer it.
//
// 404 is what a registry that never routes the endpoint returns; 403 and 405
// are what one that knows the route and refuses it returns; 501 is the explicit
// "not implemented". 401 is deliberately absent: that is a credentials problem,
// and reporting it as a missing capability would send the user looking for the
// wrong thing.
func unsupported(err error) bool {
	var resp *errcode.ErrorResponse
	if !errors.As(err, &resp) {
		return false
	}
	switch resp.StatusCode {
	case http.StatusForbidden, http.StatusNotFound,
		http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	default:
		return false
	}
}

// compile-time assertion that the OCI client satisfies registryClient.
var _ registryClient = (*ociRegistry)(nil)
