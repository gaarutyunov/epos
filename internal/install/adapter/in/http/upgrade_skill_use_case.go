// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// UpgradeSkillUseCaseHandler is a driving adapter that exposes the UpgradeSkillUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type UpgradeSkillUseCaseHandler struct {
	uc in.UpgradeSkillUseCase
}

// NewUpgradeSkillUseCaseHandler constructs the handler with its driving port.
func NewUpgradeSkillUseCaseHandler(uc in.UpgradeSkillUseCase) *UpgradeSkillUseCaseHandler {
	return &UpgradeSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the UpgradeSkillUseCase port.
func (h *UpgradeSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.UpgradeSkillInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.UpgradeSkill(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
