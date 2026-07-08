// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/install/app/port/in"
	"github.com/gaarutyunov/epos/internal/install/app/port/out"
)

// RollbackSkillInteractor implements the RollbackSkill use case: restore a
// previous bundle in full and record it as a new revision (SPEC §4.2, §5.3).
type RollbackSkillInteractor struct {
	mat   out.Materializer
	store out.RevisionRepository
	// target/namespace of the release (the coarse RollbackRequest omits them).
	target    string
	namespace string
}

var _ in.RollbackSkillUseCase = (*RollbackSkillInteractor)(nil)

// NewRollbackSkillInteractor injects the ports and the release's target context.
func NewRollbackSkillInteractor(mat out.Materializer, store out.RevisionRepository, target, namespace string) *RollbackSkillInteractor {
	if target == "" {
		target = "files"
	}
	return &RollbackSkillInteractor{mat: mat, store: store, target: target, namespace: namespace}
}

func (r *RollbackSkillInteractor) RollbackSkill(input in.RollbackSkillInput) (in.RollbackSkillOutput, error) {
	req := input.Request
	prev, err := r.store.Get(req.ReleaseName, r.target, r.namespace, int(req.ToRevision))
	if err != nil {
		return in.RollbackSkillOutput{}, err
	}
	if err := r.mat.Write(req.ReleaseName, r.target, r.namespace, prev.Files); err != nil {
		return in.RollbackSkillOutput{}, err
	}
	n, err := r.store.Append(req.ReleaseName, r.target, r.namespace, out.RevisionSpec{
		Version: prev.Version, Digest: prev.Digest, Registry: prev.Registry, Values: prev.Values, Overlays: prev.Overlays, Files: prev.Files,
	})
	if err != nil {
		return in.RollbackSkillOutput{}, err
	}
	return in.RollbackSkillOutput{Result: resultOf(req.ReleaseName, n)}, nil
}
