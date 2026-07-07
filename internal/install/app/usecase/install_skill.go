// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// InstallSkillInteractor implements the InstallSkill use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type InstallSkillInteractor struct{}

var _ in.InstallSkillUseCase = (*InstallSkillInteractor)(nil)

// NewInstallSkillInteractor constructs the interactor. Inject driven ports here.
func NewInstallSkillInteractor() *InstallSkillInteractor {
	return &InstallSkillInteractor{}
}

func (i *InstallSkillInteractor) InstallSkill(input in.InstallSkillInput) (in.InstallSkillOutput, error) {
	return in.InstallSkillOutput{}, errors.New("not implemented")
}
