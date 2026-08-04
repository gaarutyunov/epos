-- The catalog's store, part one: everything that does not read otel_traces.
--
-- Applied BEFORE the collector starts. It creates the database the collector
-- writes into, the table the catalog reads, and the three principals — so the
-- collector has a user to connect as, and the catalog has one that cannot
-- write.
--
-- The rollup is deliberately not here; it is 02-rollup.sql, and the split is
-- not tidiness. A ClickHouse materialized view is created over a source table
-- that must already exist, and otel_traces is the collector's: it does not
-- exist until the collector has started. Applying this file and the rollup as
-- one unit before the collector fails outright with UNKNOWN_TABLE.
-- tests/integration/clickhouse_test.go asserts that failure rather than leaving
-- it as a claim, because the ordering is the part of this deployment most
-- likely to be "simplified" back into one file by someone who did not hit it.
--
-- epos writes no ClickHouse code at all. This file, its sibling and the
-- collector's YAML beside them are configuration, in the sense this repository
-- already uses the word for .goreleaser.yaml and .golangci.yml: reviewed,
-- pinned and diffable. Nothing in Go creates, alters or inserts into any of it.

CREATE DATABASE IF NOT EXISTS epos;

-- ---------------------------------------------------------------------------
-- 1. otel_traces is the collector's, and is declared nowhere here.
--
-- The clickhouseexporter creates it, or an operator provisions it and sets
-- create_schema: false (which the exporter's own README recommends in
-- production). epos neither creates nor alters it. The columns the rollup in
-- 02-rollup.sql reads are Timestamp, ServiceName, SpanName and
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
-- 3. Three principals, three privileges.
--
-- This is a security property rather than tidiness, and it is declared here so
-- that it is reviewable in one place:
--
--   epos-registry (relay)  an OTLP endpoint. No database credential at all.
--   the collector          writes its own tables, and nothing else reads.
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

-- CREATE and SELECT travel with create_schema: true, and only with it. The
-- traces exporter provisions otel_traces, a trace-id lookup table and a
-- materialized view between them, and that view reads otel_traces on every
-- insert. Set create_schema: false, provision those tables yourself and the
-- grant above is enough on its own — which is what the exporter's README
-- recommends in production, and what narrows the collector to a pure writer.
GRANT CREATE, SELECT ON epos.* TO epos_collector;

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
-- 4. The catalog's query, in full, because defining the schema means the read
--    side too. internal/catalog/clickhouse.go carries it as a constant and a
--    test holds the two against each other, so a change here that is not made
--    there fails the build rather than the leaderboard.
--
--     SELECT Repository, Verified, sum(Downloads) AS Downloads
--     FROM epos.epos_downloads_total
--     WHERE Repository IN ? AND Bucket >= ?
--     GROUP BY Repository, Verified
-- ---------------------------------------------------------------------------
