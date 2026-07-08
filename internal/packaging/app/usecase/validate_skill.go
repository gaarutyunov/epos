// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
	"github.com/gaarutyunov/epos/internal/packaging/app/port/out"
)

// ValidateSkillInteractor implements the ValidateSkill use case via the
// ValidationPort driven port (SPEC §2.2, §3.5).
type ValidateSkillInteractor struct {
	port out.ValidationPort
}

var _ in.ValidateSkillUseCase = (*ValidateSkillInteractor)(nil)

// NewValidateSkillInteractor injects the ValidationPort driven port.
func NewValidateSkillInteractor(port out.ValidationPort) *ValidateSkillInteractor {
	return &ValidateSkillInteractor{port: port}
}

func (v *ValidateSkillInteractor) ValidateSkill(input in.ValidateSkillInput) (in.ValidateSkillOutput, error) {
	report, err := v.port.Validation(input.Artifact)
	return in.ValidateSkillOutput{Report: report}, err
}
