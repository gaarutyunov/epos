-- The catalog's store: two objects epos owns, and three principals.
--
-- Applied by a human or a bootstrap step BEFORE the collector's first insert.
-- That ordering is not a preference: a ClickHouse materialized view is an
-- insert trigger and sees only rows written after it exists. Create it late and
-- every span before it is invisible, with no error anywhere — the symptom is a
-- leaderboard of zeroes and a pipeline that looks healthy. The backfill for
-- that case is at the bottom of this file.
--
-- epos writes no ClickHouse code at all. This file and the collector's YAML
-- beside it are configuration, in the sense this repository already uses the
-- word for .goreleaser.yaml and .golangci.yml: reviewed, pinned and diffable.
-- Nothing in Go creates, alters or inserts into any of it.

CREATE DATABASE IF NOT EXISTS epos;

-- ---------------------------------------------------------------------------
-- 1. otel_traces is the collector's, and is declared nowhere here.
--
-- The clickhouseexporter creates it on first use, or an operator provisions it
-- and sets create_schema: false (which the exporter's own README recommends in
-- production). epos neither creates nor alters it. The columns the rollup below
-- reads are Timestamp, ServiceName, SpanName and
-- SpanAttributes Map(LowCardinality(String), String).
--
-- It is also written with a TTL, which is correct for spans and fatal for a
-- lifetime counter. That is the whole reason the rollup exists rather than the
-- catalog querying spans directly: a count taken from otel_traces would quietly
-- shrink the day the TTL first fired, which is the sort of defect nobody finds
-- until it has been wrong for a month.
-- ---------------------------------------------------------------------------

-- ---------------------------------------------------------------------------
-- 2. The catalog's table. The only thing the catalog queries.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS epos.epos_downloads_total
(
    Repository  LowCardinality(String),
    Verified    Bool,
    Bucket      DateTime,   -- start of the hour, UTC
    Downloads   UInt64
)
ENGINE = SummingMergeTree
PARTITION BY toYYYYMM(Bucket)
ORDER BY (Repository, Verified, Bucket);

-- READ THIS BEFORE WRITING A QUERY AGAINST THE TABLE ABOVE.
--
-- SummingMergeTree collapses rows *eventually*, in background merges. Selecting
-- Downloads without sum() returns whatever the merge state happens to be: right
-- on an idle demo, wrong under load, and wrong in a way that looks like a
-- plausible smaller number. The aggregation belongs in every query, always.
--
-- No TTL, deliberately. This table is what outlives the spans.

-- ---------------------------------------------------------------------------
-- 3. The rollup that fills it.
--
-- Verified is a Bool here and a string on the wire: OTLP attributes arrive as
-- strings in the exporter's map column, so the conversion happens once, in the
-- one place that knows the encoding, rather than in every query.
--
-- Hourly buckets are small enough for any window a page shows and large enough
-- that the table stays negligible — and they make a future sparkline a
-- GROUP BY Bucket rather than a new pipeline.
-- ---------------------------------------------------------------------------
CREATE MATERIALIZED VIEW IF NOT EXISTS epos.epos_downloads_mv
TO epos.epos_downloads_total AS
SELECT
    SpanAttributes['repository']         AS Repository,
    SpanAttributes['verified'] = 'true'  AS Verified,
    toStartOfHour(Timestamp)             AS Bucket,
    count()                              AS Downloads
FROM otel_traces
WHERE ServiceName = 'epos-registry' AND SpanName = 'epos.download'
GROUP BY Repository, Verified, Bucket;

-- ---------------------------------------------------------------------------
-- 4. Three principals, three privileges.
--
-- This is a security property rather than tidiness, and it is declared here so
-- that it is reviewable in one place:
--
--   epos-registry (relay)  an OTLP endpoint. No database credential at all.
--   the collector          INSERT on the collector's own tables.
--   the catalog            SELECT on one table, and nothing else.
--
-- A compromise of the relay yields an OTLP endpoint, not a database — and the
-- relay is the process on the public internet answering unauthenticated GETs.
-- "The catalog only reads" stops being a rule an implementer has to remember
-- and becomes one the database enforces.
--
-- Replace every '...' below before applying. Nothing in this repository holds
-- these credentials and nothing generates them.
-- ---------------------------------------------------------------------------

CREATE USER IF NOT EXISTS epos_collector IDENTIFIED BY '...';
GRANT INSERT ON epos.* TO epos_collector;
GRANT CREATE TABLE ON epos.* TO epos_collector;  -- only with create_schema: true

-- readonly alone is not enough, and that is why the profile is bounded: a
-- read-only user can still issue a query expensive enough to be a denial of
-- service against the store the relay shares a deployment with.
CREATE SETTINGS PROFILE IF NOT EXISTS epos_catalog_readonly SETTINGS
    readonly = 1,
    max_execution_time = 10,
    max_result_rows = 100000,
    max_memory_usage = 1000000000;

CREATE USER IF NOT EXISTS epos_catalog IDENTIFIED BY '...'
    SETTINGS PROFILE 'epos_catalog_readonly';
GRANT SELECT ON epos.epos_downloads_total TO epos_catalog;

-- ---------------------------------------------------------------------------
-- 5. The catalog's query, in full, because defining the schema means the read
--    side too.
--
--     SELECT Repository, Verified, sum(Downloads) AS Downloads
--     FROM epos.epos_downloads_total
--     WHERE Repository IN ? AND Bucket >= ?
--     GROUP BY Repository, Verified;
--
-- 6. Backfill, for a deployment where the view was created after spans had
--    already landed. A materialized view does not backfill itself; run this
--    once, and only once, or the counts double.
--
--     INSERT INTO epos.epos_downloads_total
--     SELECT
--         SpanAttributes['repository']         AS Repository,
--         SpanAttributes['verified'] = 'true'  AS Verified,
--         toStartOfHour(Timestamp)             AS Bucket,
--         count()                              AS Downloads
--     FROM otel_traces
--     WHERE ServiceName = 'epos-registry' AND SpanName = 'epos.download'
--       AND Timestamp < '<the moment the view was created>'
--     GROUP BY Repository, Verified, Bucket;
-- ---------------------------------------------------------------------------
