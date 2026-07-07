// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/signing/app/port/in"
	"net/http"
)

// VerifySignatureUseCaseHandler is a driving adapter that exposes the VerifySignatureUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type VerifySignatureUseCaseHandler struct {
	uc in.VerifySignatureUseCase
}

// NewVerifySignatureUseCaseHandler constructs the handler with its driving port.
func NewVerifySignatureUseCaseHandler(uc in.VerifySignatureUseCase) *VerifySignatureUseCaseHandler {
	return &VerifySignatureUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the VerifySignatureUseCase port.
func (v *VerifySignatureUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
