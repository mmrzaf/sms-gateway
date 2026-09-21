package store

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration is one versioned schema change, read from a file named
// <version>_<name>.sql that contains a "-- migrate:up" section and an
// optional "-- migrate:down" section.
type Migration struct {
	Version int64
	Name    string
	Up      string
	Down    string
}

// MigrationStatus is a migration and, if applied, when it was applied.
type MigrationStatus struct {
	Migration
	AppliedAt *time.Time
}

const (
	markerUp   = "-- migrate:up"
	markerDown = "-- migrate:down"

	// migrationLockKey serializes concurrent migration runs across processes.
	migrationLockKey = 72_413_205_117
)

var migrationFilePattern = regexp.MustCompile(`^(\d+)_([a-z0-9_]+)\.sql$`)

// LoadMigrations reads all migrations from the root of fsys, ordered by version.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	var list []Migration
	seen := make(map[int64]string)
	for _, e := range entries {
		if e.IsDir() || path.Ext(e.Name()) != ".sql" {
			continue
		}
		m := migrationFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %s: name must be <version>_<name>.sql", e.Name())
		}
		version, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("migrations %s and %s share version %d", other, e.Name(), version)
		}
		seen[version] = e.Name()

		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		up, down, err := splitMigration(string(body))
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", e.Name(), err)
		}
		list = append(list, Migration{Version: version, Name: m[2], Up: up, Down: down})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Version < list[j].Version })
	return list, nil
}

func splitMigration(body string) (up, down string, err error) {
	upAt := strings.Index(body, markerUp)
	if upAt < 0 {
		return "", "", fmt.Errorf("missing %q section", markerUp)
	}
	rest := body[upAt+len(markerUp):]
	if downAt := strings.Index(rest, markerDown); downAt >= 0 {
		up, down = rest[:downAt], rest[downAt+len(markerDown):]
	} else {
		up = rest
	}
	up, down = strings.TrimSpace(up), strings.TrimSpace(down)
	if up == "" {
		return "", "", fmt.Errorf("empty %q section", markerUp)
	}
	return up, down, nil
}

// Migrator applies and reverts migrations. Each migration runs in its own
// transaction, and a session-level advisory lock makes concurrent runs safe.
type Migrator struct {
	pool       *pgxpool.Pool
	migrations []Migration
}

// NewMigrator returns a Migrator for the given ordered migrations.
func NewMigrator(pool *pgxpool.Pool, migrations []Migration) *Migrator {
	return &Migrator{pool: pool, migrations: migrations}
}

// Up applies every pending migration and returns those it applied.
func (m *Migrator) Up(ctx context.Context) ([]Migration, error) {
	var applied []Migration
	err := m.locked(ctx, func(conn *pgxpool.Conn, done map[int64]time.Time) error {
		for _, mig := range m.migrations {
			if _, ok := done[mig.Version]; ok {
				continue
			}
			err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, mig.Up); err != nil {
					return err
				}
				_, err := tx.Exec(ctx,
					`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`,
					mig.Version, mig.Name)
				return err
			})
			if err != nil {
				return fmt.Errorf("apply migration %d_%s: %w", mig.Version, mig.Name, err)
			}
			applied = append(applied, mig)
		}
		return nil
	})
	return applied, err
}

// Down reverts the most recently applied migration. It returns nil if no
// migration is applied.
func (m *Migrator) Down(ctx context.Context) (*Migration, error) {
	var reverted *Migration
	err := m.locked(ctx, func(conn *pgxpool.Conn, done map[int64]time.Time) error {
		for i := len(m.migrations) - 1; i >= 0; i-- {
			mig := m.migrations[i]
			if _, ok := done[mig.Version]; !ok {
				continue
			}
			if mig.Down == "" {
				return fmt.Errorf("migration %d_%s has no %q section", mig.Version, mig.Name, markerDown)
			}
			err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, mig.Down); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, mig.Version)
				return err
			})
			if err != nil {
				return fmt.Errorf("revert migration %d_%s: %w", mig.Version, mig.Name, err)
			}
			reverted = &mig
			return nil
		}
		return nil
	})
	return reverted, err
}

// Status lists every known migration with its applied time, if any.
func (m *Migrator) Status(ctx context.Context) ([]MigrationStatus, error) {
	var list []MigrationStatus
	err := m.locked(ctx, func(_ *pgxpool.Conn, done map[int64]time.Time) error {
		for _, mig := range m.migrations {
			s := MigrationStatus{Migration: mig}
			if at, ok := done[mig.Version]; ok {
				s.AppliedAt = &at
			}
			list = append(list, s)
		}
		return nil
	})
	return list, err
}

// locked runs fn on one connection that holds the migration lock, after
// ensuring the bookkeeping table exists and loading the applied versions.
func (m *Migrator) locked(ctx context.Context, fn func(*pgxpool.Conn, map[int64]time.Time) error) error {
	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(migrationLockKey)); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer func() {
		// The lock is released with the session if this fails, so the error is not actionable.
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, int64(migrationLockKey))
	}()

	if _, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		    version     BIGINT PRIMARY KEY,
		    name        TEXT NOT NULL,
		    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := conn.Query(ctx, `SELECT version, applied_at FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	done := make(map[int64]time.Time)
	var version int64
	var at time.Time
	if _, err := pgx.ForEachRow(rows, []any{&version, &at}, func() error {
		done[version] = at
		return nil
	}); err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	return fn(conn, done)
}
