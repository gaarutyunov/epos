// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/packaging/app/port/in"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

// SkillPushFunc pushes a built skill artifact to an OCI reference, returning the
// pushed descriptor. It is the driven seam supplied by the composition root
// (the artifact's raw bytes are not carried in the coarse SkillArtifact DTO, so
// pushing is delegated to a source-aware pusher).
type SkillPushFunc func(ref domain.OciRef, artifact domain.SkillArtifact) (domain.PackagedArtifact, error)

// PushSkillInteractor implements the PushSkill use case (SPEC §4.1).
type PushSkillInteractor struct {
	push SkillPushFunc
}

var _ in.PushSkillUseCase = (*PushSkillInteractor)(nil)

// NewPushSkillInteractor injects the push seam.
func NewPushSkillInteractor(push SkillPushFunc) *PushSkillInteractor {
	return &PushSkillInteractor{push: push}
}

func (p *PushSkillInteractor) PushSkill(input in.PushSkillInput) (in.PushSkillOutput, error) {
	pushed, err := p.push(input.Ref, input.Artifact)
	return in.PushSkillOutput{Pushed: pushed}, err
}
