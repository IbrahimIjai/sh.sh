package analytics

import (
	"context"
	"crypto/rand"
	"database/sql"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/database"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/lib/pq"
)

// Every run creates and removes its own schema; it never truncates existing data.
func recorderTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	admin, err := database.Open(dsn, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := "test_clicks_" + strings.ToLower(rand.Text())
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
	conn, err := database.Open(u.String(), 8, 8)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	migrations, err := filepath.Glob("../../db/migrations/*.sql")
	if err != nil || len(migrations) == 0 {
		t.Fatalf("find migrations: %v", err)
	}
	for _, path := range migrations {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(strings.Split(string(contents), "-- +goose Down")[0]); err != nil {
			t.Fatalf("apply %s: %v", path, err)
		}
	}
	return conn
}

func TestRecorderPostgres(t *testing.T) {
	conn := recorderTestDatabase(t)
	queries := db.New(conn)
	newLink := func(t *testing.T) int64 {
		t.Helper()
		var id int64
		if err := conn.QueryRow("INSERT INTO links (slug, target_url) VALUES ($1, 'https://example.com') RETURNING id", rand.Text()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	checkCounts := func(t *testing.T, id int64) {
		t.Helper()
		var total, humans, bots, counter int
		err := conn.QueryRow(`SELECT count(*), count(*) FILTER (WHERE NOT is_bot), count(*) FILTER (WHERE is_bot),
			(SELECT coalesce(max(human_clicks), 0) FROM link_counters WHERE link_id=$1) FROM clicks WHERE link_id=$1`, id).Scan(&total, &humans, &bots, &counter)
		if err != nil || total != 2 || humans != 1 || bots != 1 || counter != 1 {
			t.Fatalf("clicks/humans/bots/counter = %d/%d/%d/%d, err=%v", total, humans, bots, counter, err)
		}
	}

	for _, failure := range []string{"lost acknowledgement", "statement rollback"} {
		t.Run(failure, func(t *testing.T) {
			id := newLink(t)
			attempts := 0
			var duplicates int64
			store := clickStoreFunc(func(ctx context.Context, arg db.RecordClicksParams) (int64, error) {
				attempts++
				if attempts == 1 && failure == "statement rollback" {
					// Violate a real column constraint in the middle of the statement. The whole
					// statement, including the counter update, must roll back.
					arg.Countries = []string{"TOO-LONG", ""}
					_, err := queries.RecordClicks(ctx, arg)
					if err == nil {
						t.Error("expected a country length error")
					}
					return 0, err
				}
				inserted, err := queries.RecordClicks(ctx, arg)
				if err != nil {
					return 0, err
				}
				duplicates += int64(len(arg.LinkIds)) - inserted
				if attempts == 1 {
					// PostgreSQL committed, but the application sees a broken connection.
					return 0, io.ErrUnexpectedEOF
				}
				return inserted, nil
			})
			recorder := NewRecorder(store, RecorderConfig{BatchSize: 2, RetryMin: time.Millisecond})
			at := time.Date(2026, 9, 21, 23, 59, 59, 0, time.UTC)
			recorder.Record(Click{LinkID: id, CreatedAt: at, VisitorHash: "human"})
			recorder.Record(Click{LinkID: id, CreatedAt: at, IsBot: true})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := recorder.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if attempts != 2 {
				t.Fatalf("attempts = %d, want 2", attempts)
			}
			checkCounts(t, id)
			wantDuplicates := map[string]int64{"lost acknowledgement": 2, "statement rollback": 0}[failure]
			if duplicates != wantDuplicates {
				t.Fatalf("the retry found %d clicks already stored, want %d", duplicates, wantDuplicates)
			}
			var originalTimes int
			if err := conn.QueryRow("SELECT count(*) FROM clicks WHERE link_id=$1 AND created_at=$2 AND event_id IS NOT NULL", id, at).Scan(&originalTimes); err != nil || originalTimes != 2 {
				t.Fatalf("stored with original timestamps and event IDs = %d, err=%v", originalTimes, err)
			}
		})
	}

	t.Run("async commit store with two writers", func(t *testing.T) {
		id := newLink(t)
		recorder := NewRecorder(NewPostgresStore(conn, true), RecorderConfig{BatchSize: 2, Writers: 2})
		recorder.Record(Click{LinkID: id, VisitorHash: "human"})
		recorder.Record(Click{LinkID: id, IsBot: true})
		if err := recorder.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		checkCounts(t, id)
	})

	t.Run("concurrent replay", func(t *testing.T) {
		id := newLink(t)
		// Capture the real recorder payload, then submit the same batch concurrently.
		var batch db.RecordClicksParams
		recorder := NewRecorder(clickStoreFunc(func(_ context.Context, arg db.RecordClicksParams) (int64, error) {
			batch = arg
			return 2, nil
		}), RecorderConfig{BatchSize: 2})
		recorder.Record(Click{LinkID: id})
		recorder.Record(Click{LinkID: id, IsBot: true})
		if err := recorder.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		for range 8 {
			wg.Go(func() {
				if _, err := queries.RecordClicks(ctx, batch); err != nil {
					t.Errorf("replay: %v", err)
				}
			})
		}
		wg.Wait()
		checkCounts(t, id)
	})
}
