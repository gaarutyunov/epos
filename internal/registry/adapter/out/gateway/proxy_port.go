// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
	"github.com/gaarutyunov/epos/internal/registry/domain"
)

// ProxyPortImpl is a driven adapter implementing the ProxyPort gateway port.
// This scaffold is written once; implement the external-system calls here.
type ProxyPortImpl struct{}

var _ out.ProxyPort = (*ProxyPortImpl)(nil)

// NewProxyPortImpl constructs the gateway adapter. Inject your client here.
func NewProxyPortImpl() *ProxyPortImpl {
	return &ProxyPortImpl{}
}

func (p *ProxyPortImpl) Proxy(request domain.ProxyRequest) (domain.ProxyResponse, error) {
	return domain.ProxyResponse{}, errors.New("not implemented")
}
