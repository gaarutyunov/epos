// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/install/app/port/out"
	"github.com/gaarutyunov/epos/internal/install/domain"
)

// MaterializePortImpl is a driven adapter implementing the MaterializePort gateway port.
// This scaffold is written once; implement the external-system calls here.
type MaterializePortImpl struct{}

var _ out.MaterializePort = (*MaterializePortImpl)(nil)

// NewMaterializePortImpl constructs the gateway adapter. Inject your client here.
func NewMaterializePortImpl() *MaterializePortImpl {
	return &MaterializePortImpl{}
}

func (m *MaterializePortImpl) Materialize(request domain.InstallRequest) (domain.InstallResult, error) {
	return domain.InstallResult{}, errors.New("not implemented")
}
