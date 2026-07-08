// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
	"github.com/gaarutyunov/epos/internal/registry/domain"
)

// RegisterRegistryInteractor implements the RegisterRegistry use case: it adds a
// registry entry to the registration index via the RegistrationStore driven port
// (SPEC §8.2). Registration is always sufficient on its own.
type RegisterRegistryInteractor struct {
	store out.RegistrationStore
	index domain.RegistrationIndex
}

var _ in.RegisterRegistryUseCase = (*RegisterRegistryInteractor)(nil)

// NewRegisterRegistryInteractor injects the RegistrationStore driven port.
func NewRegisterRegistryInteractor(store out.RegistrationStore) *RegisterRegistryInteractor {
	return &RegisterRegistryInteractor{store: store, index: domain.RegistrationIndex{ID: "default"}}
}

func (r *RegisterRegistryInteractor) RegisterRegistry(input in.RegisterRegistryInput) (in.RegisterRegistryOutput, error) {
	r.index.Entries = append(r.index.Entries, input.Entry)
	ok, err := r.store.RegistrationStore(r.index)
	return in.RegisterRegistryOutput{Ok: ok}, err
}
