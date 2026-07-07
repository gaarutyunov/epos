// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/composition/app/port/out"
	"github.com/gaarutyunov/epos/internal/composition/domain"
)

// CompositionPortImpl is a driven adapter implementing the CompositionPort gateway port.
// This scaffold is written once; implement the external-system calls here.
type CompositionPortImpl struct{}

var _ out.CompositionPort = (*CompositionPortImpl)(nil)

// NewCompositionPortImpl constructs the gateway adapter. Inject your client here.
func NewCompositionPortImpl() *CompositionPortImpl {
	return &CompositionPortImpl{}
}

func (c *CompositionPortImpl) Composition(request domain.ComposeRequest) (domain.MergedSkill, error) {
	return domain.MergedSkill{}, errors.New("not implemented")
}
