// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"

	"github.com/gaarutyunov/epos/internal/stats/app/port/out"
	"github.com/gaarutyunov/epos/internal/stats/domain"
)

// ClickHouseStatSink is the large-catalog stats backend (SPEC §10.1): every
// countable manifest GET is written as an append-only event row (skill, repo,
// reference, registry, timestamp), giving exact per-skill lifetime totals
// without polluting Prometheus. It implements the same StatSink port as the
// in-memory Prometheus-aggregate default.
type ClickHouseStatSink struct {
	db *sql.DB
}

var _ out.StatSink = (*ClickHouseStatSink)(nil)

// NewClickHouseStatSink opens a connection against dsn (a clickhouse:// DSN) and
// ensures the events table exists.
func NewClickHouseStatSink(ctx context.Context, dsn string) (*ClickHouseStatSink, error) {
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: parse dsn: %w", err)
	}
	db := clickhouse.OpenDB(opts)
	s := NewClickHouseStatSinkFromDB(db)
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// NewClickHouseStatSinkFromDB wraps an existing handle (the seam integration
// tests use to inject a handle aimed at a real instance).
func NewClickHouseStatSinkFromDB(db *sql.DB) *ClickHouseStatSink {
	return &ClickHouseStatSink{db: db}
}

// Migrate creates the pull_events table if it does not already exist.
func (s *ClickHouseStatSink) Migrate(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS pull_events (
    skill     String,
    repo      String,
    reference String,
    registry  String,
    ts        DateTime DEFAULT now()
) ENGINE = MergeTree
ORDER BY (skill, ts)`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("clickhouse: migrate pull_events: %w", err)
	}
	return nil
}

// StatSink records a counted pull as an event row (only manifest GETs are
// counted, SPEC §6.4) and returns the skill's current lifetime total. A request
// that is not a manifest GET (e.g. a read-statistics query) records nothing and
// only returns the current total.
func (s *ClickHouseStatSink) StatSink(request domain.CountRequest) (domain.CountSnapshot, error) {
	ctx := context.Background()
	ev := request.Event
	skill := lastSegment(ev.Repo)
	if ev.IsManifestGet {
		if err := s.insertEvent(ctx, skill, ev.Repo, ev.Reference); err != nil {
			return domain.CountSnapshot{}, err
		}
	}
	total, err := s.skillTotal(ctx, skill)
	if err != nil {
		return domain.CountSnapshot{}, err
	}
	return domain.CountSnapshot{Skill: skill, Total: total}, nil
}

// insertEvent appends one event row using the ClickHouse native batch protocol
// (Begin → Prepare → Exec → Commit), the std-driver contract for inserts. The
// argument order matches the table's column order (skill, repo, reference,
// registry, ts).
func (s *ClickHouseStatSink) insertEvent(ctx context.Context, skill, repo, reference string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("clickhouse: begin: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO pull_events")
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clickhouse: prepare insert: %w", err)
	}
	if _, err := stmt.ExecContext(ctx, skill, repo, reference, "", time.Now()); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clickhouse: record event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("clickhouse: commit event: %w", err)
	}
	return nil
}

func (s *ClickHouseStatSink) skillTotal(ctx context.Context, skill string) (int64, error) {
	var total uint64
	if err := s.db.QueryRowContext(ctx,
		`SELECT count() FROM pull_events WHERE skill = ?`, skill).Scan(&total); err != nil {
		return 0, fmt.Errorf("clickhouse: skill total: %w", err)
	}
	return int64(total), nil
}

// Close releases the underlying handle.
func (s *ClickHouseStatSink) Close() error { return s.db.Close() }
