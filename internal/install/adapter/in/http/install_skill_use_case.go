// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// InstallSkillUseCaseHandler is a driving adapter that exposes the InstallSkillUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type InstallSkillUseCaseHandler struct {
	uc in.InstallSkillUseCase
}

// NewInstallSkillUseCaseHandler constructs the handler with its driving port.
func NewInstallSkillUseCaseHandler(uc in.InstallSkillUseCase) *InstallSkillUseCaseHandler {
	return &InstallSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the InstallSkillUseCase port.
func (h *InstallSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.InstallSkillInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.InstallSkill(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
