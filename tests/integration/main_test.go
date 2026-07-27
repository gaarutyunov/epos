//go:build integration

package integration

import (
	"os"
	"testing"
)

// TestMain owns the containers that outlive a single test.
//
// Gitea is the only one: it takes the better part of a minute to become
// healthy, so the suites that need a git server share one (see sharedGitea) and
// it is terminated here, after every test in the package has had its chance at
// it. The registry containers stay per-scenario — zot starts in seconds, and a
// suite that tears its own down does not hold one open per scenario.
func TestMain(m *testing.M) {
	code := m.Run()
	stopSharedGitea()
	os.Exit(code)
}
