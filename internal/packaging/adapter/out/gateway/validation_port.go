// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/packaging/app/port/out"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

// ValidationPortImpl is a driven adapter implementing the ValidationPort gateway port.
// This scaffold is written once; implement the external-system calls here.
type ValidationPortImpl struct{}

var _ out.ValidationPort = (*ValidationPortImpl)(nil)

// NewValidationPortImpl constructs the gateway adapter. Inject your client here.
func NewValidationPortImpl() *ValidationPortImpl {
	return &ValidationPortImpl{}
}

func (v *ValidationPortImpl) Validation(artifact domain.SkillArtifact) (domain.ValidationReport, error) {
	return domain.ValidationReport{}, errors.New("not implemented")
}
