// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"net/http"
)

// ReadHistoryUseCaseHandler is a driving adapter that exposes the ReadHistoryUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type ReadHistoryUseCaseHandler struct {
	uc in.ReadHistoryUseCase
}

// NewReadHistoryUseCaseHandler constructs the handler with its driving port.
func NewReadHistoryUseCaseHandler(uc in.ReadHistoryUseCase) *ReadHistoryUseCaseHandler {
	return &ReadHistoryUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ReadHistoryUseCase port.
func (h *ReadHistoryUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
