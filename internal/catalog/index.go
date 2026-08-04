package catalog

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/gaarutyunov/epos/internal/artifact"
	"github.com/gaarutyunov/epos/internal/registry"
)

// Enumeration modes. The choice is a setting, not a fallback chain (D3).
//
// Silently degrading from "everything in the namespace" to "whatever a file
// happens to list" would make a missing skill indistinguishable from a registry
// that answered 404, and the operator should have said which site they wanted.
const (
	// ModeNamespace sweeps GET /v2/_catalog and keeps the repositories under a
	// namespace. This is `epos list`'s path.
	ModeNamespace = "namespace"
	// ModeRefs resolves a checked-in list of references, needing no _catalog.
	// Several hosted registries disable that endpoint — errNoCatalog exists in
	// the shipped code because of it — and a demo that cannot enumerate its own
	// registry is not a demo.
	ModeRefs = "refs"
)

// IndexOptions says which skills the catalog holds.
type IndexOptions struct {
	// Host is the upstream registry, for display and for checking a refs file
	// does not name a different one.
	Host string
	// Namespace enumerates through _catalog. Mutually exclusive with Refs.
	Namespace string
	// Refs is an explicit reference list. Mutually exclusive with Namespace.
	Refs []string
	// NamespaceMode distinguishes "sweep the whole registry" (an empty
	// Namespace is legal — a registry that holds nothing but skills needs no
	// filter) from "resolve Refs".
	NamespaceMode bool
}

// Validate checks the two modes are exclusive and one was chosen.
//
// Checked at startup, before any network request: an operator who configured
// neither should be told so by a process that has not yet opened a connection.
func (o IndexOptions) Validate() error {
	if o.NamespaceMode && len(o.Refs) > 0 {
		return errors.New("--catalog.namespace and --catalog.refs are mutually exclusive")
	}
	if !o.NamespaceMode && len(o.Refs) == 0 {
		return errors.New("the catalog needs skills to show: pass --catalog.namespace or --catalog.refs")
	}
	return nil
}

// BuildIndex reads the catalog's skills from the registry.
//
// It never returns an error. A failed or partial index must not stop the
// registry (D2a): epos-registry starts today without ever contacting upstream,
// and enabling the catalog must not turn an upstream blip at boot into a relay
// that will not serve /v2/. The failure is logged, it is recorded on the
// Catalog, and the catalog's own pages say so.
//
// The index is static for the process's lifetime; refreshing it is a restart.
// That is a real limitation and the right one for a first version — a refresh
// loop is a goroutine, a lock and a cache-invalidation policy. It says nothing
// about the counts, which are read per request (D4e); conflating the two is the
// mistake that would have made "pull, refresh, the number moved" unsatisfiable.
func BuildIndex(ctx context.Context, client registry.Client, opts IndexOptions) Catalog {
	c := Catalog{Registry: opts.Host}

	var refs []reference
	var err error
	if opts.NamespaceMode {
		refs, err = sweepNamespace(ctx, client, opts.Namespace)
	} else {
		refs, err = resolveRefs(opts.Refs, opts.Host)
	}
	if err != nil {
		c.Err = err.Error()
		log.Printf("epos-registry: the catalog index could not be built: %v", err)
		return c
	}

	for _, ref := range refs {
		skill, err := readSkill(ctx, client, ref)
		if err != nil {
			// One unreadable artifact leaves that skill out and the rest
			// listed (D3d). A catalog that fails wholesale because one
			// publisher pushed something broken is a catalog one publisher can
			// take down.
			log.Printf("epos-registry: the catalog skipped %s: %v", ref.repository, err)
			continue
		}
		c.Skills = append(c.Skills, skill)
	}

	sort.Slice(c.Skills, func(i, j int) bool {
		return c.Skills[i].Repository < c.Skills[j].Repository
	})
	return c
}

// reference is one repository the index will read, and the tag it renders.
type reference struct {
	repository string
	// tag is empty in namespace mode, where the tag list is read from the
	// registry; a refs entry names one explicitly.
	tag string
}

func sweepNamespace(ctx context.Context, client registry.Client, namespace string) ([]reference, error) {
	repositories, err := client.Catalog(ctx)
	if err != nil {
		if errors.Is(err, registry.ErrNoCatalog) {
			// A namespace sweep against an upstream without _catalog fails,
			// naming the registry; it does not fall back to some other mode
			// (D3). Falling back would produce a different site from the one
			// the operator asked for and never say so.
			return nil, err
		}
		return nil, err
	}
	repositories = registry.WithinNamespace(repositories, namespace)
	sort.Strings(repositories)

	out := make([]reference, 0, len(repositories))
	for _, r := range repositories {
		out = append(out, reference{repository: r})
	}
	return out, nil
}

// resolveRefs parses a checked-in reference list.
//
// A line is `<repository>:<tag>`, optionally prefixed with the upstream host —
// the catalog browses the registry the process already fronts and there is no
// separate registry setting (D3, 8.2a). A line naming a different host is an
// error rather than a silent fetch from somewhere else: a registry's own UI
// showing somebody else's registry is exactly what that rule exists to prevent.
func resolveRefs(refs []string, host string) ([]reference, error) {
	out := make([]reference, 0, len(refs))
	for _, line := range refs {
		repository, tag, ok := strings.Cut(line, ":")
		if !ok || repository == "" || tag == "" {
			return nil, fmt.Errorf("%q is not a reference: write <repository>:<tag>", line)
		}
		if host != "" && strings.HasPrefix(repository, host+"/") {
			repository = strings.TrimPrefix(repository, host+"/")
		} else if strings.Contains(repository, "/") && looksLikeHost(repository) && host != "" {
			return nil, fmt.Errorf("%q names a registry other than %s: the catalog shows the "+
				"registry it fronts, so a reference either omits the host or repeats it", line, host)
		}
		out = append(out, reference{repository: repository, tag: tag})
	}
	return out, nil
}

// looksLikeHost applies Docker's own rule for telling a registry host from the
// first segment of a repository name: it carries a dot or a port, or it is
// localhost.
func looksLikeHost(ref string) bool {
	head, _, ok := strings.Cut(ref, "/")
	if !ok {
		return false
	}
	return head == "localhost" || strings.ContainsAny(head, ".:")
}

// ReadRefsFile reads a reference list, one per line.
//
// Blank lines and `#` comments are skipped, so the file can say what it is for.
func ReadRefsFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read the reference list: %w", err)
	}
	defer func() { _ = file.Close() }()

	var refs []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		refs = append(refs, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(refs) == 0 {
		return nil, fmt.Errorf("%s lists no references", path)
	}
	return refs, nil
}

// readSkill turns one reference into a catalog entry.
//
// Metadata comes from the manifest and content from the layer, and only a
// detail page fetches the layer: one manifest GET yields the title, the
// description, the frontmatter from the inline config and the provenance
// annotations, which is everything a list page shows (D3).
func readSkill(ctx context.Context, client registry.Client, ref reference) (Skill, error) {
	tags := []string{ref.tag}
	if ref.tag == "" {
		var err error
		tags, err = client.Tags(ctx, ref.repository)
		if err != nil {
			return Skill{}, fmt.Errorf("list the versions of %s: %w", ref.repository, err)
		}
		if len(tags) == 0 {
			return Skill{}, fmt.Errorf("%s has no versions", ref.repository)
		}
	}

	sorted := append([]string(nil), tags...)
	sortVersions(sorted)

	manifest, err := client.Manifest(ctx, ref.repository, sorted[0])
	if err != nil {
		return Skill{}, fmt.Errorf("resolve %s:%s: %w", ref.repository, sorted[0], err)
	}
	return skillFrom(ref.repository, sorted, manifest), nil
}

// LoadDocument fetches and renders a skill's SKILL.md for a detail page.
//
// The whole content layer is fetched: SPEC.md 2.1 makes it one gzipped tar, so
// there is no range request that yields one entry. A skill whose layer cannot
// be read, is oversized or is hostile still lists — its page says the document
// could not be read (D3d), which is why every failure here lands on the skill
// rather than on the error return.
//
// The second result says whether the outcome is a *property of the artifact*
// rather than of this moment, and therefore whether a caller may cache it
// against the manifest digest. It matters: an oversized layer, a missing
// SKILL.md and a document that will not render are deterministic — the digest
// names those bytes and they will fail the same way forever, and caching them
// is what stops an attacker's 64 MiB layer being fetched on every request. A
// fetch that failed is not: the registry was unreachable for a moment, and a
// cache keyed on an immutable digest would make one blip a permanent message on
// that page until the process restarts.
func LoadDocument(ctx context.Context, client registry.Client, s Skill) (Skill, bool) {
	content, err := client.FetchContent(ctx, s.Repository, s.Version)
	if err != nil {
		s.DocumentError = fmt.Sprintf("the document could not be read: %v", err)
		return s, false
	}

	source, ok := content.Files[artifact.SkillFile]
	if !ok {
		s.DocumentError = fmt.Sprintf("the artifact carries no %s", artifact.SkillFile)
		return s, true
	}

	document, err := renderMarkdown(source)
	if err != nil {
		s.DocumentError = fmt.Sprintf("the document could not be rendered: %v", err)
		return s, true
	}
	s.Document = document
	return s, true
}
