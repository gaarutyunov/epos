// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
	"net/http"
)

// PackageSkillUseCaseHandler is a driving adapter that exposes the PackageSkillUseCase port over
// HTTP. This scaffold is written once; wire your router and decode requests here.
type PackageSkillUseCaseHandler struct {
	uc in.PackageSkillUseCase
}

// NewPackageSkillUseCaseHandler constructs the handler with its driving port.
func NewPackageSkillUseCaseHandler(uc in.PackageSkillUseCase) *PackageSkillUseCaseHandler {
	return &PackageSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the PackageSkillUseCase port.
func (p *PackageSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "not implemented", http.StatusNotImplemented)
}
