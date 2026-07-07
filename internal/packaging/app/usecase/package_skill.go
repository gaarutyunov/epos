// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
)

// PackageSkillInteractor implements the PackageSkill use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type PackageSkillInteractor struct{}

var _ in.PackageSkillUseCase = (*PackageSkillInteractor)(nil)

// NewPackageSkillInteractor constructs the interactor. Inject driven ports here.
func NewPackageSkillInteractor() *PackageSkillInteractor {
	return &PackageSkillInteractor{}
}

func (p *PackageSkillInteractor) PackageSkill(input in.PackageSkillInput) (in.PackageSkillOutput, error) {
	return in.PackageSkillOutput{}, errors.New("not implemented")
}
