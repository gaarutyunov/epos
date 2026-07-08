// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"github.com/gaarutyunov/epos/internal/composition/app/port/out"
	"github.com/gaarutyunov/epos/internal/composition/domain"
)

// CompositionPortImpl is the driven adapter implementing the CompositionPort:
// it resolves an ordered layer stack into one merged skill via the composition
// engine (later-overrides-earlier, operation-merge for SKILL.md, SPEC §9.5).
type CompositionPortImpl struct {
	stack  []domain.StackLayer
	strict bool
	last   *domain.Merged
}

var _ out.CompositionPort = (*CompositionPortImpl)(nil)

// NewCompositionPortImpl binds the adapter to a resolved layer stack.
func NewCompositionPortImpl(stack []domain.StackLayer, strict bool) *CompositionPortImpl {
	return &CompositionPortImpl{stack: stack, strict: strict}
}

// Composition merges the stack and returns the merged-skill id + provenance.
func (c *CompositionPortImpl) Composition(request domain.ComposeRequest) (domain.MergedSkill, error) {
	merged, err := domain.Compose(c.stack, c.strict)
	if err != nil {
		return domain.MergedSkill{}, err
	}
	c.last = merged
	return domain.MergedSkill{SkillID: request.StackID, Provenance: merged.ProvenanceLines()}, nil
}

// Merged returns the full merged file set from the last composition (the file
// bytes are not carried in the coarse MergedSkill DTO).
func (c *CompositionPortImpl) Merged() *domain.Merged { return c.last }
