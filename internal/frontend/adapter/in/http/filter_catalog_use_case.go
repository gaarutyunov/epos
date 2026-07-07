// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/frontend/app/port/in"
	"net/http"
)

// FilterCatalogUseCaseHandler is a driving adapter that exposes the FilterCatalogUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type FilterCatalogUseCaseHandler struct {
	uc in.FilterCatalogUseCase
}

// NewFilterCatalogUseCaseHandler constructs the handler with its driving port.
func NewFilterCatalogUseCaseHandler(uc in.FilterCatalogUseCase) *FilterCatalogUseCaseHandler {
	return &FilterCatalogUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the FilterCatalogUseCase port.
func (f *FilterCatalogUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
