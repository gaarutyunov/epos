// Package catalog is the read-only HTML view epos-registry can serve over the
// listener that already answers /v2/, and export to a directory as static
// files.
//
// One model, one route table, one template set, two drivers. A page only one of
// them can produce is a bug (D2).
//
// This package holds the repository's only //go:embed, and it is imported by
// cmd/epos-registry and by nothing on the CLI's side of the graph. Someone
// running `go install .../cmd/epos` to pack and push a skill should not be
// carrying a hundred kilobytes of JavaScript, a stylesheet and four template
// trees to do it — and that is asserted by a test rather than intended
// (cmd/epos/imports_test.go).
package catalog

import (
	"encoding/json"
	"html/template"
	"sort"
	"strconv"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/gaarutyunov/epos/internal/artifact"
	"github.com/gaarutyunov/epos/internal/registry"
)

// Skill is one catalog entry: one repository, one rendered version.
//
// A skill is a repository, not a version (D3a). The download counter's only key
// is the repository — SPEC.md 5.1 is explicit that counting parses no manifest
// — so a per-version page would have to show a per-version count that does not
// exist, or show the repository's count on every version page, which is worse.
//
// No registry types appear here. The renderer has to be testable with a
// literal, and a model carrying descriptors would make every template test need
// a registry.
type Skill struct {
	// Repository is the OCI repository name, which is also the counter's key
	// and the detail page's route.
	Repository string
	// Name and Description come from the manifest annotations, which
	// internal/artifact derives from SKILL.md's frontmatter.
	Name        string
	Description string
	// Version is the newest tag; Versions is every tag, newest first.
	Version  string
	Versions []string
	// Digest is the manifest digest of Version. Immutable, so it keys the
	// document cache with no invalidation path to get wrong.
	Digest string
	// License is read from the frontmatter the config blob mirrors, when the
	// author declared one.
	License string
	// Provenance is present only on a skill `epos build` produced (D11).
	Provenance *Provenance
	// Document is the rendered SKILL.md, on a detail page only. Empty on a list
	// page, where nothing has fetched the content layer.
	Document template.HTML
	// DocumentError says why the document is missing, if it is. One bad
	// artifact fails one page (D3d): the skill still lists, and its page says
	// the document could not be read.
	DocumentError string
}

// Provenance is what `dev.epos.skillfile.stages` records: which Skillfile stage
// contributed each file, plus the base and Skillfile pins beside it (SPEC.md
// 2.3).
//
// Absent on a skill that was packed rather than built, and the whole section is
// omitted then — an empty provenance table is worse than none.
type Provenance struct {
	Stages          []Stage
	BaseName        string
	BaseDigest      string
	SkillfileDigest string
}

// Stage is one contributing stage and the files it put in the artifact.
type Stage struct {
	Name  string
	Files []string
}

// Catalog is the whole index the pages are built from.
type Catalog struct {
	// Registry is the upstream host the skills were read from.
	Registry string
	// Skills is one entry per repository, in a deterministic order.
	Skills []Skill
	// Err, when set, is why the index could not be built. The listener is up
	// and /v2/ is serving either way (D2a); the catalog says so on its own
	// pages rather than failing the process.
	Err string
}

// Repositories is the index's repository names, which is what a statistics
// source is scoped to.
func (c Catalog) Repositories() []string {
	out := make([]string, 0, len(c.Skills))
	for _, s := range c.Skills {
		out = append(out, s.Repository)
	}
	return out
}

// Lookup finds a skill by repository name.
//
// The served catalog answers only for repositories in the index built at
// startup: a path naming anything else is a 404 and no registry request is made
// (D3b). Without this a URL path is an instruction to fetch an arbitrary
// repository — an unauthenticated proxy wearing a catalog, now bolted to a
// registry.
func (c Catalog) Lookup(repository string) (Skill, bool) {
	for _, s := range c.Skills {
		if s.Repository == repository {
			return s, true
		}
	}
	return Skill{}, false
}

// skillFrom builds one entry from a manifest and the tags of its repository.
func skillFrom(repository string, tags []string, manifest registry.Manifest) Skill {
	versions := append([]string(nil), tags...)
	sortVersions(versions)

	s := Skill{
		Repository:  repository,
		Name:        manifest.Annotations[ocispec.AnnotationTitle],
		Description: manifest.Annotations[ocispec.AnnotationDescription],
		Versions:    versions,
		Digest:      manifest.Digest,
		Provenance:  provenanceFrom(manifest.Annotations),
	}
	if len(versions) > 0 {
		s.Version = versions[0]
	}
	if s.Name == "" {
		// A repository whose manifest carries no title still gets a name: the
		// last path segment is what the publisher called the skill.
		s.Name = repository[strings.LastIndex(repository, "/")+1:]
	}

	// The config blob arrives inline in its descriptor's Data field, which is
	// why one manifest GET is enough for a list page: internal/artifact inlines
	// it precisely so a discovery UI reads the frontmatter without a second
	// round trip. A blob that did not come inline is simply absent — the
	// catalog does not fetch it, because the fields it feeds are optional.
	if len(manifest.Config.Data) > 0 {
		var cfg artifact.Config
		if err := json.Unmarshal(manifest.Config.Data, &cfg); err == nil {
			s.License, _ = cfg["license"].(string)
			if s.Description == "" {
				s.Description = cfg.Description()
			}
		}
	}
	return s
}

// provenanceFrom decodes dev.epos.skillfile.stages into the detail page's
// provenance section.
//
// The annotation is a JSON object of slash-separated path to stage name, and it
// is present only on a skill `epos build` produced. A skill `epos pack` packed
// carries none, and then the whole section is omitted.
func provenanceFrom(annotations map[string]string) *Provenance {
	raw := annotations[artifact.StagesAnnotation]
	if raw == "" {
		return nil
	}
	var stages map[string]string
	if err := json.Unmarshal([]byte(raw), &stages); err != nil || len(stages) == 0 {
		// A malformed annotation costs the section, not the page. It is
		// descriptive metadata and a conforming client ignores it (2.2).
		return nil
	}

	byStage := map[string][]string{}
	for file, stage := range stages {
		byStage[stage] = append(byStage[stage], file)
	}
	names := make([]string, 0, len(byStage))
	for name := range byStage {
		names = append(names, name)
	}
	sort.Strings(names)

	p := &Provenance{
		BaseName:        annotations[ocispec.AnnotationBaseImageName],
		BaseDigest:      annotations[ocispec.AnnotationBaseImageDigest],
		SkillfileDigest: annotations["dev.epos.skillfile.digest"],
	}
	for _, name := range names {
		files := byStage[name]
		sort.Strings(files)
		p.Stages = append(p.Stages, Stage{Name: name, Files: files})
	}
	return p
}

// sortVersions orders tags newest first.
//
// Dotted numeric segments compare numerically, so 1.10.0 is newer than 1.2.0 —
// the failure a plain string sort produces, and one that would silently put the
// wrong version on every detail page. Anything that is not a run of dotted
// numbers falls back to reverse lexical order, which is at least deterministic.
func sortVersions(tags []string) {
	sort.Slice(tags, func(i, j int) bool { return compareVersions(tags[i], tags[j]) > 0 })
}

func compareVersions(a, b string) int {
	as, bs := versionFields(a), versionFields(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			if as[i] < bs[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(as) != len(bs):
		if len(as) < len(bs) {
			return -1
		}
		return 1
	case a == b:
		return 0
	case a < b:
		return -1
	default:
		return 1
	}
}

// versionFields reads the leading dotted-numeric run of a tag. A tag that is
// not numeric at all yields nothing, which makes every such tag compare equal
// on this axis and fall through to the lexical tie-break.
func versionFields(tag string) []int {
	tag = strings.TrimPrefix(tag, "v")
	var out []int
	for _, part := range strings.Split(tag, ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return out
		}
		out = append(out, n)
	}
	return out
}
