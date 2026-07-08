// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/frontend/app/port/in"
)

// FilterCatalogUseCaseHandler is a driving adapter that exposes the FilterCatalogUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type FilterCatalogUseCaseHandler struct {
	uc in.FilterCatalogUseCase
}

// NewFilterCatalogUseCaseHandler constructs the handler with its driving port.
func NewFilterCatalogUseCaseHandler(uc in.FilterCatalogUseCase) *FilterCatalogUseCaseHandler {
	return &FilterCatalogUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the FilterCatalogUseCase port.
func (h *FilterCatalogUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.FilterCatalogInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.FilterCatalog(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
