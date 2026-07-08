// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
)

// RegisterRegistryUseCaseHandler is a driving adapter that exposes the RegisterRegistryUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type RegisterRegistryUseCaseHandler struct {
	uc in.RegisterRegistryUseCase
}

// NewRegisterRegistryUseCaseHandler constructs the handler with its driving port.
func NewRegisterRegistryUseCaseHandler(uc in.RegisterRegistryUseCase) *RegisterRegistryUseCaseHandler {
	return &RegisterRegistryUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the RegisterRegistryUseCase port.
func (h *RegisterRegistryUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.RegisterRegistryInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.RegisterRegistry(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
