// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/frontend/app/port/in"
)

// ListCatalogUseCaseHandler is a driving adapter that exposes the ListCatalogUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type ListCatalogUseCaseHandler struct {
	uc in.ListCatalogUseCase
}

// NewListCatalogUseCaseHandler constructs the handler with its driving port.
func NewListCatalogUseCaseHandler(uc in.ListCatalogUseCase) *ListCatalogUseCaseHandler {
	return &ListCatalogUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ListCatalogUseCase port.
func (h *ListCatalogUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.ListCatalogInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.ListCatalog(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
