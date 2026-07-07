// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"net/http"
)

// UninstallSkillUseCaseHandler is a driving adapter that exposes the UninstallSkillUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type UninstallSkillUseCaseHandler struct {
	uc in.UninstallSkillUseCase
}

// NewUninstallSkillUseCaseHandler constructs the handler with its driving port.
func NewUninstallSkillUseCaseHandler(uc in.UninstallSkillUseCase) *UninstallSkillUseCaseHandler {
	return &UninstallSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the UninstallSkillUseCase port.
func (u *UninstallSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
