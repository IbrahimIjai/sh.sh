package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ibrahimijai/sh.sh/db/migrations"
	"github.com/lib/pq"
)

func migrationTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	admin, err := Open(dsn, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := "test_migrate_" + strings.ToLower(rand.Text())
	if _, err := admin.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE"); err != nil {
			t.Errorf("remove test schema: %v", err)
		}
	})
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	conn, err := Open(u.String(), 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestMigrationsUpDownUp(t *testing.T) {
	conn := migrationTestDatabase(t)
	ctx := context.Background()
	files, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("embedded migrations: %v", err)
	}
	latest := int64(len(files))

	applied, version, err := Migrate(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != len(files) || version != latest {
		t.Fatalf("fresh database: applied %d, version %d; want %d and %d", len(applied), version, len(files), latest)
	}
	for _, table := range []string{"links", "clicks", "link_counters", "click_daily"} {
		var found sql.NullString
		if err := conn.QueryRow("SELECT to_regclass($1)::text", table).Scan(&found); err != nil || !found.Valid {
			t.Errorf("table %s missing after migrating: %v", table, err)
		}
	}

	applied, version, err = Migrate(ctx, conn)
	if err != nil || len(applied) != 0 || version != latest {
		t.Fatalf("second run: applied %v, version %d, err %v; want nothing applied", applied, version, err)
	}

	provider, err := newMigrator(conn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("roll back every migration: %v", err)
	}
	applied, version, err = Migrate(ctx, conn)
	if err != nil || len(applied) != len(files) || version != latest {
		t.Fatalf("after rollback: applied %d, version %d, err %v; want everything reapplied", len(applied), version, err)
	}
}
