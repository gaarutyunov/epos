package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// DownloadsQuery is the read side of deploy/clickhouse/01-schema.sql, and the only
// statement this repository issues against the store.
//
// It is a constant rather than a string built at the call site so that one test
// can hold it against the query the checked-in schema states, and fail when the
// two drift. A schema file whose stated query is not the query that runs is
// worse than one that states nothing: it is documentation a reviewer trusts.
//
// Three things in it are decisions rather than syntax:
//
//   - sum() is required, not decorative. SummingMergeTree collapses rows
//     eventually, in background merges; reading Downloads without summing
//     returns whatever the merge state happens to be — right on an idle demo,
//     wrong under load, and wrong in a way that looks like a plausible smaller
//     number. tests/integration/clickhouse_test.go demonstrates the wrong
//     answer rather than asserting the rule in prose.
//   - It reads epos_downloads_total and never otel_traces. The raw span table
//     is the collector's, it expires under a TTL, and the encoding of Verified
//     belongs in the rollup rather than in every query.
//   - Repository IN ? is what scopes a source to the catalog's own skills. The
//     bound is in the WHERE clause, so a store holding counts for repositories
//     outside this catalog cannot leak them into its pages even if the renderer
//     forgot to filter.
const DownloadsQuery = `SELECT Repository, Verified, sum(Downloads) AS Downloads
FROM epos.epos_downloads_total
WHERE Repository IN ? AND Bucket >= ?
GROUP BY Repository, Verified`

// chReader is the slice of the driver this source uses.
//
// Two methods, both of which the driver's own Conn already satisfies, so that
// nothing wider than reading and closing is reachable from here — a source that
// cannot express a write is a stronger statement than one that chooses not to.
//
// Deliberately not mocked. The mapping below is asserted against a real
// ClickHouse in tests/integration/clickhouse_test.go instead, because a double
// of the thing that runs the query would agree with whatever the mapping did,
// including with rows no ClickHouse would ever have produced.
type chReader interface {
	Select(ctx context.Context, dest any, query string, args ...any) error
	Close() error
}

// downloadRow is one grouped row of DownloadsQuery.
//
// Uint64 on the wire because that is what sum() over a UInt64 column yields;
// the conversion to the int64 that Pulls carries happens once, here.
type downloadRow struct {
	Repository string `ch:"Repository"`
	Verified   bool   `ch:"Verified"`
	Downloads  uint64 `ch:"Downloads"`
}

// sinceAllTime is the lower bound that means "every bucket there is".
//
// The parameter stays in the query even though nothing configures a window yet:
// a trend view is the first thing this table was shaped for — hourly buckets
// exist so a sparkline is a GROUP BY rather than a new pipeline — and keeping
// the bound is what makes that a changed argument instead of a changed query.
//
// It is the Unix epoch and not a zero time.Time, deliberately. ClickHouse's
// DateTime starts at 1970; Go's zero time is year 1, and binding it does not
// fail loudly — it is clamped, which is the same answer by accident.
var sinceAllTime = time.Unix(0, 0).UTC()

// ClickHouseStats reads counts from the rollup the collector fills.
//
// This is the production source: the file source is an input an operator holds,
// and none is the absence of one. It queries; it never writes, and it holds a
// credential that could not write if it tried (deploy/clickhouse/01-schema.sql
// grants SELECT on one table to the principal this DSN carries).
type ClickHouseStats struct {
	conn  chReader
	repos []string
	since time.Time
	// now is injected so a test can assert the capture time travels with the
	// counts without comparing against a clock.
	now func() time.Time
}

// NewClickHouseStats opens a lazy connection to the store, scoped to repos.
//
// It does not reach the store. clickhouse.Open builds a pool and connects on
// first use, which is the behaviour this wants: a registry that refused to
// start because a statistics database was down would have turned a page feature
// into an availability problem. An unreachable store surfaces later, as counts
// that degrade to absent on a page that still renders.
//
// The DSN carries a password, so it appears in no error this function returns —
// including the parse failure, where the obvious wrapping of the driver's own
// error would print the URL it was given straight into a log.
func NewClickHouseStats(dsn string, repos []string) (*ClickHouseStats, error) {
	if dsn == "" {
		return nil, fmt.Errorf("--catalog.stats-source %s needs %s%s",
			SourceClickHouse, statsDSNEnv, statsDSNHint)
	}
	options, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		// Not %w, and not the DSN. net/url's parse errors quote the whole URL
		// back, credential included, and this error is on a path that ends in a
		// log line an operator pastes into an issue.
		return nil, errors.New(statsDSNEnv + " is not a valid ClickHouse DSN; " +
			"the value is not repeated here because it carries the credential")
	}
	conn, err := clickhouse.Open(options)
	if err != nil {
		return nil, errors.New("open the statistics store: " +
			"the connection could not be built from " + statsDSNEnv)
	}
	return &ClickHouseStats{
		conn:  conn,
		repos: repos,
		since: sinceAllTime,
		now:   time.Now,
	}, nil
}

// statsDSNEnv is the one place the credential arrives from, named in errors so
// an operator who set nothing knows what to set.
const statsDSNEnv = "EPOS_REGISTRY_CATALOG_STATS_DSN"

// statsDSNHint says why there is no flag to suggest instead.
const statsDSNHint = " in the environment; there is no flag for it, because a" +
	" server's arguments are readable by every process on the host"

// Pulls runs DownloadsQuery and folds its rows into per-repository counts.
func (c *ClickHouseStats) Pulls(ctx context.Context) (Counts, error) {
	// No repositories is not an empty query. `IN ()` is a syntax error in
	// ClickHouse, and a catalog with nothing in its index has nothing to ask
	// about — so the answer is an empty set of counts, captured now.
	if len(c.repos) == 0 {
		return Counts{CapturedAt: c.now(), Rows: map[string]Pulls{}}, nil
	}

	var rows []downloadRow
	if err := c.conn.Select(ctx, &rows, DownloadsQuery, c.repos, c.since); err != nil {
		return Counts{}, fmt.Errorf("query the statistics store: %w", err)
	}

	// Only repositories the store answered for get a row. A repository with no
	// downloads is deliberately absent rather than present with zero: the
	// renderer shows "unknown" for the first and a hard 0 for the second, and
	// those are different claims.
	counts := Counts{CapturedAt: c.now(), Rows: make(map[string]Pulls, len(rows))}
	for _, row := range rows {
		pulls := counts.Rows[row.Repository]
		// The two sides arrive as two rows of the same group — that is what
		// GROUP BY Repository, Verified produces — and they are kept apart here
		// for the same reason the schema keeps them apart: the leaderboard
		// ranks on Verified, and a sum would silently rank on the inflated one.
		if row.Verified {
			pulls.Verified = int64(row.Downloads)
		} else {
			pulls.Unverified = int64(row.Downloads)
		}
		counts.Rows[row.Repository] = pulls
	}
	return counts, nil
}

// Close releases the connection pool.
//
// Not part of Stats. The interface is one context-taking method, and that is
// what makes a further source an addition rather than a rewrite; a source with
// something to release says so by implementing io.Closer, and the one place
// that owns a source's lifetime asks.
func (c *ClickHouseStats) Close() error { return c.conn.Close() }

// Compile-time proof that the driver's own connection is a chReader, which is
// what keeps the narrowed interface honest rather than an invented shape the
// real client happens not to fit.
var _ chReader = (driver.Conn)(nil)
