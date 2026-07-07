// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
)

// RegisterRegistryInteractor implements the RegisterRegistry use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type RegisterRegistryInteractor struct{}

var _ in.RegisterRegistryUseCase = (*RegisterRegistryInteractor)(nil)

// NewRegisterRegistryInteractor constructs the interactor. Inject driven ports here.
func NewRegisterRegistryInteractor() *RegisterRegistryInteractor {
	return &RegisterRegistryInteractor{}
}

func (r *RegisterRegistryInteractor) RegisterRegistry(input in.RegisterRegistryInput) (in.RegisterRegistryOutput, error) {
	return in.RegisterRegistryOutput{}, errors.New("not implemented")
}
