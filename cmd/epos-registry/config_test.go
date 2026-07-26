package main

import (
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// flagsFor builds the root command's flag set as the CLI would, then applies
// the given arguments.
func flagsFor(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	cmd := newRootCommand()
	require.NoError(t, cmd.ParseFlags(args), "parse %v", args)
	return cmd.Flags()
}

func TestConfigDefaults(t *testing.T) {
	cfg, err := loadConfig(flagsFor(t, "--upstream", "http://zot:5000"))
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.addr)
	assert.Equal(t, "stdout", cfg.exporter)
	assert.False(t, cfg.versionAttribute,
		"SPEC.md 5.3 wants version-valued attributes off by default")
}

func TestUpstreamIsRequired(t *testing.T) {
	_, err := loadConfig(flagsFor(t))
	assert.Error(t, err, "an upstream is required")
}

func TestConfigFromEnvironment(t *testing.T) {
	t.Setenv(envPrefix+"UPSTREAM", "http://zot:5000")
	t.Setenv(envPrefix+"ADDR", "127.0.0.1:9999")
	t.Setenv(envPrefix+"METRICS_EXPORTER", "none")
	t.Setenv(envPrefix+"METRICS_INTERVAL", "250ms")
	t.Setenv(envPrefix+"METRICS_VERSION_ATTRIBUTE", "true")

	cfg, err := loadConfig(flagsFor(t))
	require.NoError(t, err)

	assert.Equal(t, "http://zot:5000", cfg.upstreamURL)
	assert.Equal(t, "127.0.0.1:9999", cfg.addr)
	assert.Equal(t, "none", cfg.exporter)
	assert.Equal(t, 250*time.Millisecond, cfg.interval)
	assert.True(t, cfg.versionAttribute)
}

// A flag the user actually typed beats the ambient environment; an untouched
// flag must not overwrite it with its default.
func TestFlagsBeatEnvironment(t *testing.T) {
	t.Setenv(envPrefix+"UPSTREAM", "http://from-env:5000")
	t.Setenv(envPrefix+"ADDR", "127.0.0.1:9999")

	cfg, err := loadConfig(flagsFor(t, "--upstream", "http://from-flag:5000"))
	require.NoError(t, err)

	assert.Equal(t, "http://from-flag:5000", cfg.upstreamURL,
		"a flag the user typed beats the environment")
	assert.Equal(t, "127.0.0.1:9999", cfg.addr,
		"an untouched flag must not clobber the environment with its default")
}
