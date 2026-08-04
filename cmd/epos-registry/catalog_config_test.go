package main

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gaarutyunov/epos/internal/catalog"
)

// The catalog is off by default, so a plain `epos-registry` still serves /v2/
// and nothing else.
func TestTheCatalogIsOffByDefault(t *testing.T) {
	cfg, err := loadConfig(flagsFor(t, "--upstream", "http://zot:5000"))
	require.NoError(t, err)

	assert.False(t, cfg.catalog.enabled)
	assert.Equal(t, catalog.SourceNone, cfg.catalog.statsSource,
		"and it renders no counts unless a source is configured")
}

// The credential's only path.
//
// EPOS_REGISTRY_CATALOG_STATS_DSN lowercases to catalog_stats_dsn, matches no
// `__`, and misses the hardcoded metrics_ replace. Without a `catalog_` line in
// the TransformFunc it ends up as the flat key catalog-stats-dsn and resolves
// to nothing — and because that key is the credential and has no flag, the
// failure is silent and reads as "the store is not configured".
func TestTheStoreCredentialResolvesFromTheEnvironment(t *testing.T) {
	t.Setenv(envPrefix+"UPSTREAM", "http://zot:5000")
	t.Setenv(envPrefix+"CATALOG_STATS_DSN", "clickhouse://reader@store:9000/epos")

	cfg, err := loadConfig(flagsFor(t))
	require.NoError(t, err)
	assert.Equal(t, "clickhouse://reader@store:9000/epos", cfg.statsDSN)
}

// It has no flag at all, and that is the mechanism rather than an omission: a
// server runs for days and its arguments are readable by every process on the
// host.
func TestTheStoreCredentialHasNoFlag(t *testing.T) {
	assert.Nil(t, newRootCommand().Flags().Lookup("catalog.stats-dsn"))
	assert.Nil(t, newRootCommand().Flags().Lookup("catalog-stats-dsn"))
}

// The rest of the catalog keys reach their dotted form the same way.
func TestTheCatalogKeysResolveFromTheEnvironment(t *testing.T) {
	t.Setenv(envPrefix+"UPSTREAM", "http://zot:5000")
	t.Setenv(envPrefix+"CATALOG_ENABLED", "true")
	t.Setenv(envPrefix+"CATALOG_BASE_PATH", "/epos/catalog")
	t.Setenv(envPrefix+"CATALOG_NAMESPACE", "demo/agent-skills")
	t.Setenv(envPrefix+"CATALOG_STATS_SOURCE", "file")
	t.Setenv(envPrefix+"CATALOG_STATS_FILE", "/etc/epos/counts.json")
	t.Setenv(envPrefix+"CATALOG_STATS_TTL", "0s")

	cfg, err := loadConfig(flagsFor(t))
	require.NoError(t, err)

	assert.True(t, cfg.catalog.enabled)
	assert.Equal(t, "/epos/catalog", cfg.catalog.basePath)
	assert.Equal(t, "demo/agent-skills", cfg.catalog.namespace)
	assert.True(t, cfg.catalog.namespaceMode)
	assert.Equal(t, "file", cfg.catalog.statsSource)
	assert.Equal(t, "/etc/epos/counts.json", cfg.catalog.statsFile)
	assert.Equal(t, time.Duration(0), cfg.catalog.statsTTL,
		"a TTL of zero must survive the round trip: it means query every request")
}

// The metrics keys keep behaving exactly as they did. The transform was
// deliberately not generalised to map every `_` to `.`, because that would
// break this one — whose env form is EPOS_REGISTRY_METRICS_VERSION_ATTRIBUTE.
func TestTheCatalogTransformDoesNotDisturbTheMetricsKeys(t *testing.T) {
	t.Setenv(envPrefix+"UPSTREAM", "http://zot:5000")
	t.Setenv(envPrefix+"METRICS_VERSION_ATTRIBUTE", "true")
	t.Setenv(envPrefix+"CATALOG_STATS_DSN", "clickhouse://reader@store:9000/epos")

	cfg, err := loadConfig(flagsFor(t))
	require.NoError(t, err)
	assert.True(t, cfg.versionAttribute)
}

// The upstream URL is where the catalog's registry comes from. There is no
// separate setting, so a registry's own UI cannot be pointed at somebody else's
// registry.
func TestTheCatalogBrowsesTheUpstreamTheRelayFronts(t *testing.T) {
	cfg, err := loadConfig(flagsFor(t, "--upstream", "http://127.0.0.1:45100"))
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:45100", cfg.registryHost)
	assert.True(t, cfg.plainHTTP, "an http upstream is a plain-HTTP registry")

	assert.Nil(t, newRootCommand().Flags().Lookup("catalog.registry"))
	assert.Nil(t, newRootCommand().Flags().Lookup("registry"))

	secure, err := loadConfig(flagsFor(t, "--upstream", "https://ghcr.io"))
	require.NoError(t, err)
	assert.False(t, secure.plainHTTP)
}

// Adding a subcommand must not change what a bare `epos-registry` does.
func TestTheRootStillServesAndTakesNoArguments(t *testing.T) {
	root := newRootCommand()

	var names []string
	for _, sub := range root.Commands() {
		names = append(names, sub.Name())
	}
	assert.Contains(t, names, "catalog")
	assert.NotNil(t, root.RunE, "the root still serves")

	assert.Error(t, root.Args(root, []string{"serve"}),
		"the root takes no positional arguments")
}

// 8.1d: the root's flags are local, so the subcommand declares its own set
// through the shared registration helper. An export subcommand that opens no
// port must not advertise --addr.
func TestExportDeclaresItsOwnFlagsAndNotTheServers(t *testing.T) {
	var export *cobra.Command
	for _, sub := range newRootCommand().Commands() {
		if sub.Name() != "catalog" {
			continue
		}
		for _, leaf := range sub.Commands() {
			if leaf.Name() == "export" {
				export = leaf
			}
		}
	}
	require.NotNil(t, export, "epos-registry catalog export")

	assert.NotNil(t, export.Flags().Lookup("out"))
	assert.NotNil(t, export.Flags().Lookup("upstream"))
	assert.NotNil(t, export.Flags().Lookup("catalog.base-path"))
	assert.NotNil(t, export.Flags().Lookup("catalog.stats-source"))

	assert.Nil(t, export.Flags().Lookup("addr"), "export opens no port")
	assert.Nil(t, export.Flags().Lookup("catalog.enabled"), "export is not a switch on itself")
	assert.Nil(t, export.Flags().Lookup("catalog.stats-ttl"),
		"a one-shot render has nothing to keep fresh")
}

// The measurement behind `catalog.enabled`, kept as a test so the collision
// cannot come back the next time somebody prefers the shorter flag.
//
// koanf holds a dotted key as a tree. A scalar at `catalog` and a map at
// `catalog.base-path` cannot both exist: whichever loads last wins and the
// other resolves to nothing, with no error anywhere. The design's key table
// wrote the enable switch as a bare `catalog`; this is that table corrected.
func TestTheEnableKeyDoesNotCollideWithTheCatalogSubtree(t *testing.T) {
	t.Setenv(envPrefix+"UPSTREAM", "http://zot:5000")
	t.Setenv(envPrefix+"CATALOG_ENABLED", "true")
	t.Setenv(envPrefix+"CATALOG_BASE_PATH", "/epos/catalog")
	t.Setenv(envPrefix+"CATALOG_STATS_DSN", "clickhouse://reader@store:9000/epos")

	cfg, err := loadConfig(flagsFor(t))
	require.NoError(t, err)

	assert.True(t, cfg.catalog.enabled, "the switch resolved")
	assert.Equal(t, "/epos/catalog", cfg.catalog.basePath, "and so did its siblings")
	assert.Equal(t, "clickhouse://reader@store:9000/epos", cfg.statsDSN,
		"including the one that has no flag to fall back on")
}
