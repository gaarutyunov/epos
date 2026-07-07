// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/packaging/app/port/out"
	"github.com/gaarutyunov/epos/internal/packaging/domain"
)

// PackagingPortImpl is a driven adapter implementing the PackagingPort gateway port.
// This scaffold is written once; implement the external-system calls here.
type PackagingPortImpl struct{}

var _ out.PackagingPort = (*PackagingPortImpl)(nil)

// NewPackagingPortImpl constructs the gateway adapter. Inject your client here.
func NewPackagingPortImpl() *PackagingPortImpl {
	return &PackagingPortImpl{}
}

func (p *PackagingPortImpl) Packaging(request domain.PackageRequest) (domain.PackagedArtifact, error) {
	return domain.PackagedArtifact{}, errors.New("not implemented")
}
