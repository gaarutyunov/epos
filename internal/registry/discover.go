// Package registry is what a program needs to talk to an OCI registry about
// skills: enumeration (SPEC.md 7.2), one manifest read, and the fetch-and-untar
// of a skill's content layer.
//
// It exists as its own package because two binaries need it and neither may
// link the other's dependencies. cmd/epos-registry must not import internal/cli
// — that would pull the whole CLI, cobra tree and all, into the registry — and
// must not import internal/skillfile, which carries the Skillfile build
// language along with go-git, goawk, go-gitdiff and goccy/go-yaml. Everything
// here is oras-go and the standard library.
//
// Nothing in this package knows about cobra or koanf. Options is a plain struct
// each binary fills in its own way: internal/cli builds one from its
// registryOptions, which owns the flag binding and the Docker credential store.
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

//go:generate go tool mockgen -source=discover.go -destination=mocks_test.go -package=registry

// ErrNoCatalog reports a registry that does not implement GET /v2/_catalog.
//
// SPEC.md 7.1: there is no other way to enumerate a registry, and Epos does not
// compensate for the gap — no hand-authored catalog file, no scanning of
// configured prefixes, no published collection index. list and search say so
// and exit non-zero; a direct reference needs no catalog and is unaffected.
var ErrNoCatalog = errors.New("registry does not support catalog enumeration")

// Client is the whole of what discovery asks a registry for.
//
// It is an interface because laziness is a behavioural requirement rather than
// an optimisation (SPEC.md 7.2): `epos list` without --versions must issue no
// per-repository request at all, and the only way to assert that is over the
// calls actually made.
type Client interface {
	// Catalog returns the registry's repository names — step 1 of 7.2. It
	// returns ErrNoCatalog when the registry does not implement the endpoint.
	Catalog(ctx context.Context) ([]string, error)
	// Tags returns one repository's versions — step 3.
	Tags(ctx context.Context, repository string) ([]string, error)
	// Annotations returns one manifest's annotations — step 4.
	Annotations(ctx context.Context, repository, reference string) (map[string]string, error)
	// Manifest returns the parts of a manifest a catalog page is built from:
	// the annotations Annotations already returns, plus the config descriptor
	// carrying the inline frontmatter and the layer descriptors.
	//
	// Separate from Annotations rather than replacing it because discovery
	// deliberately reads nothing else (7.2), and a listing that started
	// decoding config blobs would be a behaviour change to `epos list`.
	Manifest(ctx context.Context, repository, reference string) (Manifest, error)
	// FetchContent reads a skill artifact's content layer into a file map,
	// with every guard 2.5 applies. Only a detail page needs it.
	FetchContent(ctx context.Context, repository, reference string) (Content, error)
}

// Manifest is the part of an OCI manifest anything in epos reads.
//
// Config carries the inline config blob in its Data field: internal/artifact
// inlines it at build time precisely so that a client wanting only the SKILL.md
// frontmatter — `epos search`, a catalog — reads it out of the manifest with no
// second round trip.
type Manifest struct {
	// Digest is what the reference resolved to. Immutable, which is what makes
	// it usable as a cache key.
	Digest      string
	Annotations map[string]string
	Config      ocispec.Descriptor
	Layers      []ocispec.Descriptor
}

// Skill is one row of a listing.
//
// A row without a version is the lazy case: the catalog knows the repository
// name and nothing else has been asked for.
type Skill struct {
	Repository  string
	Version     string
	Name        string
	Description string
}

// Discover runs the client-side pipeline of SPEC.md 7.2.
//
// Entirely client-side: no discovery endpoint, no content negotiation, no
// Epos-specific media type. The catalog is filtered to the skill namespace,
// and only then — and only when versions is set — is each repository asked for
// its tags and each manifest for its annotations.
func Discover(ctx context.Context, c Client, namespace string, versions bool) ([]Skill, error) {
	repositories, err := c.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	repositories = WithinNamespace(repositories, namespace)

	// The catalog arrives in whatever order the registry chose, and the
	// namespace filter preserves it. Sorting here is what makes two runs
	// against the same registry print the same thing.
	sort.Strings(repositories)

	if !versions {
		// Step 2 is the end of the road without --versions. Nothing below this
		// line may run: a per-repository request here would be one round trip
		// per skill on a command that was asked only which skills exist.
		listing := make([]Skill, 0, len(repositories))
		for _, repository := range repositories {
			listing = append(listing, Skill{Repository: repository})
		}
		return listing, nil
	}

	var listing []Skill
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
			listing = append(listing, Skill{
				Repository:  repository,
				Version:     tag,
				Name:        annotations[ocispec.AnnotationTitle],
				Description: annotations[ocispec.AnnotationDescription],
			})
		}
	}
	return listing, nil
}

// WithinNamespace keeps the repositories under the configured skill namespace.
//
// An empty namespace means the whole registry: a registry that holds nothing
// but skills needs no filter, and requiring one would make the common case
// harder for nothing.
func WithinNamespace(repositories []string, namespace string) []string {
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

// Matches is the search filter of SPEC.md 7.3: repository name, skill name and
// description, case-insensitively.
//
// A client-side filter over the enumeration, not a server-side query — the OCI
// Distribution API has no search endpoint and epos-registry does not add one.
func (s Skill) Matches(query string) bool {
	query = strings.ToLower(query)
	for _, field := range []string{s.Repository, s.Name, s.Description} {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

// CatalogUnavailable is what SPEC.md 7.1 requires the user to be told.
//
// Deliberately not a bare 404 or a generic HTTP error: the registry answered
// correctly, it simply does not offer the endpoint discovery is built on, and
// the fix is to use a direct reference rather than to retry.
func CatalogUnavailable(registry string) error {
	return fmt.Errorf("%s does not support catalog enumeration (GET /v2/_catalog), "+
		"so its skills cannot be listed or searched; direct references need no catalog, "+
		"for example: epos pull %s/<repository>:<version>", registry, registry)
}

// Unsupported reports whether err is the registry declining to serve _catalog
// at all, rather than failing to answer it.
//
// 404 is what a registry that never routes the endpoint returns; 403 and 405
// are what one that knows the route and refuses it returns; 501 is the explicit
// "not implemented". 401 is deliberately absent: that is a credentials problem,
// and reporting it as a missing capability would send the user looking for the
// wrong thing.
func Unsupported(err error) bool {
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

// decodeManifest reads the manifest fields anything in epos looks at.
func decodeManifest(body []byte, digest, repository, reference string) (Manifest, error) {
	var parsed struct {
		Annotations map[string]string    `json:"annotations"`
		Config      ocispec.Descriptor   `json:"config"`
		Layers      []ocispec.Descriptor `json:"layers"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return Manifest{}, fmt.Errorf("parse the manifest of %s:%s: %w", repository, reference, err)
	}
	return Manifest{
		Digest:      digest,
		Annotations: parsed.Annotations,
		Config:      parsed.Config,
		Layers:      parsed.Layers,
	}, nil
}
