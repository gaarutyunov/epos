// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
	"net/http"
)

// PushSkillUseCaseHandler is a driving adapter that exposes the PushSkillUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type PushSkillUseCaseHandler struct {
	uc in.PushSkillUseCase
}

// NewPushSkillUseCaseHandler constructs the handler with its driving port.
func NewPushSkillUseCaseHandler(uc in.PushSkillUseCase) *PushSkillUseCaseHandler {
	return &PushSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the PushSkillUseCase port.
func (p *PushSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
