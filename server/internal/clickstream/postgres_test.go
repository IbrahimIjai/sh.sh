package clickstream

import (
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ibrahimijai/sh.sh/internal/analytics"
	"github.com/ibrahimijai/sh.sh/internal/database"
	"github.com/lib/pq"
	"github.com/redis/go-redis/v9"
)

// Every run uses its own schema and stream, and removes them afterwards.
func testDatabase(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	admin, err := database.Open(dsn, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	schema := "test_stream_" + strings.ToLower(rand.Text())
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

func TestStreamStoresEachClickOnceInPostgres(t *testing.T) {
	dsn, redisURL := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_REDIS_URL")
	if dsn == "" || redisURL == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_REDIS_URL to run the stream against PostgreSQL and Redis")
	}
	conn := testDatabase(t, dsn)
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	inspect := redis.NewClient(opts)
	stream := "test:clicks:" + strings.ToLower(rand.Text())
	t.Cleanup(func() {
		inspect.Del(context.Background(), stream, stream+":dead")
		inspect.Close()
	})

	var links []int64
	for range 3 {
		var id int64
		if err := conn.QueryRow("INSERT INTO links (slug, target_url) VALUES ($1, 'https://example.com') RETURNING id", rand.Text()).Scan(&id); err != nil {
			t.Fatal(err)
		}
		links = append(links, id)
	}
	store := analytics.NewPostgresStore(conn, true)
	fallback := analytics.NewRecorder(store, analytics.RecorderConfig{})
	opts.ContextTimeoutEnabled = true
	q := newQueue(redis.NewClient(opts), store, fallback, Config{ClaimIdle: 100 * time.Millisecond, BatchSize: 50})
	q.stream, q.dead = stream, stream+":dead"
	q.claimEvery, q.checkEvery = 50*time.Millisecond, 50*time.Millisecond

	// A crashed instance wrote this entry to PostgreSQL but never acknowledged it.
	now := time.Now().Truncate(time.Microsecond)
	crashed := []analytics.Click{
		{EventID: analytics.NewEventID(), CreatedAt: now, LinkID: links[0], VisitorHash: "crashed"},
		{EventID: analytics.NewEventID(), CreatedAt: now, LinkID: links[0], IsBot: true},
	}
	payload, err := encode(crashed)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspect.XGroupCreateMkStream(ctx, stream, group, "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := inspect.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: []any{"v", wireVersion, "d", payload}}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := inspect.XReadGroup(ctx, &redis.XReadGroupArgs{Group: group, Consumer: "crashed", Streams: []string{stream, ">"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordClicks(ctx, analytics.BatchParams(crashed)); err != nil {
		t.Fatal(err)
	}

	q.startProducer()
	q.startConsumers()
	humans := map[int64]int{links[0]: 1}
	total := len(crashed)
	for i := range 300 {
		link := links[i%len(links)]
		bot := i%10 == 0
		q.Record(analytics.Click{LinkID: link, IsBot: bot, VisitorHash: fmt.Sprint(i)})
		if !bot {
			humans[link]++
		}
		total++
	}
	eventually(t, "every click stored", func() bool {
		var n int
		return conn.QueryRow("SELECT count(*) FROM clicks").Scan(&n) == nil && n == total
	})
	eventually(t, "the stream emptied", func() bool { return streamLength(t, inspect, stream) == 0 })

	closeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := q.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err := fallback.Close(closeCtx); err != nil {
		t.Fatal(err)
	}

	var rows, events int
	if err := conn.QueryRow("SELECT count(*), count(DISTINCT event_id) FROM clicks").Scan(&rows, &events); err != nil {
		t.Fatal(err)
	}
	if rows != total || events != total {
		t.Fatalf("stored %d rows with %d distinct events, want %d of each", rows, events, total)
	}
	for _, link := range links {
		var counter int
		if err := conn.QueryRow("SELECT human_clicks FROM link_counters WHERE link_id = $1", link).Scan(&counter); err != nil {
			t.Fatal(err)
		}
		if counter != humans[link] {
			t.Errorf("link %d counter = %d, want %d", link, counter, humans[link])
		}
	}
	pending, err := inspect.XPending(ctx, stream, group).Result()
	if err != nil || pending.Count != 0 {
		t.Errorf("pending = %+v, err = %v; want nothing pending", pending, err)
	}
	if s := q.Stats(); s.Claimed != 1 || s.Duplicates != 2 || s.Written != uint64(total) || s.Fallback != 0 {
		t.Errorf("stats = %+v; want the crashed entry taken over and skipped, everything else written once", s)
	}
}
