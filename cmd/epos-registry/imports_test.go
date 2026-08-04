package main

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// epos writes nothing into the statistics store, from anywhere in this
// repository.
//
// The rollup is schema, the raw tables are the collector's, and epos reads. A
// write from Go would mean the process on the public internet — the one
// answering unauthenticated GETs — had acquired a database credential, which is
// the whole property the collector exists to avoid.
//
// Scoped to files that actually speak SQL rather than grepping every .go file
// for a keyword: "store" is an overloaded word in this repository (internal/store
// is the local OCI layout on disk and has nothing to do with ClickHouse), and a
// blanket scan flags it. Scoped this way the assertion also survives the read
// path landing: a ClickHouse driver is a legitimate thing for the catalog to
// link once it queries counts, and then this test checks exactly the files that
// could carry a write.
func TestNothingInGoWritesToTheStatisticsStore(t *testing.T) {
	speaksSQL := regexp.MustCompile(`"database/sql"|clickhouse-go`)
	writes := regexp.MustCompile(`(?i)\b(INSERT\s+INTO|CREATE\s+(TABLE|DATABASE|MATERIALIZED\s+VIEW|USER)|ALTER\s+TABLE|DROP\s+(TABLE|DATABASE)|TRUNCATE\s+TABLE)\b`)

	var scanned int
	require.NoError(t, filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "imports_test.go") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !speaksSQL.Match(body) {
			return nil
		}
		scanned++
		assert.NotRegexp(t, writes, string(body),
			"%s speaks SQL and carries a statement that would change the store; "+
				"the collector is the only writer and deploy/clickhouse/ "+
				"is the only schema", path)
		return nil
	}))

	// Today the answer is that nothing speaks SQL at all, which is the
	// strongest form of the property and worth stating rather than passing
	// silently on an empty set.
	t.Logf("%d Go files speak SQL; none of them writes", scanned)
}
