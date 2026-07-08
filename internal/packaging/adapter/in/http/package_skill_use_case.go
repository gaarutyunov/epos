// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
)

// PackageSkillUseCaseHandler is a driving adapter that exposes the PackageSkillUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type PackageSkillUseCaseHandler struct {
	uc in.PackageSkillUseCase
}

// NewPackageSkillUseCaseHandler constructs the handler with its driving port.
func NewPackageSkillUseCaseHandler(uc in.PackageSkillUseCase) *PackageSkillUseCaseHandler {
	return &PackageSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the PackageSkillUseCase port.
func (h *PackageSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.PackageSkillInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.PackageSkill(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
