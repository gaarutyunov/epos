// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/frontend/app/port/out"
	"github.com/gaarutyunov/epos/internal/frontend/domain"
)

// CatalogFeedImpl is a driven adapter implementing the CatalogFeed gateway port.
// This scaffold is written once; implement the external-system calls here.
type CatalogFeedImpl struct{}

var _ out.CatalogFeed = (*CatalogFeedImpl)(nil)

// NewCatalogFeedImpl constructs the gateway adapter. Inject your client here.
func NewCatalogFeedImpl() *CatalogFeedImpl {
	return &CatalogFeedImpl{}
}

func (c *CatalogFeedImpl) CatalogFeed(filter domain.Filter) (domain.Listing, error) {
	return domain.Listing{}, errors.New("not implemented")
}
