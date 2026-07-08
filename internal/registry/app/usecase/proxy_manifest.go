// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
)

// ProxyManifestInteractor implements the ProxyManifest use case via the ProxyPort
// driven port (transparent pass-through + stats, SPEC §6).
type ProxyManifestInteractor struct {
	proxy out.ProxyPort
}

var _ in.ProxyManifestUseCase = (*ProxyManifestInteractor)(nil)

// NewProxyManifestInteractor injects the ProxyPort driven port.
func NewProxyManifestInteractor(proxy out.ProxyPort) *ProxyManifestInteractor {
	return &ProxyManifestInteractor{proxy: proxy}
}

func (p *ProxyManifestInteractor) ProxyManifest(input in.ProxyManifestInput) (in.ProxyManifestOutput, error) {
	resp, err := p.proxy.Proxy(input.Request)
	return in.ProxyManifestOutput{Response: resp}, err
}
