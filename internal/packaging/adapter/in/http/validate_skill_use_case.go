// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
	"net/http"
)

// ValidateSkillUseCaseHandler is a driving adapter that exposes the ValidateSkillUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type ValidateSkillUseCaseHandler struct {
	uc in.ValidateSkillUseCase
}

// NewValidateSkillUseCaseHandler constructs the handler with its driving port.
func NewValidateSkillUseCaseHandler(uc in.ValidateSkillUseCase) *ValidateSkillUseCaseHandler {
	return &ValidateSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ValidateSkillUseCase port.
func (v *ValidateSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
