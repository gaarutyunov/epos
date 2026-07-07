// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/infrastructure/app/port/out"
	"github.com/gaarutyunov/epos/internal/infrastructure/domain"
)

// OciClientImpl is a driven adapter implementing the OciClient gateway port.
// This scaffold is written once; implement the external-system calls here.
type OciClientImpl struct{}

var _ out.OciClient = (*OciClientImpl)(nil)

// NewOciClientImpl constructs the gateway adapter. Inject your client here.
func NewOciClientImpl() *OciClientImpl {
	return &OciClientImpl{}
}

func (o *OciClientImpl) OciClient(endpoint domain.HTTPEndpoint, repo string, reference string) (string, error) {
	return "", errors.New("not implemented")
}
