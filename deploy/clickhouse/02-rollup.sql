-- The catalog's store, part two: the rollup that fills the table.
--
-- Applied AFTER the collector has started and BEFORE the first download span.
-- Both halves of that sentence are load-bearing and they pull in opposite
-- directions, which is why this is its own file:
--
--   * after the collector, because a materialized view is created over a source
--     table that must already exist, and otel_traces is the collector's. Run
--     this against a ClickHouse the collector has never connected to and it
--     fails with UNKNOWN_TABLE.
--   * before the first download span, because a materialized view is an insert
--     trigger. It sees only rows written after it exists and it does not
--     backfill. Create it late and every span before it is invisible, with no
--     error anywhere — the symptom is a leaderboard of zeroes and a pipeline
--     that looks healthy.
--
-- The window between those two is why deploy/compose.yaml stands the whole
-- stack up before any registry is pointed at the collector, and why the backfill
-- at the bottom of this file exists for the deployment where that was missed.
--
-- tests/integration/clickhouse_test.go proves both halves: that this file fails
-- without otel_traces, and that a span recorded before the view lands in no
-- count while one recorded after it does.

-- ---------------------------------------------------------------------------
-- Verified is a Bool here and a string on the wire: OTLP attributes arrive as
-- strings in the exporter's map column, so the conversion happens once, in the
-- one place that knows the encoding, rather than in every query.
--
-- Hourly buckets are small enough for any window a page shows and large enough
-- that the table stays negligible — and they make a future sparkline a
-- GROUP BY Bucket rather than a new pipeline.
--
-- otel_traces is qualified. Unqualified it resolves against the view's own
-- database, which is the same database here and would not be in a deployment
-- that gives the collector one of its own.
-- ---------------------------------------------------------------------------
CREATE MATERIALIZED VIEW IF NOT EXISTS epos.epos_downloads_mv
TO epos.epos_downloads_total AS
SELECT
    SpanAttributes['repository']         AS Repository,
    SpanAttributes['verified'] = 'true'  AS Verified,
    toStartOfHour(Timestamp)             AS Bucket,
    count()                              AS Downloads
FROM epos.otel_traces
WHERE ServiceName = 'epos-registry' AND SpanName = 'epos.download'
GROUP BY Repository, Verified, Bucket;

-- ---------------------------------------------------------------------------
-- Backfill, for a deployment where the view was created after spans had already
-- landed. A materialized view does not backfill itself; run this once, and only
-- once, or the counts double.
--
--     INSERT INTO epos.epos_downloads_total
--     SELECT
--         SpanAttributes['repository']         AS Repository,
--         SpanAttributes['verified'] = 'true'  AS Verified,
--         toStartOfHour(Timestamp)             AS Bucket,
--         count()                              AS Downloads
--     FROM epos.otel_traces
--     WHERE ServiceName = 'epos-registry' AND SpanName = 'epos.download'
--       AND Timestamp < '<the moment the view was created>'
--     GROUP BY Repository, Verified, Bucket;
--
-- The bound is not optional. Without it the rows the view has already rolled up
-- are counted a second time, which is the failure this is meant to repair.
-- ---------------------------------------------------------------------------
