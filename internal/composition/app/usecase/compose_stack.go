// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/composition/app/port/in"
)

// ComposeStackInteractor implements the ComposeStack use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type ComposeStackInteractor struct{}

var _ in.ComposeStackUseCase = (*ComposeStackInteractor)(nil)

// NewComposeStackInteractor constructs the interactor. Inject driven ports here.
func NewComposeStackInteractor() *ComposeStackInteractor {
	return &ComposeStackInteractor{}
}

func (c *ComposeStackInteractor) ComposeStack(input in.ComposeStackInput) (in.ComposeStackOutput, error) {
	return in.ComposeStackOutput{}, errors.New("not implemented")
}
