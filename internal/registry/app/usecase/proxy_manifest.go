// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
)

// ProxyManifestInteractor implements the ProxyManifest use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type ProxyManifestInteractor struct{}

var _ in.ProxyManifestUseCase = (*ProxyManifestInteractor)(nil)

// NewProxyManifestInteractor constructs the interactor. Inject driven ports here.
func NewProxyManifestInteractor() *ProxyManifestInteractor {
	return &ProxyManifestInteractor{}
}

func (p *ProxyManifestInteractor) ProxyManifest(input in.ProxyManifestInput) (in.ProxyManifestOutput, error) {
	return in.ProxyManifestOutput{}, errors.New("not implemented")
}
