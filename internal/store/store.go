// Package store owns every Postgres query and the schema migrations.
package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	Pool *pgxpool.Pool
}

// Open connects with retries, because containers start before Postgres is ready.
func Open(ctx context.Context, url string) (*Store, error) {
	var lastErr error
	for attempt := 0; attempt < 30; attempt++ {
		pool, err := pgxpool.New(ctx, url)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return &Store{Pool: pool}, nil
			}
			pool.Close()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
	return nil, fmt.Errorf("postgres not reachable: %w", lastErr)
}

func (s *Store) Close() { s.Pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

// migrateLockID is an arbitrary constant; any two processes migrating at once
// serialise on this advisory lock, so each migration runs exactly once.
const migrateLockID = 7_201_601

// Migrate applies every embedded migration that has not run yet, in filename order.
func (s *Store) Migrate(ctx context.Context) (applied []string, err error) {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	if _, err = conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateLockID); err != nil {
		return nil, err
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrateLockID)

	if _, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}

	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		var done bool
		if err = conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
			return applied, err
		}
		if done {
			continue
		}
		sql, err := migrationFS.ReadFile(name)
		if err != nil {
			return applied, err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return applied, err
		}
		if _, err = tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			return applied, fmt.Errorf("migration %s: %w", version, err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx)
			return applied, err
		}
		if err = tx.Commit(ctx); err != nil {
			return applied, err
		}
		applied = append(applied, version)
	}
	return applied, nil
}
