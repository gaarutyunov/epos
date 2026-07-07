// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
	"net/http"
)

// VerifyPinUseCaseHandler is a driving adapter that exposes the VerifyPinUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type VerifyPinUseCaseHandler struct {
	uc in.VerifyPinUseCase
}

// NewVerifyPinUseCaseHandler constructs the handler with its driving port.
func NewVerifyPinUseCaseHandler(uc in.VerifyPinUseCase) *VerifyPinUseCaseHandler {
	return &VerifyPinUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the VerifyPinUseCase port.
func (v *VerifyPinUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
