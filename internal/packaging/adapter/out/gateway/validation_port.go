// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"github.com/gaarutyunov/epos/internal/packaging/app/port/out"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

// ValidationPortImpl is the driven adapter implementing the ValidationPort port:
// strict Agent-Skills metadata validation (SPEC §2.2).
type ValidationPortImpl struct{}

var _ out.ValidationPort = (*ValidationPortImpl)(nil)

// NewValidationPortImpl constructs the validator.
func NewValidationPortImpl() *ValidationPortImpl { return &ValidationPortImpl{} }

// Validation validates an artifact's metadata, returning the report.
func (v *ValidationPortImpl) Validation(artifact domain.SkillArtifact) (domain.ValidationReport, error) {
	m := &domain.Manifest{
		Name:        artifact.Metadata.Name,
		Version:     artifact.Metadata.Version.Raw,
		Description: artifact.Metadata.Description,
	}
	msgs := domain.ValidateManifest(m, artifact.Metadata.Name)
	return domain.ValidationReport{Ok: len(msgs) == 0, Messages: msgs}, nil
}
