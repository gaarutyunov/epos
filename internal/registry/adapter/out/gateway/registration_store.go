// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"sync"

	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
	"github.com/gaarutyunov/epos/internal/registry/domain"
)

// RegistrationStoreImpl is the default in-memory registration-index store
// (SPEC §8.2, §11): ephemeral, rebuilt from config on startup. Durable
// ConfigMap/Secret/Postgres backends implement the same port.
type RegistrationStoreImpl struct {
	mu    sync.Mutex
	index domain.RegistrationIndex
}

var _ out.RegistrationStore = (*RegistrationStoreImpl)(nil)

// NewRegistrationStoreImpl constructs the in-memory store.
func NewRegistrationStoreImpl() *RegistrationStoreImpl {
	return &RegistrationStoreImpl{}
}

// RegistrationStore persists the registration index in memory.
func (r *RegistrationStoreImpl) RegistrationStore(index domain.RegistrationIndex) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.index = index
	return true, nil
}

// Index returns the currently stored registration index.
func (r *RegistrationStoreImpl) Index() domain.RegistrationIndex {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.index
}
