// Code scaffolded by sysgo; edit freely (not regenerated).

package usecase

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/registry/app/port/in"
)

// DetectDiscoveryInteractor implements the DetectDiscovery use case. This scaffold is
// written once; add orchestration logic here. sysgo will not overwrite it.
type DetectDiscoveryInteractor struct{}

var _ in.DetectDiscoveryUseCase = (*DetectDiscoveryInteractor)(nil)

// NewDetectDiscoveryInteractor constructs the interactor. Inject driven ports here.
func NewDetectDiscoveryInteractor() *DetectDiscoveryInteractor {
	return &DetectDiscoveryInteractor{}
}

func (d *DetectDiscoveryInteractor) DetectDiscovery(input in.DetectDiscoveryInput) (in.DetectDiscoveryOutput, error) {
	return in.DetectDiscoveryOutput{}, errors.New("not implemented")
}
