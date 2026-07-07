// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/composition/app/port/out"
	"github.com/gaarutyunov/epos/internal/composition/domain"
)

// LayerSourceImpl is a driven adapter implementing the LayerSource gateway port.
// This scaffold is written once; implement the external-system calls here.
type LayerSourceImpl struct{}

var _ out.LayerSource = (*LayerSourceImpl)(nil)

// NewLayerSourceImpl constructs the gateway adapter. Inject your client here.
func NewLayerSourceImpl() *LayerSourceImpl {
	return &LayerSourceImpl{}
}

func (l *LayerSourceImpl) LayerSource(layer domain.Layer) (domain.PinRecord, error) {
	return domain.PinRecord{}, errors.New("not implemented")
}
