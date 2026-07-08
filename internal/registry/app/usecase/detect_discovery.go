// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
)

// DetectDiscoveryInteractor implements the DetectDiscovery use case via the
// CatalogProbe driven port (SPEC §8.1.1).
type DetectDiscoveryInteractor struct {
	probe out.CatalogProbe
}

var _ in.DetectDiscoveryUseCase = (*DetectDiscoveryInteractor)(nil)

// NewDetectDiscoveryInteractor injects the CatalogProbe driven port.
func NewDetectDiscoveryInteractor(probe out.CatalogProbe) *DetectDiscoveryInteractor {
	return &DetectDiscoveryInteractor{probe: probe}
}

func (d *DetectDiscoveryInteractor) DetectDiscovery(input in.DetectDiscoveryInput) (in.DetectDiscoveryOutput, error) {
	res, err := d.probe.CatalogProbe(input.Entry)
	return in.DetectDiscoveryOutput{Result: res}, err
}
