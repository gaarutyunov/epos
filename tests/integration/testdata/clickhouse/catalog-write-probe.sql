-- The catalog's credential attempting to write. The store must refuse it.
--
-- "The catalog only reads" is a grant, not a convention an implementation has
-- to remember, and this is how that claim is checked: run as epos_catalog, this
-- statement has to fail. It targets the one table that credential can see, so a
-- refusal is about the privilege and not about the table being invisible.
--
-- If this ever succeeds, deploy/clickhouse/01-schema.sql granted more than
-- SELECT and the separation in D4g is decoration.

INSERT INTO epos.epos_downloads_total (Repository, Verified, Bucket, Downloads)
VALUES ('demo/write-probe', true, toStartOfHour(now()), 1);
