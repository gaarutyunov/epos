// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
	"github.com/gaarutyunov/epos/internal/registry/domain"
)

// RegistrationStoreImpl is a driven adapter implementing the RegistrationStore gateway port.
// This scaffold is written once; implement the external-system calls here.
type RegistrationStoreImpl struct{}

var _ out.RegistrationStore = (*RegistrationStoreImpl)(nil)

// NewRegistrationStoreImpl constructs the gateway adapter. Inject your client here.
func NewRegistrationStoreImpl() *RegistrationStoreImpl {
	return &RegistrationStoreImpl{}
}

func (r *RegistrationStoreImpl) RegistrationStore(index domain.RegistrationIndex) (bool, error) {
	return false, errors.New("not implemented")
}
