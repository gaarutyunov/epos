// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/stats/app/port/in"
	"net/http"
)

// RecordPullUseCaseHandler is a driving adapter that exposes the RecordPullUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type RecordPullUseCaseHandler struct {
	uc in.RecordPullUseCase
}

// NewRecordPullUseCaseHandler constructs the handler with its driving port.
func NewRecordPullUseCaseHandler(uc in.RecordPullUseCase) *RecordPullUseCaseHandler {
	return &RecordPullUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the RecordPullUseCase port.
func (h *RecordPullUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
