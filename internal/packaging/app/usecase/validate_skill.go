// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
)

// ValidateSkillInteractor implements the ValidateSkill use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type ValidateSkillInteractor struct{}

var _ in.ValidateSkillUseCase = (*ValidateSkillInteractor)(nil)

// NewValidateSkillInteractor constructs the interactor. Inject driven ports here.
func NewValidateSkillInteractor() *ValidateSkillInteractor {
	return &ValidateSkillInteractor{}
}

func (v *ValidateSkillInteractor) ValidateSkill(input in.ValidateSkillInput) (in.ValidateSkillOutput, error) {
	return in.ValidateSkillOutput{}, errors.New("not implemented")
}
