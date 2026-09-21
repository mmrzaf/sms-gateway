package store_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mmrzaf/sms-gatway/internal/store"
	"github.com/mmrzaf/sms-gatway/internal/testutil"
	"github.com/mmrzaf/sms-gatway/migrations"
)

func TestLoadMigrations(t *testing.T) {
	fsys := fstest.MapFS{
		"00002_second.sql": {Data: []byte("-- migrate:up\nCREATE TABLE b (id int);\n-- migrate:down\nDROP TABLE b;")},
		"00001_first.sql":  {Data: []byte("-- migrate:up\nCREATE TABLE a (id int);")},
		"README.txt":       {Data: []byte("ignored")},
	}
	list, err := store.LoadMigrations(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Version != 1 || list[1].Version != 2 {
		t.Fatalf("unexpected order: %+v", list)
	}
	if list[0].Name != "first" || list[0].Down != "" {
		t.Errorf("first migration parsed as %+v", list[0])
	}
	if list[1].Up != "CREATE TABLE b (id int);" || list[1].Down != "DROP TABLE b;" {
		t.Errorf("second migration parsed as %+v", list[1])
	}
}

func TestLoadMigrationsRejectsInvalidFiles(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"bad name":   {"first.sql": {Data: []byte("-- migrate:up\nSELECT 1;")}},
		"missing up": {"00001_a.sql": {Data: []byte("SELECT 1;")}},
		"empty up":   {"00001_a.sql": {Data: []byte("-- migrate:up\n-- migrate:down\nSELECT 1;")}},
		"duplicate version": {
			"00001_a.sql": {Data: []byte("-- migrate:up\nSELECT 1;")},
			"1_b.sql":     {Data: []byte("-- migrate:up\nSELECT 1;")},
		},
	}
	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := store.LoadMigrations(fsys); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	list, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) == 0 {
		t.Fatal("no migrations embedded")
	}
	for _, m := range list {
		if m.Down == "" {
			t.Errorf("migration %d_%s has no down section", m.Version, m.Name)
		}
	}
}

func TestMigratorUpDownStatus(t *testing.T) {
	pool := testutil.EmptyDB(t)
	ctx := context.Background()
	list, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	m := store.NewMigrator(pool, list)

	applied, err := m.Up(ctx)
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if len(applied) != len(list) {
		t.Fatalf("applied %d migrations, want %d", len(applied), len(list))
	}

	again, err := m.Up(ctx)
	if err != nil || len(again) != 0 {
		t.Fatalf("second up applied %d migrations, err %v; want none", len(again), err)
	}

	status, err := m.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range status {
		if s.AppliedAt == nil {
			t.Errorf("migration %d reported as pending after up", s.Version)
		}
	}

	for _, table := range []string{"customers", "messages", "transactions", "queue", "workers"} {
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil || !exists {
			t.Errorf("table %s missing after up (err %v)", table, err)
		}
	}

	for range list {
		if _, err := m.Down(ctx); err != nil {
			t.Fatalf("down: %v", err)
		}
	}
	reverted, err := m.Down(ctx)
	if err != nil || reverted != nil {
		t.Fatalf("down with nothing applied: got %v, %v", reverted, err)
	}
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('customers') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Errorf("customers still exists after full down (err %v)", err)
	}
}

func TestMigratorReportsFailingMigration(t *testing.T) {
	pool := testutil.EmptyDB(t)
	m := store.NewMigrator(pool, []store.Migration{
		{Version: 1, Name: "ok", Up: "CREATE TABLE ok_table (id int)"},
		{Version: 2, Name: "broken", Up: "CREATE TABLE ok_table (id int)"},
	})
	applied, err := m.Up(context.Background())
	if err == nil || !strings.Contains(err.Error(), "2_broken") {
		t.Fatalf("expected failure naming 2_broken, got %v", err)
	}
	if len(applied) != 1 {
		t.Errorf("applied %d migrations before the failure, want 1", len(applied))
	}
}
