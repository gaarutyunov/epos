// Code scaffolded by sysgo; edit freely (not regenerated).

package http

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
)

// ProxyManifestUseCaseHandler is a driving adapter that exposes the ProxyManifestUseCase port over HTTP:
// it decodes the request into the input DTO, invokes the use case, and encodes
// the output DTO as JSON.
type ProxyManifestUseCaseHandler struct {
	uc in.ProxyManifestUseCase
}

// NewProxyManifestUseCaseHandler constructs the handler with its driving port.
func NewProxyManifestUseCaseHandler(uc in.ProxyManifestUseCase) *ProxyManifestUseCaseHandler {
	return &ProxyManifestUseCaseHandler{uc: uc}
}

// ServeHTTP handles an inbound request for the ProxyManifestUseCase port.
func (h *ProxyManifestUseCaseHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var input in.ProxyManifestInput
	if r.Body != nil {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil && err != io.EOF {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	output, err := h.uc.ProxyManifest(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(output)
}
