// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"errors"
	"github.com/gaarutyunov/epos/internal/infrastructure/app/port/out"
)

// GitClientImpl is a driven adapter implementing the GitClient gateway port.
// This scaffold is written once; implement the external-system calls here.
type GitClientImpl struct{}

var _ out.GitClient = (*GitClientImpl)(nil)

// NewGitClientImpl constructs the gateway adapter. Inject your client here.
func NewGitClientImpl() *GitClientImpl {
	return &GitClientImpl{}
}

func (g *GitClientImpl) GitClient(remoteURL string, ref string, subpath string) (string, string, error) {
	return "", "", errors.New("not implemented")
}
