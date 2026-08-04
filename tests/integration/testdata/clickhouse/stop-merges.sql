-- Hold the SummingMergeTree's parts apart, so the sum() trap is demonstrable
-- rather than a race.
--
-- The table collapses rows in background merges, on its own schedule. Left
-- alone, a test that inserts twice and then reads the stored Downloads column
-- gets the right answer if a merge happened to have run and the wrong one if it
-- did not — which is a flaky test asserting a real defect, the worst of both.
-- Stopping merges makes "there are two partial rows" a fact of the fixture, and
-- the wrong answer reproducible.
--
-- This is also exactly the state a busy deployment is in most of the time. The
-- demo where reading without sum() looks correct is the idle one.

SYSTEM STOP MERGES epos.epos_downloads_total;
