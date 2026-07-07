// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/signing/app/port/in"
)

// VerifySignatureInteractor implements the VerifySignature use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type VerifySignatureInteractor struct{}

var _ in.VerifySignatureUseCase = (*VerifySignatureInteractor)(nil)

// NewVerifySignatureInteractor constructs the interactor. Inject driven ports here.
func NewVerifySignatureInteractor() *VerifySignatureInteractor {
	return &VerifySignatureInteractor{}
}

func (v *VerifySignatureInteractor) VerifySignature(input in.VerifySignatureInput) (in.VerifySignatureOutput, error) {
	return in.VerifySignatureOutput{}, errors.New("not implemented")
}
