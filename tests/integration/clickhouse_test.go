//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/gaarutyunov/epos/internal/catalog"
)

// clickhouseImage is the tag deploy/compose.yaml pins, and it is the same one
// deliberately: a schema this test proves against one server version and a
// deployment that applies it to another is a test about the wrong thing.
const clickhouseImage = "clickhouse/clickhouse-server:25.3.3.42-alpine"

// The three passwords the fixture substitutes into the checked-in schema.
//
// Not secrets and not pretending to be: they exist for the length of one
// container. The names say so, because a reader who greps this repository for
// something that looks like a credential should find an answer immediately.
const (
	chAdminPassword     = "admin-not-a-secret"
	chCollectorPassword = "collector-not-a-secret"
	chCatalogPassword   = "catalog-not-a-secret"
)

// dsnPlaceholder is what deploy/clickhouse/01-schema.sql carries in place of a
// password, twice — once for the collector and once for the catalog, in that
// order.
const dsnPlaceholder = "IDENTIFIED BY '...'"

// Repositories the fixture records downloads for.
//
// pdf and reviewer are in the catalog's index; outside is not, and exists to
// show the scope is enforced by the query rather than by the renderer. lost
// only ever receives a download before the rollup exists.
const (
	repoPDF      = "demo/agent-skills/pdf"
	repoReviewer = "demo/agent-skills/reviewer"
	repoQuiet    = "demo/agent-skills/quiet"
	repoOutside  = "someone-else/private"
	repoLost     = "demo/agent-skills/lost"
)

// chFixture is one ClickHouse container plus the checked-in schema applied to
// it in the order deploy/compose.yaml applies it.
type chFixture struct {
	container testcontainers.Container
	native    string
}

// TestTheStatisticsStoreAnswersTheCatalogsQuery is task 1.5 and task 4.3, and
// it is the assertion that the durable half of this change works.
//
// It is one test and not five because the properties are sequential facts about
// one store: the rollup cannot be created before the table it reads exists, a
// download recorded before it exists is lost, a download recorded after it
// exists is counted, and the count is only right if the reader sums. Split into
// independent tests each would need the four stages before it anyway.
//
// What it does not cover, stated here rather than left to be assumed: no
// OpenTelemetry Collector runs. The spans are written into a stand-in for
// otel_traces (testdata/clickhouse/otel-traces-stand-in.sql) carrying the four
// columns the rollup reads. So this proves the rollup, the ordering, the
// aggregation, the grants and the Go read path; it does not prove that the
// contrib clickhouseexporter names those four columns the way the design says
// it does. That leg needs a collector image and is the one part of task 1.5
// still unexercised.
func TestTheStatisticsStoreAnswersTheCatalogsQuery(t *testing.T) {
	ctx := context.Background()
	f := startClickHouse(ctx, t)

	// --- Stage 1: epos's own objects, before anything writes -----------------
	code, out := f.apply(ctx, t, "default", chAdminPassword, "/01-schema.sql")
	require.Zero(t, code, "apply 01-schema.sql: %s", out)

	// --- The rollup cannot be created yet, and that is the whole reason it is
	// a second file (task 1.5a, the half a comment cannot demonstrate) --------
	code, out = f.apply(ctx, t, "default", chAdminPassword, "/02-rollup.sql")
	require.NotZero(t, code,
		"02-rollup.sql was applied before the collector's table existed and "+
			"succeeded; if that is now possible the two-stage bootstrap in "+
			"deploy/compose.yaml is unnecessary and should be collapsed. Output: %s", out)
	assert.Contains(t, strings.ToLower(out), "otel_traces",
		"the failure should name the table the rollup reads: %s", out)

	// --- The collector's table appears (here, a stand-in for it) -------------
	code, out = f.apply(ctx, t, "default", chAdminPassword, "/otel-traces-stand-in.sql")
	require.Zero(t, code, "apply the otel_traces stand-in: %s", out)

	// --- A download recorded BEFORE the rollup exists ------------------------
	//
	// This is the trap. Nothing errors, nothing warns, and the count is simply
	// never made — which on a deployment reads as "the leaderboard is zero for
	// no reason".
	f.recordDownload(ctx, t, repoLost, true)

	// --- Stage 2: the rollup, now that it has something to read --------------
	code, out = f.apply(ctx, t, "default", chAdminPassword, "/02-rollup.sql")
	require.Zero(t, code, "apply 02-rollup.sql after the stand-in exists: %s", out)

	// Merges held off so the partial rows below stay partial. Without this the
	// sum() assertion is a race against a background merge.
	code, out = f.apply(ctx, t, "default", chAdminPassword, "/stop-merges.sql")
	require.Zero(t, code, "stop merges: %s", out)

	// --- Downloads recorded AFTER the rollup exists --------------------------
	//
	// pdf twice verified, in two separate writes, so the SummingMergeTree holds
	// two partial rows for one key — which is what task 1.5b is about. Once
	// unverified, so the two sides of the counter can be told apart. reviewer
	// once, so the leaderboard has something to rank. outside once, so the
	// scope has something to exclude. quiet never, so "unknown" has something
	// to be unknown about.
	f.recordDownload(ctx, t, repoPDF, true)
	f.recordDownload(ctx, t, repoPDF, true)
	f.recordDownload(ctx, t, repoPDF, false)
	f.recordDownload(ctx, t, repoReviewer, true)
	f.recordDownload(ctx, t, repoOutside, true)

	t.Run("a download recorded before the rollup existed is in no count", func(t *testing.T) {
		// Task 1.5a. The span is in the store — it is simply not in the rollup,
		// because a materialized view is an insert trigger and does not
		// backfill. deploy/clickhouse/02-rollup.sql carries the repair.
		assert.Zero(t, f.rolledUp(ctx, t, repoLost),
			"a download recorded before the rollup existed was counted; either "+
				"materialized views backfill after all, or the fixture recorded "+
				"it in the wrong order")
		assert.Equal(t, uint64(1), f.spans(ctx, t, repoLost),
			"the span itself should still be in the store; only the rollup missed it")
	})

	t.Run("sum is required, not decorative", func(t *testing.T) {
		// Task 1.5b. Two writes for one key leave two partial rows. A reader
		// that takes a stored Downloads value gets 1; the answer is 2.
		partials, largest := f.partialRows(ctx, t, repoPDF)
		require.Greater(t, partials, uint64(1),
			"the fixture did not leave partial rows, so this proves nothing; "+
				"stop-merges.sql is what is supposed to guarantee it")
		assert.Less(t, largest, uint64(2),
			"no single stored row should already hold the total, or the merge "+
				"that this test stops has run anyway")
		assert.Equal(t, uint64(2), f.rolledUp(ctx, t, repoPDF),
			"sum() over the same rows is the right answer")
	})

	t.Run("the catalog reads real numbers through the read-only credential", func(t *testing.T) {
		// Task 4.3, and the answer to "how will I test this if there is no
		// durable metrics storage": these are counts of downloads that were
		// actually recorded, read by the production source over the production
		// query with the production grant.
		stats, err := catalog.NewClickHouseStats(
			f.dsn("epos_catalog", chCatalogPassword),
			[]string{repoPDF, repoReviewer, repoQuiet},
		)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, stats.Close()) })

		counts, err := stats.Pulls(ctx)
		require.NoError(t, err)

		assert.Equal(t, catalog.Pulls{Verified: 2, Unverified: 1}, counts.Rows[repoPDF])
		assert.Equal(t, catalog.Pulls{Verified: 1}, counts.Rows[repoReviewer])
		assert.NotContains(t, counts.Rows, repoQuiet,
			"a skill with no recorded downloads must be absent so the page can "+
				"render it as unknown; a zero row is a different claim")
		assert.NotContains(t, counts.Rows, repoOutside,
			"the source reported on a repository outside the catalog's index; "+
				"the bound is Repository IN ? and it is not optional")
		assert.NotContains(t, counts.Rows, repoLost)
		assert.WithinDuration(t, time.Now(), counts.CapturedAt, time.Minute,
			"the moment the counts were current travels with them")
		assert.Empty(t, counts.Note,
			"a source that measured something leaves the provenance note empty; "+
				"the capture time already says when")
	})

	t.Run("the catalogs credential cannot write", func(t *testing.T) {
		// Task 4.4b. The grant is the mechanism, so the refusal has to come
		// from the store rather than from anything in Go declining to try.
		code, out := f.apply(ctx, t, "epos_catalog", chCatalogPassword, "/catalog-write-probe.sql")
		require.NotZero(t, code,
			"the catalog's credential wrote to the store; 01-schema.sql grants "+
				"more than SELECT and D4g's separation is decoration. Output: %s", out)
		// Two refusals are correct and either is the property: the bounded
		// profile stops a write before privileges are consulted, and the grant
		// stops it if the profile is ever relaxed. What must not appear is a
		// syntax or missing-table error, which would mean the probe never
		// reached the check.
		refusal := strings.ToLower(out)
		assert.True(t,
			strings.Contains(refusal, "readonly") ||
				strings.Contains(refusal, "not enough privileges"),
			"the refusal should come from the readonly profile or from a missing "+
				"grant, not from the statement failing to parse: %s", out)
	})

}

// startClickHouse brings up the server and copies every .sql the test applies.
func startClickHouse(ctx context.Context, t *testing.T) *chFixture {
	t.Helper()

	files := []testcontainers.ContainerFile{
		{HostFilePath: schemaWithPasswords(t), ContainerFilePath: "/01-schema.sql", FileMode: 0o644},
		{
			HostFilePath:      filepath.Join("..", "..", "deploy", "clickhouse", "02-rollup.sql"),
			ContainerFilePath: "/02-rollup.sql", FileMode: 0o644,
		},
	}
	for _, name := range []string{
		"otel-traces-stand-in.sql", "record-download.sql",
		"stop-merges.sql", "catalog-write-probe.sql",
	} {
		files = append(files, testcontainers.ContainerFile{
			HostFilePath:      filepath.Join("testdata", "clickhouse", name),
			ContainerFilePath: "/" + name,
			FileMode:          0o644,
		})
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        clickhouseImage,
			ExposedPorts: []string{"9000/tcp", "8123/tcp"},
			Env: map[string]string{
				"CLICKHOUSE_USER":     "default",
				"CLICKHOUSE_PASSWORD": chAdminPassword,
				// Without this the default user cannot make the three
				// principals, and the whole point of 01-schema.sql is that it
				// makes them.
				"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1",
			},
			Files: files,
			WaitingFor: wait.ForHTTP("/ping").WithPort("8123/tcp").
				WithStartupTimeout(3 * time.Minute),
		},
		Started: true,
	})
	require.NoError(t, err, "start clickhouse")
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })

	native, err := c.PortEndpoint(ctx, "9000/tcp", "")
	require.NoError(t, err, "clickhouse native endpoint")
	return &chFixture{container: c, native: native}
}

// schemaWithPasswords writes the checked-in schema out with its two placeholder
// passwords filled in, and returns the path.
//
// The schema itself is used, not a copy of it maintained here: a test that
// asserts against its own idea of the schema asserts nothing about the file the
// deployment applies.
func schemaWithPasswords(t *testing.T) string {
	t.Helper()

	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "clickhouse", "01-schema.sql"))
	require.NoError(t, err)

	text := string(body)
	require.Equal(t, 2, strings.Count(text, dsnPlaceholder),
		"01-schema.sql should carry exactly two placeholder passwords, the "+
			"collector's and then the catalog's")
	text = strings.Replace(text, dsnPlaceholder,
		"IDENTIFIED BY '"+chCollectorPassword+"'", 1)
	text = strings.Replace(text, dsnPlaceholder,
		"IDENTIFIED BY '"+chCatalogPassword+"'", 1)

	path := filepath.Join(t.TempDir(), "01-schema.sql")
	require.NoError(t, os.WriteFile(path, []byte(text), 0o644))
	return path
}

// apply runs one checked-in .sql file through clickhouse-client, the way
// deploy/compose.yaml does, and returns the exit code and combined output.
func (f *chFixture) apply(ctx context.Context, t *testing.T, user, password, file string,
	params ...string) (int, string) {
	t.Helper()

	argv := append([]string{
		"clickhouse-client", "--user", user, "--password", password, "--multiquery",
	}, params...)
	// A shell, because the redirection is the interface: --multiquery reads the
	// script from stdin.
	code, reader, err := f.container.Exec(ctx,
		[]string{"sh", "-c", shellQuote(argv) + " < " + file}, tcexec.Multiplexed())
	require.NoError(t, err, "exec clickhouse-client")
	out, err := io.ReadAll(reader)
	require.NoError(t, err)
	return code, string(out)
}

// recordDownload writes one download span, standing in for the collector.
func (f *chFixture) recordDownload(ctx context.Context, t *testing.T, repository string, verified bool) {
	t.Helper()
	code, out := f.apply(ctx, t, "default", chAdminPassword, "/record-download.sql",
		"--param_repository="+repository,
		fmt.Sprintf("--param_verified=%t", verified),
	)
	require.Zero(t, code, "record a download for %s: %s", repository, out)
}

// rolledUp is the verified count the catalog's own aggregation yields.
func (f *chFixture) rolledUp(ctx context.Context, t *testing.T, repository string) uint64 {
	t.Helper()
	return f.scalar(ctx, t,
		"SELECT sum(Downloads) FROM epos.epos_downloads_total "+
			"WHERE Repository = ? AND Verified = true", repository)
}

// partialRows is how many rows the rollup left for one key, and the largest
// value any one of them holds — the two numbers that show why sum() is not
// decoration.
func (f *chFixture) partialRows(ctx context.Context, t *testing.T, repository string) (uint64, uint64) {
	t.Helper()
	const where = " FROM epos.epos_downloads_total WHERE Repository = ? AND Verified = true"
	return f.scalar(ctx, t, "SELECT count()"+where, repository),
		f.scalar(ctx, t, "SELECT max(Downloads)"+where, repository)
}

// spans is how many download spans the store holds for a repository, rollup or
// no rollup.
func (f *chFixture) spans(ctx context.Context, t *testing.T, repository string) uint64 {
	t.Helper()
	return f.scalar(ctx, t,
		"SELECT count() FROM epos.otel_traces "+
			"WHERE SpanName = 'epos.download' AND SpanAttributes['repository'] = ?",
		repository)
}

// scalar runs a one-value read as the admin, which is the only principal in
// this fixture that can see both tables.
func (f *chFixture) scalar(ctx context.Context, t *testing.T, query string, args ...any) uint64 {
	t.Helper()

	options, err := clickhouse.ParseDSN(f.dsn("default", chAdminPassword))
	require.NoError(t, err)
	conn, err := clickhouse.Open(options)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	var value uint64
	require.NoError(t, conn.QueryRow(ctx, query, args...).Scan(&value), "read: %s", query)
	return value
}

// dsn is how the catalog is told where the store is: a URL carrying the
// credential, and never a flag.
func (f *chFixture) dsn(user, password string) string {
	return fmt.Sprintf("clickhouse://%s:%s@%s/epos?dial_timeout=10s", user, password, f.native)
}

// shellQuote makes an argv safe to hand to sh -c.
func shellQuote(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
	}
	return strings.Join(quoted, " ")
}
