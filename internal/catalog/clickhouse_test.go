package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The rows this source maps and the counts it returns are asserted against a
// real server in tests/integration/clickhouse_test.go, not here. A source whose
// only job is to run one query and fold its rows is not usefully tested against
// a double of the thing that runs the query: the double would agree with
// whatever the mapping did, including with a mapping that no ClickHouse would
// ever produce those rows for. What is left here is everything that is true
// before a connection exists — and the credential handling, which is the part
// that must not wait for an integration run to be found wrong.

func TestTheClickHouseSourceIsSelectableByName(t *testing.T) {
	stats, err := StatsFor(SourceClickHouse, "", "clickhouse://reader@store:9000/epos", nil)
	require.NoError(t, err)
	assert.IsType(t, &ClickHouseStats{}, stats)
	require.NoError(t, stats.(*ClickHouseStats).Close())
}

// A source that cannot be configured says what to set, and the answer is never
// a flag: the DSN is a working credential for a queryable database, and a
// server's arguments are readable by every process on the host.
func TestTheClickHouseSourceWithoutADSNSaysWhereToPutOne(t *testing.T) {
	_, err := StatsFor(SourceClickHouse, "", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "EPOS_REGISTRY_CATALOG_STATS_DSN")
	assert.NotContains(t, err.Error(), "--catalog.stats-dsn",
		"there is no such flag, so suggesting one sends an operator looking for it")
}

// The failure this guards is a real one and it is easy to write by accident:
// net/url quotes the whole URL back in its parse error, credential included,
// and that error would be wrapped straight into a log line an operator pastes
// into an issue.
func TestAMalformedDSNIsNotQuotedBackWithItsPassword(t *testing.T) {
	const password = "hunter2-do-not-print-me"
	_, err := NewClickHouseStats(
		"clickhouse://epos_catalog:"+password+"@store:not-a-port/epos", nil)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), password,
		"the DSN's password reached an error message")
	assert.Contains(t, err.Error(), "EPOS_REGISTRY_CATALOG_STATS_DSN",
		"an operator still has to be told which value was rejected")
}

// Construction must not reach the store. A registry that would not start
// because a statistics database was down would have turned a page feature into
// an availability problem; the store's absence is meant to surface as counts
// that degrade to absent on a page that still renders.
func TestBuildingTheSourceDoesNotReachTheStore(t *testing.T) {
	stats, err := NewClickHouseStats("clickhouse://epos_catalog:pw@127.0.0.1:1/epos", nil)
	require.NoError(t, err, "port 1 has nothing listening, and that must not matter yet")
	require.NoError(t, stats.Close())
}

// An empty index is not an empty query: `IN ()` does not parse, and there is
// nothing to ask about anyway. The conn is deliberately nil here — reaching it
// at all would be the defect.
func TestACatalogWithNoSkillsAsksTheStoreNothing(t *testing.T) {
	captured := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	stats := &ClickHouseStats{now: func() time.Time { return captured }}

	counts, err := stats.Pulls(context.Background())
	require.NoError(t, err)
	assert.Equal(t, captured, counts.CapturedAt)
	assert.Empty(t, counts.Rows)
}

// The cache is what the rest of the program holds, so a source with something
// to release is only reachable through it.
func TestTheCacheReleasesASourceThatHoldsSomething(t *testing.T) {
	inner, err := NewClickHouseStats("clickhouse://epos_catalog:pw@127.0.0.1:1/epos", nil)
	require.NoError(t, err)

	cached := WithCache(inner, time.Second, time.Second)
	closer, ok := cached.(interface{ Close() error })
	require.True(t, ok, "the cache must pass a release through, or the pool leaks")
	assert.NoError(t, closer.Close())
}

// And a source that holds nothing is not an error to release.
func TestReleasingASourceThatHoldsNothingIsNotAnError(t *testing.T) {
	cached := WithCache(NewFileStats("counts.json", nil), time.Second, time.Second)
	closer, ok := cached.(interface{ Close() error })
	require.True(t, ok)
	assert.NoError(t, closer.Close())
}

// The schema file writes the read side out in full, so that reading the stored
// column directly — which is correct only by coincidence of timing — is not the
// first thing an implementation tries. That is only worth anything while the
// two agree, and nothing but this test makes them.
func TestTheQueryMatchesTheOneTheSchemaStates(t *testing.T) {
	schema, err := os.ReadFile(filepath.Join("..", "..", "deploy", "clickhouse", "01-schema.sql"))
	require.NoError(t, err)

	assert.Contains(t, uncommentedSQL(string(schema)), collapseSpaces(DownloadsQuery),
		"deploy/clickhouse/01-schema.sql no longer states the query this package runs")
}

// The one query mistake the design predicts, asserted where it is cheapest: the
// aggregation is in the text or it is nowhere.
func TestTheQueryAggregatesAndReadsTheRollup(t *testing.T) {
	assert.Contains(t, DownloadsQuery, "sum(Downloads)")
	assert.Contains(t, DownloadsQuery, "epos_downloads_total")
	assert.NotContains(t, DownloadsQuery, "otel_traces",
		"the raw span table expires under a TTL; the rollup is what outlives it")
	assert.Equal(t, 2, strings.Count(DownloadsQuery, "?"),
		"the repositories and the window are both bound, never interpolated")
}

// uncommentedSQL strips the leading `--` from a .sql file's comment lines, so a
// statement quoted inside one can be compared with a Go constant.
func uncommentedSQL(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimPrefix(strings.TrimSpace(line), "--")
	}
	return collapseSpaces(strings.Join(lines, "\n"))
}

// collapseSpaces makes a comparison about the statement rather than about how
// it was wrapped.
func collapseSpaces(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}
