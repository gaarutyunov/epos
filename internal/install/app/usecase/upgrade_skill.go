// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// UpgradeSkillInteractor implements the UpgradeSkill use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type UpgradeSkillInteractor struct{}

var _ in.UpgradeSkillUseCase = (*UpgradeSkillInteractor)(nil)

// NewUpgradeSkillInteractor constructs the interactor. Inject driven ports here.
func NewUpgradeSkillInteractor() *UpgradeSkillInteractor {
	return &UpgradeSkillInteractor{}
}

func (u *UpgradeSkillInteractor) UpgradeSkill(input in.UpgradeSkillInput) (in.UpgradeSkillOutput, error) {
	return in.UpgradeSkillOutput{}, errors.New("not implemented")
}
