// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// UninstallSkillInteractor implements the UninstallSkill use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type UninstallSkillInteractor struct{}

var _ in.UninstallSkillUseCase = (*UninstallSkillInteractor)(nil)

// NewUninstallSkillInteractor constructs the interactor. Inject driven ports here.
func NewUninstallSkillInteractor() *UninstallSkillInteractor {
	return &UninstallSkillInteractor{}
}

func (u *UninstallSkillInteractor) UninstallSkill(input in.UninstallSkillInput) (in.UninstallSkillOutput, error) {
	return in.UninstallSkillOutput{}, errors.New("not implemented")
}
