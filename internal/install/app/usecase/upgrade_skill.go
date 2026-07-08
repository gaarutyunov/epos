// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"github.com/gaarutyunov/epos/internal/install/app/port/out"
)

// UpgradeSkillInteractor implements the UpgradeSkill use case: fetch a newer
// version, re-materialize, and append a new revision (no three-way merge, SPEC
// §4.2).
type UpgradeSkillInteractor struct {
	mat   out.Materializer
	store out.RevisionRepository
}

var _ in.UpgradeSkillUseCase = (*UpgradeSkillInteractor)(nil)

// NewUpgradeSkillInteractor injects the MaterializePort and RevisionStore ports.
func NewUpgradeSkillInteractor(mat out.Materializer, store out.RevisionRepository) *UpgradeSkillInteractor {
	return &UpgradeSkillInteractor{mat: mat, store: store}
}

func (u *UpgradeSkillInteractor) UpgradeSkill(input in.UpgradeSkillInput) (in.UpgradeSkillOutput, error) {
	req := input.Request
	res, err := u.mat.Materialize(req)
	if err != nil {
		return in.UpgradeSkillOutput{}, err
	}
	files := u.mat.LastFiles()
	n, err := u.store.Append(req.ReleaseName, req.Target.Value, req.Namespace, versionOf(files), u.mat.LastDigest(), files)
	if err != nil {
		return in.UpgradeSkillOutput{}, err
	}
	res.Revision = int64(n)
	return in.UpgradeSkillOutput{Result: res}, nil
}
