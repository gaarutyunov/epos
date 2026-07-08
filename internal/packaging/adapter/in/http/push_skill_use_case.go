// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
)

// PushSkillUseCaseHandler is a driving adapter that exposes the PushSkillUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type PushSkillUseCaseHandler struct {
	uc in.PushSkillUseCase
}

// NewPushSkillUseCaseHandler constructs the handler with its driving port.
func NewPushSkillUseCaseHandler(uc in.PushSkillUseCase) *PushSkillUseCaseHandler {
	return &PushSkillUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the PushSkillUseCase port.
func (h *PushSkillUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.PushSkillInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.PushSkill(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
