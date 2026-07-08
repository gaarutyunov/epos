// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
	"github.com/gaarutyunov/epos/internal/composition/app/port/out"
)

// CaptureDependencyPinInteractor implements the CaptureDependencyPin use case via
// the LayerSource driven port (SPEC §9.7).
type CaptureDependencyPinInteractor struct {
	source out.LayerSource
}

var _ in.CaptureDependencyPinUseCase = (*CaptureDependencyPinInteractor)(nil)

// NewCaptureDependencyPinInteractor injects the LayerSource driven port.
func NewCaptureDependencyPinInteractor(source out.LayerSource) *CaptureDependencyPinInteractor {
	return &CaptureDependencyPinInteractor{source: source}
}

func (c *CaptureDependencyPinInteractor) CaptureDependencyPin(input in.CaptureDependencyPinInput) (in.CaptureDependencyPinOutput, error) {
	pin, err := c.source.LayerSource(input.Layer)
	return in.CaptureDependencyPinOutput{Pin: pin}, err
}
