// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/stats/app/port/in"
)

// ReadStatisticsUseCaseHandler is a driving adapter that exposes the ReadStatisticsUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type ReadStatisticsUseCaseHandler struct {
	uc in.ReadStatisticsUseCase
}

// NewReadStatisticsUseCaseHandler constructs the handler with its driving port.
func NewReadStatisticsUseCaseHandler(uc in.ReadStatisticsUseCase) *ReadStatisticsUseCaseHandler {
	return &ReadStatisticsUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ReadStatisticsUseCase port.
func (h *ReadStatisticsUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.ReadStatisticsInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.ReadStatistics(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
