// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"

	"github.com/gaarutyunov/epos/internal/registry/app/port/out"
	"github.com/gaarutyunov/epos/internal/registry/domain"
)

// defaultIndexID keys the singleton registration index when the aggregate
// carries no ID of its own.
const defaultIndexID = "default"

// PostgresRegistrationStore is the durable PostgreSQL registration-index backend
// (SPEC §8.2, §11): the whole index aggregate is persisted as one JSON row so
// runtime registrations (added via API/UI) survive a restart. It implements the
// same RegistrationStore port as the in-memory default.
type PostgresRegistrationStore struct {
	db *sql.DB
}

var _ out.RegistrationStore = (*PostgresRegistrationStore)(nil)

// NewPostgresRegistrationStore opens a connection pool against dsn (a libpq /
// pgx DSN) and ensures the schema exists.
func NewPostgresRegistrationStore(ctx context.Context, dsn string) (*PostgresRegistrationStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	s := NewPostgresRegistrationStoreFromDB(db)
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// NewPostgresRegistrationStoreFromDB wraps an existing pool (the seam integration
// tests use to inject a pool aimed at a real instance).
func NewPostgresRegistrationStoreFromDB(db *sql.DB) *PostgresRegistrationStore {
	return &PostgresRegistrationStore{db: db}
}

// Migrate creates the registration_index table if it does not already exist.
func (s *PostgresRegistrationStore) Migrate(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS registration_index (
    id         TEXT PRIMARY KEY,
    data       JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("postgres: migrate registration_index: %w", err)
	}
	return nil
}

// RegistrationStore upserts the whole registration index as one JSON row, keyed
// by the aggregate's ID (SPEC §11).
func (s *PostgresRegistrationStore) RegistrationStore(index domain.RegistrationIndex) (bool, error) {
	data, err := json.Marshal(index)
	if err != nil {
		return false, err
	}
	const q = `
INSERT INTO registration_index (id, data, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (id) DO UPDATE SET data = EXCLUDED.data, updated_at = now()`
	if _, err := s.db.ExecContext(context.Background(), q, indexID(index.ID), data); err != nil {
		return false, fmt.Errorf("postgres: store index: %w", err)
	}
	return true, nil
}

// Index loads the most recently persisted registration index (empty if none has
// been stored yet). Errors are swallowed to mirror the in-memory adapter's
// signature; use Load for explicit error handling.
func (s *PostgresRegistrationStore) Index() domain.RegistrationIndex {
	idx, _ := s.Load()
	return idx
}

// Load reads the most recently updated registration index from the store.
func (s *PostgresRegistrationStore) Load() (domain.RegistrationIndex, error) {
	const q = `SELECT data FROM registration_index ORDER BY updated_at DESC LIMIT 1`
	var data []byte
	switch err := s.db.QueryRowContext(context.Background(), q).Scan(&data); err {
	case nil:
	case sql.ErrNoRows:
		return domain.RegistrationIndex{}, nil
	default:
		return domain.RegistrationIndex{}, fmt.Errorf("postgres: load index: %w", err)
	}
	var idx domain.RegistrationIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		return domain.RegistrationIndex{}, err
	}
	return idx, nil
}

// Close releases the underlying pool.
func (s *PostgresRegistrationStore) Close() error { return s.db.Close() }

func indexID(id string) string {
	if id == "" {
		return defaultIndexID
	}
	return id
}
