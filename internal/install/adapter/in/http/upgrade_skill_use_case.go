// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"net/http"
)

// UpgradeSkillUseCaseHandler is a driving adapter that exposes the UpgradeSkillUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type UpgradeSkillUseCaseHandler struct {
	uc in.UpgradeSkillUseCase
}

// NewUpgradeSkillUseCaseHandler constructs the handler with its driving port.
func NewUpgradeSkillUseCaseHandler(uc in.UpgradeSkillUseCase) *UpgradeSkillUseCaseHandler {
	return &UpgradeSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the UpgradeSkillUseCase port.
func (u *UpgradeSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
