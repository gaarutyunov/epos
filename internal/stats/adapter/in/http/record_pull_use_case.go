// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/stats/app/port/in"
)

// RecordPullUseCaseHandler is a driving adapter that exposes the RecordPullUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type RecordPullUseCaseHandler struct {
	uc in.RecordPullUseCase
}

// NewRecordPullUseCaseHandler constructs the handler with its driving port.
func NewRecordPullUseCaseHandler(uc in.RecordPullUseCase) *RecordPullUseCaseHandler {
	return &RecordPullUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the RecordPullUseCase port.
func (h *RecordPullUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.RecordPullInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.RecordPull(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
