// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// ReadHistoryUseCaseHandler is a driving adapter that exposes the ReadHistoryUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type ReadHistoryUseCaseHandler struct {
	uc in.ReadHistoryUseCase
}

// NewReadHistoryUseCaseHandler constructs the handler with its driving port.
func NewReadHistoryUseCaseHandler(uc in.ReadHistoryUseCase) *ReadHistoryUseCaseHandler {
	return &ReadHistoryUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ReadHistoryUseCase port.
func (h *ReadHistoryUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.ReadHistoryInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.ReadHistory(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
