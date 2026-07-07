// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/install/app/port/out"
	"github.com/gaarutyunov/epos/internal/install/domain"
)

// RevisionStoreImpl is a driven adapter implementing the RevisionStore gateway port.
// This scaffold is written once; implement the external-system calls here.
type RevisionStoreImpl struct{}

var _ out.RevisionStore = (*RevisionStoreImpl)(nil)

// NewRevisionStoreImpl constructs the gateway adapter. Inject your client here.
func NewRevisionStoreImpl() *RevisionStoreImpl {
	return &RevisionStoreImpl{}
}

func (r *RevisionStoreImpl) RevisionStore(release domain.Release) (bool, error) {
	return false, errors.New("not implemented")
}
