package main

import (
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The environment variables the tests set, spelled the way SPEC.md 4.6 says an
// operator spells them: the prefix, "__" between key-path segments, a single
// "_" between the words of one segment.
const (
	envUpstream         = envPrefix + "__UPSTREAM"
	envAddr             = envPrefix + "__ADDR"
	envOpsAddr          = envPrefix + "__OPS_ADDR"
	envMetricsExporter  = envPrefix + "__METRICS__EXPORTER"
	envMetricsInterval  = envPrefix + "__METRICS__INTERVAL"
	envVersionAttribute = envPrefix + "__METRICS__VERSION_ATTRIBUTE"
	envTracesExporter   = envPrefix + "__TRACES__EXPORTER"
	envLogsExporter     = envPrefix + "__LOGS__EXPORTER"
)

// flagsFor builds the root command's flag set as the CLI would, then applies
// the given arguments.
func flagsFor(t *testing.T, args ...string) *pflag.FlagSet {
	t.Helper()
	cmd := newRootCommand()
	require.NoError(t, cmd.ParseFlags(args), "parse %v", args)
	return cmd.Flags()
}

// load runs the command's own configuration pipeline against args.
func load(t *testing.T, args ...string) (registryConfig, error) {
	t.Helper()
	return loadConfig(t.Context(), flagsFor(t, args...))
}

func TestConfigDefaults(t *testing.T) {
	cfg, err := load(t, "--upstream", "http://zot:5000")
	require.NoError(t, err)

	assert.Equal(t, ":8080", cfg.Addr)
	assert.Equal(t, "stdout", cfg.Metrics.Exporter)
	assert.False(t, cfg.Metrics.VersionAttribute,
		"SPEC.md 5.3 wants version-valued attributes off by default")

	// goga/telemetry installs all three signals or none, so traces and logs
	// have a configured exporter whether or not anyone asked for them.
	assert.Equal(t, "none", cfg.Traces.Exporter,
		"traces go nowhere until a collector is deployed")
	assert.Equal(t, "stderr", cfg.Logs.Exporter,
		"operator output stays on stderr, where log.Printf wrote it")
}

func TestUpstreamIsRequired(t *testing.T) {
	_, err := load(t)
	require.Error(t, err, "an upstream is required")
	assert.Contains(t, err.Error(), upstreamEnv,
		"the message names the variable an operator would set")
}

func TestConfigFromEnvironment(t *testing.T) {
	t.Setenv(envUpstream, "http://zot:5000")
	t.Setenv(envAddr, "127.0.0.1:9999")
	t.Setenv(envMetricsExporter, "none")
	t.Setenv(envMetricsInterval, "250ms")
	t.Setenv(envVersionAttribute, "true")

	cfg, err := load(t)
	require.NoError(t, err)

	assert.Equal(t, "http://zot:5000", cfg.Upstream)
	assert.Equal(t, "127.0.0.1:9999", cfg.Addr)
	assert.Equal(t, "none", cfg.Metrics.Exporter)
	assert.Equal(t, 250*time.Millisecond, cfg.Metrics.Interval)
	assert.True(t, cfg.Metrics.VersionAttribute)
}

// The traces and logs groups reach their dotted keys the same way the metrics
// group does.
func TestTraceAndLogExporterFromEnvironment(t *testing.T) {
	t.Setenv(envUpstream, "http://zot:5000")
	t.Setenv(envTracesExporter, "otlp")
	t.Setenv(envLogsExporter, "none")

	cfg, err := load(t)
	require.NoError(t, err)

	assert.Equal(t, "otlp", cfg.Traces.Exporter)
	assert.Equal(t, "none", cfg.Logs.Exporter)
}

// TestPrecedence pins the whole order, for every setting, in one table.
//
// It exists because precedence is the property that fails silently. A loader
// that gets it backwards returns no error and starts a process on a value
// nobody chose, and the symptom — "the flag does nothing" — surfaces nowhere
// near the code that caused it. Every setting is covered rather than a
// representative one, because the way this goes wrong in practice is a
// per-key exception in the environment-name mapping, which a spot check on
// `upstream` would never see.
//
// Three relations, asserted for each setting:
//
//  1. a flag the operator typed beats the environment;
//  2. the environment beats a flag left at its default;
//  3. a flag default is what is left when nothing else sets the key.
func TestPrecedence(t *testing.T) {
	type want struct {
		flagWins, envWins, fallback string
	}
	cases := []struct {
		name   string
		env    string // the variable that sets this key
		envVal string
		flag   string // the argument that sets it, as "--name=value"
		got    func(registryConfig) string
		want   want
	}{
		{
			name: "addr", env: envAddr, envVal: "127.0.0.1:9999",
			flag: "--addr=127.0.0.1:1111",
			got:  func(c registryConfig) string { return c.Addr },
			want: want{flagWins: "127.0.0.1:1111", envWins: "127.0.0.1:9999", fallback: ":8080"},
		},
		{
			name: "ops-addr", env: envOpsAddr, envVal: "127.0.0.1:9998",
			flag: "--ops-addr=127.0.0.1:1112",
			got:  func(c registryConfig) string { return c.OpsAddr },
			want: want{flagWins: "127.0.0.1:1112", envWins: "127.0.0.1:9998", fallback: ""},
		},
		{
			name: "upstream", env: envUpstream, envVal: "http://from-env:5000",
			flag: "--upstream=http://from-flag:5000",
			got:  func(c registryConfig) string { return c.Upstream },
			// No fallback case: an unset upstream fails the load, which
			// TestUpstreamIsRequired covers.
			want: want{flagWins: "http://from-flag:5000", envWins: "http://from-env:5000"},
		},
		{
			name: "metrics.exporter", env: envMetricsExporter, envVal: "none",
			flag: "--metrics.exporter=otlp",
			got:  func(c registryConfig) string { return c.Metrics.Exporter },
			want: want{flagWins: "otlp", envWins: "none", fallback: exporterStdout},
		},
		{
			name: "metrics.interval", env: envMetricsInterval, envVal: "250ms",
			flag: "--metrics.interval=1s",
			got:  func(c registryConfig) string { return c.Metrics.Interval.String() },
			want: want{flagWins: "1s", envWins: "250ms", fallback: "0s"},
		},
		{
			// The hyphen-to-underscore rewrite: --metrics.version-attribute and
			// …__METRICS__VERSION_ATTRIBUTE are one key, metrics.version_attribute.
			name: "metrics.version-attribute", env: envVersionAttribute, envVal: "true",
			flag: "--metrics.version-attribute=false",
			got: func(c registryConfig) string {
				if c.Metrics.VersionAttribute {
					return "true"
				}
				return "false"
			},
			want: want{flagWins: "false", envWins: "true", fallback: "false"},
		},
		{
			name: "traces.exporter", env: envTracesExporter, envVal: "otlp",
			flag: "--traces.exporter=stdout",
			got:  func(c registryConfig) string { return c.Traces.Exporter },
			want: want{flagWins: "stdout", envWins: "otlp", fallback: exporterNone},
		},
		{
			name: "logs.exporter", env: envLogsExporter, envVal: "none",
			flag: "--logs.exporter=stdout",
			got:  func(c registryConfig) string { return c.Logs.Exporter },
			want: want{flagWins: "stdout", envWins: "none", fallback: exporterStderr},
		},
	}

	// Every case but `upstream` needs an upstream from somewhere, and it must
	// come from the source with the LOWEST precedence in play so that it never
	// interferes with the relation under test.
	const anUpstream = "--upstream=http://zot:5000"

	for _, tc := range cases {
		t.Run(tc.name+"/flag beats environment", func(t *testing.T) {
			t.Setenv(envUpstream, "http://zot:5000")
			t.Setenv(tc.env, tc.envVal)
			cfg, err := load(t, tc.flag)
			require.NoError(t, err)
			assert.Equal(t, tc.want.flagWins, tc.got(cfg),
				"a flag the operator typed beats %s", tc.env)
		})

		t.Run(tc.name+"/environment beats a flag default", func(t *testing.T) {
			t.Setenv(envUpstream, "http://zot:5000")
			t.Setenv(tc.env, tc.envVal)
			cfg, err := load(t)
			require.NoError(t, err)
			assert.Equal(t, tc.want.envWins, tc.got(cfg),
				"an untouched flag must not clobber %s with its default", tc.env)
		})

		if tc.name == "upstream" {
			continue
		}
		t.Run(tc.name+"/flag default is the fallback", func(t *testing.T) {
			cfg, err := load(t, anUpstream)
			require.NoError(t, err)
			assert.Equal(t, tc.want.fallback, tc.got(cfg),
				"nothing sets %s, so the flag's own default stands", tc.name)
		})
	}
}

// The pre-adoption single-underscore spellings are gone. They are asserted
// dead rather than left untested: an operator upgrading reads this list, and a
// variable that quietly kept working would make the rename look optional.
func TestOldEnvironmentSpellingsAreNotRead(t *testing.T) {
	for _, old := range []string{
		"EPOS_REGISTRY_ADDR",
		"EPOS_REGISTRY_METRICS_EXPORTER",
		"EPOS_REGISTRY_METRICS__EXPORTER",
		"EPOS_REGISTRY_TRACES_EXPORTER",
	} {
		t.Run(old, func(t *testing.T) {
			t.Setenv(envUpstream, "http://zot:5000")
			t.Setenv(old, "127.0.0.1:9999")

			cfg, err := load(t)
			require.NoError(t, err, "an unrecognised name is ignored, not rejected")
			assert.Equal(t, ":8080", cfg.Addr)
			assert.Equal(t, exporterStdout, cfg.Metrics.Exporter)
			assert.Equal(t, exporterNone, cfg.Traces.Exporter)
		})
	}
}

// A value the environment cannot supply is now an error rather than a zero.
//
// Both of these used to start the process on a value nobody wrote:
// METRICS__VERSION_ATTRIBUTE=maybe turned the attribute OFF, which is
// indistinguishable from not asking for it, and METRICS__INTERVAL=250 meant
// 250 *nanoseconds* — an export interval that would spin the exporter — because
// koanf's Duration accessor reads a bare integer as a nanosecond count.
func TestUnparsableEnvironmentValueFailsTheLoad(t *testing.T) {
	t.Run("boolean", func(t *testing.T) {
		t.Setenv(envUpstream, "http://zot:5000")
		t.Setenv(envVersionAttribute, "maybe")

		_, err := load(t)
		require.Error(t, err, "a boolean that is not one must not silently be false")
	})

	t.Run("duration without a unit", func(t *testing.T) {
		t.Setenv(envUpstream, "http://zot:5000")
		t.Setenv(envMetricsInterval, "250")

		_, err := load(t)
		require.Error(t, err, "250 is not a duration; it used to mean 250ns")
	})
}

// A key cannot be both a value and a parent, and the loader now says so.
//
// EPOS_REGISTRY__METRICS=none is the shape of an operator reaching for
// --metrics.exporter and stopping a segment short. koanf cannot represent the
// merge — one of `metrics` and `metrics.exporter` is discarded, silently, with
// the winner decided by merge order — so goga/config detects it before merging
// and names both keys.
func TestCollidingKeysFailTheLoad(t *testing.T) {
	t.Setenv(envUpstream, "http://zot:5000")
	t.Setenv(envPrefix+"__METRICS", "none")

	_, err := load(t)
	require.Error(t, err, "a scalar `metrics` cannot coexist with `metrics.exporter`")
	assert.Contains(t, err.Error(), "metrics")
}
