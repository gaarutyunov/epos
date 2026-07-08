// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
	"github.com/gaarutyunov/epos/internal/composition/app/port/out"
)

// ComposeStackInteractor implements the ComposeStack use case via the
// CompositionPort driven port (SPEC §9).
type ComposeStackInteractor struct {
	port out.CompositionPort
}

var _ in.ComposeStackUseCase = (*ComposeStackInteractor)(nil)

// NewComposeStackInteractor injects the CompositionPort driven port.
func NewComposeStackInteractor(port out.CompositionPort) *ComposeStackInteractor {
	return &ComposeStackInteractor{port: port}
}

func (c *ComposeStackInteractor) ComposeStack(input in.ComposeStackInput) (in.ComposeStackOutput, error) {
	merged, err := c.port.Composition(input.Request)
	return in.ComposeStackOutput{Merged: merged}, err
}
