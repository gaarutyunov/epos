// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
)

// ComposeStackUseCaseHandler is a driving adapter that exposes the ComposeStackUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type ComposeStackUseCaseHandler struct {
	uc in.ComposeStackUseCase
}

// NewComposeStackUseCaseHandler constructs the handler with its driving port.
func NewComposeStackUseCaseHandler(uc in.ComposeStackUseCase) *ComposeStackUseCaseHandler {
	return &ComposeStackUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ComposeStackUseCase port.
func (h *ComposeStackUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.ComposeStackInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.ComposeStack(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
