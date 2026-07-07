// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"net/http"
)

// InstallSkillUseCaseHandler is a driving adapter that exposes the InstallSkillUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type InstallSkillUseCaseHandler struct {
	uc in.InstallSkillUseCase
}

// NewInstallSkillUseCaseHandler constructs the handler with its driving port.
func NewInstallSkillUseCaseHandler(uc in.InstallSkillUseCase) *InstallSkillUseCaseHandler {
	return &InstallSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the InstallSkillUseCase port.
func (i *InstallSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
