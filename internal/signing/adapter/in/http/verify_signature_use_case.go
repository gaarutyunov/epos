// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/signing/app/port/in"
)

// VerifySignatureUseCaseHandler is a driving adapter that exposes the VerifySignatureUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type VerifySignatureUseCaseHandler struct {
	uc in.VerifySignatureUseCase
}

// NewVerifySignatureUseCaseHandler constructs the handler with its driving port.
func NewVerifySignatureUseCaseHandler(uc in.VerifySignatureUseCase) *VerifySignatureUseCaseHandler {
	return &VerifySignatureUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the VerifySignatureUseCase port.
func (h *VerifySignatureUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.VerifySignatureInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.VerifySignature(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
