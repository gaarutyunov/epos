// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
)

// CaptureDependencyPinUseCaseHandler is a driving adapter that exposes the CaptureDependencyPinUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type CaptureDependencyPinUseCaseHandler struct {
	uc in.CaptureDependencyPinUseCase
}

// NewCaptureDependencyPinUseCaseHandler constructs the handler with its driving port.
func NewCaptureDependencyPinUseCaseHandler(uc in.CaptureDependencyPinUseCase) *CaptureDependencyPinUseCaseHandler {
	return &CaptureDependencyPinUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the CaptureDependencyPinUseCase port.
func (h *CaptureDependencyPinUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.CaptureDependencyPinInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.CaptureDependencyPin(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
