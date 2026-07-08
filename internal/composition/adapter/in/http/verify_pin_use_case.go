// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
)

// VerifyPinUseCaseHandler is a driving adapter that exposes the VerifyPinUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type VerifyPinUseCaseHandler struct {
	uc in.VerifyPinUseCase
}

// NewVerifyPinUseCaseHandler constructs the handler with its driving port.
func NewVerifyPinUseCaseHandler(uc in.VerifyPinUseCase) *VerifyPinUseCaseHandler {
	return &VerifyPinUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the VerifyPinUseCase port.
func (h *VerifyPinUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.VerifyPinInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.VerifyPin(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
