// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
	"github.com/gaarutyunov/epos/internal/registry/domain"
)

// ListSkillsInteractor implements the ListSkills use case: it enumerates skills
// across the registered registries using the read-only listing credential, via
// the CatalogProbe driven port (SPEC §8.1).
type ListSkillsInteractor struct {
	probe   out.CatalogProbe
	entries []domain.RegistryEntry
}

var _ in.ListSkillsUseCase = (*ListSkillsInteractor)(nil)

// NewListSkillsInteractor injects the CatalogProbe driven port and the set of
// registries to enumerate.
func NewListSkillsInteractor(probe out.CatalogProbe, entries []domain.RegistryEntry) *ListSkillsInteractor {
	return &ListSkillsInteractor{probe: probe, entries: entries}
}

func (l *ListSkillsInteractor) ListSkills(input in.ListSkillsInput) (in.ListSkillsOutput, error) {
	result := domain.CatalogResult{Mode: domain.DiscoveryMode{Value: "registered"}}
	for _, entry := range l.entries {
		res, err := l.probe.CatalogProbe(entry)
		if err != nil {
			continue
		}
		result.Repos = append(result.Repos, res.Repos...)
	}
	return in.ListSkillsOutput{Result: result}, nil
}
