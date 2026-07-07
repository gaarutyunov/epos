// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
	"github.com/gaarutyunov/epos/internal/registry/domain"
)

// CatalogProbeImpl is a driven adapter implementing the CatalogProbe gateway port.
// This scaffold is written once; implement the external-system calls here.
type CatalogProbeImpl struct{}

var _ out.CatalogProbe = (*CatalogProbeImpl)(nil)

// NewCatalogProbeImpl constructs the gateway adapter. Inject your client here.
func NewCatalogProbeImpl() *CatalogProbeImpl {
	return &CatalogProbeImpl{}
}

func (c *CatalogProbeImpl) CatalogProbe(entry domain.RegistryEntry) (domain.CatalogResult, error) {
	return domain.CatalogResult{}, errors.New("not implemented")
}
