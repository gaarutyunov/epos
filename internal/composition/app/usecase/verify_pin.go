// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
)

// VerifyPinInteractor implements the VerifyPin use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type VerifyPinInteractor struct{}

var _ in.VerifyPinUseCase = (*VerifyPinInteractor)(nil)

// NewVerifyPinInteractor constructs the interactor. Inject driven ports here.
func NewVerifyPinInteractor() *VerifyPinInteractor {
	return &VerifyPinInteractor{}
}

func (v *VerifyPinInteractor) VerifyPin(input in.VerifyPinInput) (in.VerifyPinOutput, error) {
	return in.VerifyPinOutput{}, errors.New("not implemented")
}
