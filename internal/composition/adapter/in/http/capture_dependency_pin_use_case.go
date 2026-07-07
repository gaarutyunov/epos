// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
	"net/http"
)

// CaptureDependencyPinUseCaseHandler is a driving adapter that exposes the CaptureDependencyPinUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type CaptureDependencyPinUseCaseHandler struct {
	uc in.CaptureDependencyPinUseCase
}

// NewCaptureDependencyPinUseCaseHandler constructs the handler with its driving port.
func NewCaptureDependencyPinUseCaseHandler(uc in.CaptureDependencyPinUseCase) *CaptureDependencyPinUseCaseHandler {
	return &CaptureDependencyPinUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the CaptureDependencyPinUseCase port.
func (c *CaptureDependencyPinUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
