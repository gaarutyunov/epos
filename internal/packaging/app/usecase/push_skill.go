// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
)

// PushSkillInteractor implements the PushSkill use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type PushSkillInteractor struct{}

var _ in.PushSkillUseCase = (*PushSkillInteractor)(nil)

// NewPushSkillInteractor constructs the interactor. Inject driven ports here.
func NewPushSkillInteractor() *PushSkillInteractor {
	return &PushSkillInteractor{}
}

func (p *PushSkillInteractor) PushSkill(input in.PushSkillInput) (in.PushSkillOutput, error) {
	return in.PushSkillOutput{}, errors.New("not implemented")
}
