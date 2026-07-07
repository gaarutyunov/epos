// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
	"net/http"
)

// RegisterRegistryUseCaseHandler is a driving adapter that exposes the RegisterRegistryUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type RegisterRegistryUseCaseHandler struct {
	uc in.RegisterRegistryUseCase
}

// NewRegisterRegistryUseCaseHandler constructs the handler with its driving port.
func NewRegisterRegistryUseCaseHandler(uc in.RegisterRegistryUseCase) *RegisterRegistryUseCaseHandler {
	return &RegisterRegistryUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the RegisterRegistryUseCase port.
func (h *RegisterRegistryUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
