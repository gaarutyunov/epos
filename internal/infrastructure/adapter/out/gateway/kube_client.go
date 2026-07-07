// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/infrastructure/app/port/out"
)

// KubeClientImpl is a driven adapter implementing the KubeClient gateway port.
// This scaffold is written once; implement the external-system calls here.
type KubeClientImpl struct{}

var _ out.KubeClient = (*KubeClientImpl)(nil)

// NewKubeClientImpl constructs the gateway adapter. Inject your client here.
func NewKubeClientImpl() *KubeClientImpl {
	return &KubeClientImpl{}
}

func (k *KubeClientImpl) KubeClient(namespace string, kind string, name string, manifest string) (bool, error) {
	return false, errors.New("not implemented")
}
