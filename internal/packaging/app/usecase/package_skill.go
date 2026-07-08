// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
	"github.com/gaarutyunov/epos/internal/packaging/app/port/out"
)

// PackageSkillInteractor implements the PackageSkill use case via the
// PackagingPort driven port (SPEC §2.3, §4.1).
type PackageSkillInteractor struct {
	port out.PackagingPort
}

var _ in.PackageSkillUseCase = (*PackageSkillInteractor)(nil)

// NewPackageSkillInteractor injects the PackagingPort driven port.
func NewPackageSkillInteractor(port out.PackagingPort) *PackageSkillInteractor {
	return &PackageSkillInteractor{port: port}
}

func (p *PackageSkillInteractor) PackageSkill(input in.PackageSkillInput) (in.PackageSkillOutput, error) {
	art, err := p.port.Packaging(input.Request)
	return in.PackageSkillOutput{Artifact: art}, err
}
