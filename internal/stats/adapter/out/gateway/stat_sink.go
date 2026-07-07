// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/stats/app/port/out"
	"github.com/gaarutyunov/epos/internal/stats/domain"
)

// StatSinkImpl is a driven adapter implementing the StatSink gateway port.
// This scaffold is written once; implement the external-system calls here.
type StatSinkImpl struct{}

var _ out.StatSink = (*StatSinkImpl)(nil)

// NewStatSinkImpl constructs the gateway adapter. Inject your client here.
func NewStatSinkImpl() *StatSinkImpl {
	return &StatSinkImpl{}
}

func (s *StatSinkImpl) StatSink(request domain.CountRequest) (domain.CountSnapshot, error) {
	return domain.CountSnapshot{}, errors.New("not implemented")
}
