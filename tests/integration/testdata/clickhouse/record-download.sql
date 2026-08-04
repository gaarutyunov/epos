-- One download span, written the way the collector writes one.
--
-- Parameterised rather than generated in Go, and that is the point: no Go file
-- in this repository carries a statement that changes the store, so the
-- assertion in cmd/epos-registry/imports_test.go stays a real guard instead of
-- one with a test-shaped hole in it. The test supplies --param_repository and
-- --param_verified.
--
-- verified is a string here because it is a string on the wire: OTLP attributes
-- arrive in the exporter's map column as strings, and turning it into a Bool is
-- the rollup's job, done once. Passing 'true'/'false' is therefore fidelity to
-- the real pipeline rather than laziness about types.

INSERT INTO epos.otel_traces (Timestamp, ServiceName, SpanName, SpanAttributes)
SELECT
    now64(9),
    'epos-registry',
    'epos.download',
    map('repository', {repository:String}, 'verified', {verified:String});
