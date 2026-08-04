-- A stand-in for the collector's own table, for this test and nowhere else.
--
-- deploy/clickhouse/ deliberately declares no part of otel_traces: it is the
-- clickhouseexporter's, created by the collector at startup, and epos neither
-- creates nor alters it. This file is not that declaration and must never be
-- copied into deploy/ — it exists so the rollup and the catalog's read path can
-- be exercised against a ClickHouse container without also standing up a
-- collector, which is a second image and a second protocol for the same four
-- columns.
--
-- Those four are the whole contract the rollup depends on, and they are the
-- part of this file that has to stay true to the real exporter:
--
--     Timestamp       DateTime64(9)
--     ServiceName     LowCardinality(String)
--     SpanName        LowCardinality(String)
--     SpanAttributes  Map(LowCardinality(String), String)
--
-- Everything the exporter also writes — trace and span ids, durations, status,
-- resource attributes, the TTL — is absent because the rollup reads none of it.
-- If a collector run ever shows a different name or type for one of the four,
-- this file and deploy/clickhouse/02-rollup.sql are both wrong and this test
-- will not have caught it. That is the known limit of testing the rollup this
-- way, and it is stated here rather than discovered.

CREATE TABLE IF NOT EXISTS epos.otel_traces
(
    Timestamp       DateTime64(9),
    ServiceName     LowCardinality(String),
    SpanName        LowCardinality(String),
    SpanAttributes  Map(LowCardinality(String), String)
)
ENGINE = MergeTree
ORDER BY (ServiceName, SpanName, toUnixTimestamp(Timestamp));
