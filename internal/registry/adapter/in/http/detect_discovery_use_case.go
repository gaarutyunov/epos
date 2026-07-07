// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
	"net/http"
)

// DetectDiscoveryUseCaseHandler is a driving adapter that exposes the DetectDiscoveryUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type DetectDiscoveryUseCaseHandler struct {
	uc in.DetectDiscoveryUseCase
}

// NewDetectDiscoveryUseCaseHandler constructs the handler with its driving port.
func NewDetectDiscoveryUseCaseHandler(uc in.DetectDiscoveryUseCase) *DetectDiscoveryUseCaseHandler {
	return &DetectDiscoveryUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the DetectDiscoveryUseCase port.
func (d *DetectDiscoveryUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
