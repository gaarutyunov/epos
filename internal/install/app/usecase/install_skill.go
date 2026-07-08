// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"github.com/gaarutyunov/epos/internal/install/app/port/out"
)

// InstallSkillInteractor implements the InstallSkill use case: it materializes a
// resolved skill bundle through the MaterializePort and records a new revision
// through the RevisionStore (SPEC §4.2, §5).
type InstallSkillInteractor struct {
	mat   out.Materializer
	store out.RevisionRepository
}

var _ in.InstallSkillUseCase = (*InstallSkillInteractor)(nil)

// NewInstallSkillInteractor injects the MaterializePort and RevisionStore ports.
func NewInstallSkillInteractor(mat out.Materializer, store out.RevisionRepository) *InstallSkillInteractor {
	return &InstallSkillInteractor{mat: mat, store: store}
}

func (i *InstallSkillInteractor) InstallSkill(input in.InstallSkillInput) (in.InstallSkillOutput, error) {
	req := input.Request
	res, err := i.mat.Materialize(req)
	if err != nil {
		return in.InstallSkillOutput{}, err
	}
	files := i.mat.LastFiles()
	n, err := i.store.Append(req.ReleaseName, req.Target.Value, req.Namespace, out.RevisionSpec{
		Version: versionOf(files), Digest: i.mat.LastDigest(), Registry: req.SkillID, Values: i.mat.LastValues(), Files: files,
	})
	if err != nil {
		return in.InstallSkillOutput{}, err
	}
	res.Revision = int64(n)
	return in.InstallSkillOutput{Result: res}, nil
}
