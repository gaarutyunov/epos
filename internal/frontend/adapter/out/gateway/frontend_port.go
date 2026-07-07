// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/frontend/app/port/out"
	"github.com/gaarutyunov/epos/internal/frontend/domain"
)

// FrontendPortImpl is a driven adapter implementing the FrontendPort gateway port.
// This scaffold is written once; implement the external-system calls here.
type FrontendPortImpl struct{}

var _ out.FrontendPort = (*FrontendPortImpl)(nil)

// NewFrontendPortImpl constructs the gateway adapter. Inject your client here.
func NewFrontendPortImpl() *FrontendPortImpl {
	return &FrontendPortImpl{}
}

func (f *FrontendPortImpl) Frontend(request domain.ListingRequest) (domain.Listing, error) {
	return domain.Listing{}, errors.New("not implemented")
}
