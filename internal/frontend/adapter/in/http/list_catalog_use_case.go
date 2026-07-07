// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/frontend/app/port/in"
	"net/http"
)

// ListCatalogUseCaseHandler is a driving adapter that exposes the ListCatalogUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type ListCatalogUseCaseHandler struct {
	uc in.ListCatalogUseCase
}

// NewListCatalogUseCaseHandler constructs the handler with its driving port.
func NewListCatalogUseCaseHandler(uc in.ListCatalogUseCase) *ListCatalogUseCaseHandler {
	return &ListCatalogUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ListCatalogUseCase port.
func (l *ListCatalogUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
