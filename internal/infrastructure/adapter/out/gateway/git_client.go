// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"github.com/gaarutyunov/epos/internal/infrastructure/app/port/out"
	"github.com/gaarutyunov/epos/internal/infrastructure/git"
)

// GitClientImpl is the shared, domain-free git adapter (SPEC §15.1). It drives
// the git binary to resolve a ref to a commit and capture the subpath's tree SHA
// (the basis of git-source pin capture, SPEC §9.7).
type GitClientImpl struct {
	client *git.Client
}

var _ out.GitClient = (*GitClientImpl)(nil)

// NewGitClientImpl wraps a git client (nil ⇒ default, unauthenticated).
func NewGitClientImpl(client *git.Client) *GitClientImpl {
	if client == nil {
		client = &git.Client{}
	}
	return &GitClientImpl{client: client}
}

// GitClient resolves remoteURL@ref/subpath, returning (commit, treeSha).
func (g *GitClientImpl) GitClient(remoteURL string, ref string, subpath string) (string, string, error) {
	res, err := g.client.Resolve(remoteURL, ref, subpath)
	if err != nil {
		return "", "", err
	}
	return res.Commit, res.TreeSha, nil
}
