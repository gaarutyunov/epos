// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
	"net/http"
)

// ListSkillsUseCaseHandler is a driving adapter that exposes the ListSkillsUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type ListSkillsUseCaseHandler struct {
	uc in.ListSkillsUseCase
}

// NewListSkillsUseCaseHandler constructs the handler with its driving port.
func NewListSkillsUseCaseHandler(uc in.ListSkillsUseCase) *ListSkillsUseCaseHandler {
	return &ListSkillsUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ListSkillsUseCase port.
func (l *ListSkillsUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
