// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"github.com/gaarutyunov/epos/internal/install/app/port/out"
)

// UninstallSkillInteractor implements the UninstallSkill use case: remove the
// materialized files/ConfigMaps and the revision history (SPEC §4.2).
type UninstallSkillInteractor struct {
	mat       out.Materializer
	store     out.RevisionRepository
	target    string
	namespace string
}

var _ in.UninstallSkillUseCase = (*UninstallSkillInteractor)(nil)

// NewUninstallSkillInteractor injects the ports and target context.
func NewUninstallSkillInteractor(mat out.Materializer, store out.RevisionRepository, target, namespace string) *UninstallSkillInteractor {
	if target == "" {
		target = "files"
	}
	return &UninstallSkillInteractor{mat: mat, store: store, target: target, namespace: namespace}
}

func (u *UninstallSkillInteractor) UninstallSkill(input in.UninstallSkillInput) (in.UninstallSkillOutput, error) {
	if err := u.mat.Remove(input.ReleaseName, u.target, u.namespace); err != nil {
		return in.UninstallSkillOutput{Ok: false}, err
	}
	if err := u.store.Delete(input.ReleaseName, u.target, u.namespace); err != nil {
		return in.UninstallSkillOutput{Ok: false}, err
	}
	return in.UninstallSkillOutput{Ok: true}, nil
}
