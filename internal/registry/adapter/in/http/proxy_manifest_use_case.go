// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
	"net/http"
)

// ProxyManifestUseCaseHandler is a driving adapter that exposes the ProxyManifestUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type ProxyManifestUseCaseHandler struct {
	uc in.ProxyManifestUseCase
}

// NewProxyManifestUseCaseHandler constructs the handler with its driving port.
func NewProxyManifestUseCaseHandler(uc in.ProxyManifestUseCase) *ProxyManifestUseCaseHandler {
	return &ProxyManifestUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ProxyManifestUseCase port.
func (p *ProxyManifestUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
