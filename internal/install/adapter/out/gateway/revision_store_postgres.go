// Code scaffolded by sysgo; edit freely (not regenerated).

package gateway

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver "pgx"

	"github.com/gaarutyunov/epos/internal/install/app/port/out"
	"github.com/gaarutyunov/epos/internal/install/domain"
)

// PostgresRevisionStore is the durable PostgreSQL revision-history backend
// (SPEC §5.4, §11): each install/upgrade appends a self-contained revision
// bundle (version + digest + rendered files) as a row keyed by
// (release, target, namespace, number). It is "PostgreSQL at scale" — the
// large-catalog alternative to the git lockfile and in-cluster ConfigMap
// records — and implements the same RevisionRepository port.
type PostgresRevisionStore struct {
	db *sql.DB
	// retention bounds the retained revisions per release (0 = unbounded).
	retention int
}

var _ out.RevisionRepository = (*PostgresRevisionStore)(nil)

// NewPostgresRevisionStore opens a pool against dsn and ensures the schema.
// retention, when > 0, prunes the oldest revisions beyond the limit on Append.
func NewPostgresRevisionStore(ctx context.Context, dsn string, retention int) (*PostgresRevisionStore, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	s := NewPostgresRevisionStoreFromDB(db, retention)
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// NewPostgresRevisionStoreFromDB wraps an existing pool (the seam integration
// tests use to inject a pool aimed at a real instance).
func NewPostgresRevisionStoreFromDB(db *sql.DB, retention int) *PostgresRevisionStore {
	return &PostgresRevisionStore{db: db, retention: retention}
}

// Migrate creates the revisions table if it does not already exist.
func (s *PostgresRevisionStore) Migrate(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS revisions (
    release    TEXT NOT NULL,
    target     TEXT NOT NULL,
    namespace  TEXT NOT NULL,
    number     INTEGER NOT NULL,
    version    TEXT NOT NULL,
    digest     TEXT NOT NULL,
    bundle     TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (release, target, namespace, number)
)`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("postgres: migrate revisions: %w", err)
	}
	return nil
}

// RevisionStore persists a release's pending revisions (SPEC §5.3): each blob is
// decoded into its bundle and appended.
func (s *PostgresRevisionStore) RevisionStore(release domain.Release) (bool, error) {
	for _, rev := range release.Revisions {
		b, err := decodeBundle(rev.Blob)
		if err != nil {
			return false, err
		}
		if _, err := s.Append(release.Name, release.Target.Value, release.Namespace, b.Version, b.Digest, b.Files); err != nil {
			return false, err
		}
	}
	return true, nil
}

// Append records one revision bundle and returns its assigned number (the next
// integer after the current maximum for the release).
func (s *PostgresRevisionStore) Append(release, target, namespace, version, digest string, files map[string][]byte) (int, error) {
	ctx := context.Background()
	blob, err := encodeBundle(bundle{Version: version, Digest: digest, Files: files})
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var next int
	err = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(number), 0) + 1 FROM revisions WHERE release = $1 AND target = $2 AND namespace = $3`,
		release, target, namespace).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("postgres: next revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO revisions (release, target, namespace, number, version, digest, bundle) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		release, target, namespace, next, version, digest, blob); err != nil {
		return 0, fmt.Errorf("postgres: insert revision: %w", err)
	}
	if s.retention > 0 {
		// Keep only the last `retention` revisions; the threshold is computed in
		// Go so Postgres sees a single typed integer parameter (an in-SQL
		// `$n - $m` is type-ambiguous over the extended protocol).
		threshold := next - s.retention
		if _, err := tx.ExecContext(ctx, `
DELETE FROM revisions
WHERE release = $1 AND target = $2 AND namespace = $3
  AND number <= $4`,
			release, target, namespace, threshold); err != nil {
			return 0, fmt.Errorf("postgres: prune revisions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

// History returns the retained revisions of a release (oldest first).
func (s *PostgresRevisionStore) History(release, target, namespace string) ([]out.RevisionInfo, error) {
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT number, bundle FROM revisions WHERE release = $1 AND target = $2 AND namespace = $3 ORDER BY number ASC`,
		release, target, namespace)
	if err != nil {
		return nil, fmt.Errorf("postgres: history: %w", err)
	}
	defer rows.Close()

	var infos []out.RevisionInfo
	for rows.Next() {
		var number int
		var blob string
		if err := rows.Scan(&number, &blob); err != nil {
			return nil, err
		}
		info, err := infoFromBlob(number, blob)
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, rows.Err()
}

// Get returns a specific retained revision.
func (s *PostgresRevisionStore) Get(release, target, namespace string, number int) (out.RevisionInfo, error) {
	var blob string
	err := s.db.QueryRowContext(context.Background(),
		`SELECT bundle FROM revisions WHERE release = $1 AND target = $2 AND namespace = $3 AND number = $4`,
		release, target, namespace, number).Scan(&blob)
	if err == sql.ErrNoRows {
		return out.RevisionInfo{}, fmt.Errorf("revision %d of %q not found", number, release)
	}
	if err != nil {
		return out.RevisionInfo{}, fmt.Errorf("postgres: get revision: %w", err)
	}
	return infoFromBlob(number, blob)
}

// Delete removes a release's revision history.
func (s *PostgresRevisionStore) Delete(release, target, namespace string) error {
	if _, err := s.db.ExecContext(context.Background(),
		`DELETE FROM revisions WHERE release = $1 AND target = $2 AND namespace = $3`,
		release, target, namespace); err != nil {
		return fmt.Errorf("postgres: delete revisions: %w", err)
	}
	return nil
}

// Close releases the underlying pool.
func (s *PostgresRevisionStore) Close() error { return s.db.Close() }

func infoFromBlob(number int, blob string) (out.RevisionInfo, error) {
	b, err := decodeBundle(blob)
	if err != nil {
		return out.RevisionInfo{}, err
	}
	return out.RevisionInfo{Number: number, Version: b.Version, Digest: b.Digest, Files: b.Files}, nil
}
