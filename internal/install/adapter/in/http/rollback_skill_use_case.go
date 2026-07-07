// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"net/http"
)

// RollbackSkillUseCaseHandler is a driving adapter that exposes the RollbackSkillUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type RollbackSkillUseCaseHandler struct {
	uc in.RollbackSkillUseCase
}

// NewRollbackSkillUseCaseHandler constructs the handler with its driving port.
func NewRollbackSkillUseCaseHandler(uc in.RollbackSkillUseCase) *RollbackSkillUseCaseHandler {
	return &RollbackSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the RollbackSkillUseCase port.
func (h *RollbackSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
