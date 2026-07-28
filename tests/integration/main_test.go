//go:build integration

package integration

import (
	"os"
	"testing"
)

// TestMain owns the containers that outlive a single test.
//
// Gitea is one: it takes the better part of a minute to become healthy, so the
// suites that need a git server share one (see sharedGitea). The B2 registry is
// the other — not because zot is slow, but because every scenario of
// build-from-registry publishes its base into a repository of its own, so there
// is no state to keep apart and no reason to start one registry per scenario
// (see sharedZot).
//
// Both are terminated here rather than by t.Cleanup: a cleanup registered on
// whichever test happened to ask first would take the container down while
// later tests still needed it. The registries the other suites start stay
// per-scenario, because those scenarios do share repository names.
func TestMain(m *testing.M) {
	code := m.Run()
	stopSharedGitea()
	stopSharedZot()
	os.Exit(code)
}
