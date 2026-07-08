// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
)

// ListSkillsUseCaseHandler is a driving adapter that exposes the ListSkillsUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type ListSkillsUseCaseHandler struct {
	uc in.ListSkillsUseCase
}

// NewListSkillsUseCaseHandler constructs the handler with its driving port.
func NewListSkillsUseCaseHandler(uc in.ListSkillsUseCase) *ListSkillsUseCaseHandler {
	return &ListSkillsUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ListSkillsUseCase port.
func (h *ListSkillsUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.ListSkillsInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.ListSkills(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
