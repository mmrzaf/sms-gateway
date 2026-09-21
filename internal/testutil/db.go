// Package testutil provides helpers shared by tests that need PostgreSQL.
//
// Tests that call DB or EmptyDB run only when TEST_DATABASE_URL is set and are
// skipped otherwise, so "go test ./..." works without a database. Each call
// creates a fresh schema, which isolates tests from one another and lets them
// run in parallel against a single database.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mmrzaf/sms-gatway/internal/store"
	"github.com/mmrzaf/sms-gatway/migrations"
)

// DatabaseURLEnv names the variable that enables database tests.
const DatabaseURLEnv = "TEST_DATABASE_URL"

// SchemaURL creates a new, empty schema and returns a connection URL whose
// search_path points at it. The schema is dropped when the test ends.
func SchemaURL(t testing.TB) string {
	t.Helper()
	base := os.Getenv(DatabaseURLEnv)
	if base == "" {
		t.Skipf("%s is not set", DatabaseURLEnv)
	}
	ctx := context.Background()
	schema := "test_" + randomHex(8)

	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), base)
		if err != nil {
			t.Errorf("connect for cleanup: %v", err)
			return
		}
		defer conn.Close(context.Background())
		if _, err := conn.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop schema %s: %v", schema, err)
		}
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse %s: %v", DatabaseURLEnv, err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// EmptyDB returns a pool on a new, empty schema.
func EmptyDB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool, err := store.Open(context.Background(), SchemaURL(t), 20)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	// Registered after SchemaURL's cleanup, so it runs first: the pool is
	// closed before the schema is dropped.
	t.Cleanup(pool.Close)
	return pool
}

// DB returns a pool on a new schema with every migration applied.
func DB(t testing.TB) *pgxpool.Pool {
	t.Helper()
	pool := EmptyDB(t)
	list, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	if _, err := store.NewMigrator(pool, list).Up(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pool
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
