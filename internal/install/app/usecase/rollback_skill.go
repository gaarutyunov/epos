// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
)

// RollbackSkillInteractor implements the RollbackSkill use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type RollbackSkillInteractor struct{}

var _ in.RollbackSkillUseCase = (*RollbackSkillInteractor)(nil)

// NewRollbackSkillInteractor constructs the interactor. Inject driven ports here.
func NewRollbackSkillInteractor() *RollbackSkillInteractor {
	return &RollbackSkillInteractor{}
}

func (r *RollbackSkillInteractor) RollbackSkill(input in.RollbackSkillInput) (in.RollbackSkillOutput, error) {
	return in.RollbackSkillOutput{}, errors.New("not implemented")
}
