// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// RollbackSkillUseCaseHandler is a driving adapter that exposes the RollbackSkillUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type RollbackSkillUseCaseHandler struct {
	uc in.RollbackSkillUseCase
}

// NewRollbackSkillUseCaseHandler constructs the handler with its driving port.
func NewRollbackSkillUseCaseHandler(uc in.RollbackSkillUseCase) *RollbackSkillUseCaseHandler {
	return &RollbackSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the RollbackSkillUseCase port.
func (h *RollbackSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.RollbackSkillInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.RollbackSkill(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
