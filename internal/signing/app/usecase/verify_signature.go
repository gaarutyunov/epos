// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/signing/app/port/in"
	"github.com/gaarutyunov/epos/internal/signing/app/port/out"
)

// VerifySignatureInteractor implements the VerifySignature use case: it verifies
// a subject's cosign signatures through the SignaturePort driven port (SPEC §7).
type VerifySignatureInteractor struct {
	port out.SignaturePort
}

var _ in.VerifySignatureUseCase = (*VerifySignatureInteractor)(nil)

// NewVerifySignatureInteractor injects the SignaturePort driven port.
func NewVerifySignatureInteractor(port out.SignaturePort) *VerifySignatureInteractor {
	return &VerifySignatureInteractor{port: port}
}

func (v *VerifySignatureInteractor) VerifySignature(input in.VerifySignatureInput) (in.VerifySignatureOutput, error) {
	res, err := v.port.Signature(input.Request)
	return in.VerifySignatureOutput{Result: res}, err
}
