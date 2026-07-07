// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
	"net/http"
)

// ComposeStackUseCaseHandler is a driving adapter that exposes the ComposeStackUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type ComposeStackUseCaseHandler struct {
	uc in.ComposeStackUseCase
}

// NewComposeStackUseCaseHandler constructs the handler with its driving port.
func NewComposeStackUseCaseHandler(uc in.ComposeStackUseCase) *ComposeStackUseCaseHandler {
	return &ComposeStackUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ComposeStackUseCase port.
func (c *ComposeStackUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
