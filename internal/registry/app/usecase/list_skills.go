// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
)

// ListSkillsInteractor implements the ListSkills use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type ListSkillsInteractor struct{}

var _ in.ListSkillsUseCase = (*ListSkillsInteractor)(nil)

// NewListSkillsInteractor constructs the interactor. Inject driven ports here.
func NewListSkillsInteractor() *ListSkillsInteractor {
	return &ListSkillsInteractor{}
}

func (l *ListSkillsInteractor) ListSkills(input in.ListSkillsInput) (in.ListSkillsOutput, error) {
	return in.ListSkillsOutput{}, errors.New("not implemented")
}
