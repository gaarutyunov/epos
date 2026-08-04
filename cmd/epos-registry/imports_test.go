package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The other direction of the same rule, and the reason internal/registry
// exists.
//
// The catalog needs enumeration and a content-layer fetch. Both used to live in
// packages the registry binary must not link: internal/cli is the whole command
// tree, and internal/skillfile carries the Skillfile build language along with
// go-git, goawk, go-gitdiff and goccy/go-yaml. Importing either to obtain one
// function is how a relay acquires a build system.
//
// This is the assertion that keeps the move honest. Without it the decision
// survives until the first convenient import.
func TestTheRegistryLinksNeitherTheCLINorTheBuildLanguage(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./").CombinedOutput()
	require.NoError(t, err, "go list -deps: %s", out)
	deps := strings.Fields(string(out))

	for _, forbidden := range []string{
		"github.com/gaarutyunov/epos/internal/cli",
		"github.com/gaarutyunov/epos/internal/skillfile",
		"github.com/gaarutyunov/epos/internal/install",
		"github.com/go-git/go-git/v5",
		"github.com/benhoyt/goawk/interp",
		"github.com/bluekeyes/go-gitdiff/gitdiff",
	} {
		assert.NotContains(t, deps, forbidden,
			"epos-registry must not link %s", forbidden)
	}

	// And the half that must be true: the catalog is here.
	assert.Contains(t, deps, "github.com/gaarutyunov/epos/internal/catalog")
	assert.Contains(t, deps, "github.com/gaarutyunov/epos/internal/registry")
}

// The registry holds no credential that can write to the store, and the
// cheapest way for that to stop being true is a ClickHouse client appearing in
// the binary.
//
// epos writes no ingestion code: the rollup is schema, the raw tables are the
// collector's, and epos reads. A write client here would mean the process on
// the public internet — the one answering unauthenticated GETs — had acquired a
// database credential, which is the whole property the collector exists to
// avoid.
func TestTheRegistryLinksNoClickHouseWriteClient(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "./").CombinedOutput()
	require.NoError(t, err, "go list -deps: %s", out)

	for _, dep := range strings.Fields(string(out)) {
		assert.NotContains(t, strings.ToLower(dep), "clickhouse",
			"epos-registry links %s: the store is filled by the collector, never from Go", dep)
	}
}
