// Code scaffolded by sysgo; edit freely (not regenerated).

package repository

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/signing/app/port/out"
	"github.com/gaarutyunov/epos/internal/signing/domain"
)

// SignaturePortImpl is a driven adapter implementing the SignaturePort port.
// This scaffold is written once; implement the persistence logic here.
type SignaturePortImpl struct{}

var _ out.SignaturePort = (*SignaturePortImpl)(nil)

// NewSignaturePortImpl constructs the adapter. Inject your DB handle here.
func NewSignaturePortImpl() *SignaturePortImpl {
	return &SignaturePortImpl{}
}

func (s *SignaturePortImpl) Signature(request domain.VerifyRequest) (domain.VerifyResult, error) {
	return domain.VerifyResult{}, errors.New("not implemented")
}
