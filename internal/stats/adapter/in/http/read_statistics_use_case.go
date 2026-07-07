// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/stats/app/port/in"
	"net/http"
)

// ReadStatisticsUseCaseHandler is a driving adapter that exposes the ReadStatisticsUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type ReadStatisticsUseCaseHandler struct {
	uc in.ReadStatisticsUseCase
}

// NewReadStatisticsUseCaseHandler constructs the handler with its driving port.
func NewReadStatisticsUseCaseHandler(uc in.ReadStatisticsUseCase) *ReadStatisticsUseCaseHandler {
	return &ReadStatisticsUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ReadStatisticsUseCase port.
func (h *ReadStatisticsUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
