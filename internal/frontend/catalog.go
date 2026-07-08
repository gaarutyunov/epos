// Package frontend is the federated, filter-to-skills web listing (SPEC §12). It
// shares the proxy/discovery core, federates across registries, filters to skill
// packages via the media-type discriminator, and shows download stats when a
// per-skill backend is enabled.
package frontend

import (
	"context"
	"sort"
	"strings"

	"github.com/gaarutyunov/epos/internal/config"
	"github.com/gaarutyunov/epos/internal/infrastructure/oci"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
	"github.com/gaarutyunov/epos/internal/registry/discovery"
	"github.com/gaarutyunov/epos/internal/stats"
)

// SkillCard is one entry in the federated listing (SPEC §12.1).
type SkillCard struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Registry    string `json:"registry"`
	Downloads   int    `json:"downloads"`
}

// Filter narrows the listing by keyword and/or registry (SPEC §12.1).
type Filter struct {
	Keyword  string
	Registry string
}

// Catalog is the in-memory federated skill index (SPEC §12.2).
type Catalog struct {
	cards []SkillCard
}

// NewCatalog builds a catalog from a fixed set of cards (used by tests and by
// the periodic refresh).
func NewCatalog(cards []SkillCard) *Catalog { return &Catalog{cards: cards} }

// Cards returns all cards sorted by name.
func (c *Catalog) Cards() []SkillCard {
	out := append([]SkillCard(nil), c.cards...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Filter returns the cards matching the filter (SPEC §12.1). An empty filter
// returns everything.
func (c *Catalog) Filter(f Filter) []SkillCard {
	kw := strings.ToLower(f.Keyword)
	var out []SkillCard
	for _, card := range c.Cards() {
		if f.Registry != "" && card.Registry != f.Registry {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(card.Name), kw) &&
			!strings.Contains(strings.ToLower(card.Description), kw) {
			continue
		}
		out = append(out, card)
	}
	return out
}

// Feed gathers a federated catalog across registries using discovery + stats
// (the CatalogFeed driven port, SPEC §12.2).
type Feed struct {
	Registries []config.Registry
	Client     *oci.Client
	Stats      *stats.Counter
}

// Gather enumerates each registry's skills, reading metadata from the config
// blob and download counts from the stats backend, into a Catalog.
func (fd *Feed) Gather(ctx context.Context) (*Catalog, error) {
	d := &discovery.Discoverer{Client: fd.Client}
	var cards []SkillCard
	for _, reg := range fd.Registries {
		res, err := d.Discover(ctx, reg)
		if err != nil {
			continue
		}
		for _, repo := range res.Repos {
			card := fd.card(ctx, reg, repo)
			if card != nil {
				cards = append(cards, *card)
			}
		}
	}
	return NewCatalog(cards), nil
}

func (fd *Feed) card(ctx context.Context, reg config.Registry, repo string) *SkillCard {
	ref := trimScheme(reg.URL) + "/" + repo
	tags, err := fd.Client.Tags(ctx, ref)
	if err != nil || len(tags) == 0 {
		return nil
	}
	man, err := fd.Client.Pull(ctx, ref+":"+tags[len(tags)-1])
	if err != nil {
		return nil
	}
	if !domain.IsSkillConfigMediaType(man.Config.MediaType) && man.ArtifactType != domain.MediaTypeSkillConfig {
		return nil
	}
	meta, err := domain.ParseManifest(man.Config.Data)
	if err != nil {
		return nil
	}
	downloads := 0
	if fd.Stats != nil {
		downloads = fd.Stats.Skill(meta.Name)
	}
	return &SkillCard{
		Name:        meta.Name,
		Version:     meta.Version,
		Description: meta.Description,
		Registry:    reg.Name,
		Downloads:   downloads,
	}
}

func trimScheme(u string) string {
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	return strings.TrimRight(u, "/")
}
