// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// UninstallSkillUseCaseHandler is a driving adapter that exposes the UninstallSkillUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type UninstallSkillUseCaseHandler struct {
	uc in.UninstallSkillUseCase
}

// NewUninstallSkillUseCaseHandler constructs the handler with its driving port.
func NewUninstallSkillUseCaseHandler(uc in.UninstallSkillUseCase) *UninstallSkillUseCaseHandler {
	return &UninstallSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the UninstallSkillUseCase port.
func (h *UninstallSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.UninstallSkillInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.UninstallSkill(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
