// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
)

// CaptureDependencyPinInteractor implements the CaptureDependencyPin use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type CaptureDependencyPinInteractor struct{}

var _ in.CaptureDependencyPinUseCase = (*CaptureDependencyPinInteractor)(nil)

// NewCaptureDependencyPinInteractor constructs the interactor. Inject driven ports here.
func NewCaptureDependencyPinInteractor() *CaptureDependencyPinInteractor {
	return &CaptureDependencyPinInteractor{}
}

func (c *CaptureDependencyPinInteractor) CaptureDependencyPin(input in.CaptureDependencyPinInput) (in.CaptureDependencyPinOutput, error) {
	return in.CaptureDependencyPinOutput{}, errors.New("not implemented")
}
