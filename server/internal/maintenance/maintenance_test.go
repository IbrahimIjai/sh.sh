package maintenance

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

	"github.com/ibrahimijai/sh.sh/internal/database"
	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/lib/pq"
)

// testDatabase applies every migration in a schema of its own, removed afterwards.
func testDatabase(t *testing.T) *sql.DB {
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
	schema := "test_maintenance_" + strings.ToLower(rand.Text())
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

func TestMaintenancePass(t *testing.T) {
	conn := testDatabase(t)
	ctx := context.Background()
	queries := db.New(conn)
	now := time.Now().UTC()
	today := dayOf(now)
	day := func(offset int) time.Time { return today.AddDate(0, 0, offset) }

	var linkID int64
	if err := conn.QueryRow("INSERT INTO links (slug, target_url) VALUES ('m', 'https://example.com') RETURNING id").Scan(&linkID); err != nil {
		t.Fatal(err)
	}
	old := day(-40)
	if _, err := conn.Exec(fmt.Sprintf("CREATE TABLE %s PARTITION OF clicks FOR VALUES FROM ('%s') TO ('%s')",
		pq.QuoteIdentifier(partitionPrefix+old.Format(dayLayout)), old.Format(time.RFC3339), old.AddDate(0, 0, 1).Format(time.RFC3339))); err != nil {
		t.Fatal(err)
	}
	click := func(at time.Time, visitor, source string, bot bool) {
		t.Helper()
		if _, err := conn.Exec(`INSERT INTO clicks (created_at, link_id, visitor_hash, source, is_bot) VALUES ($1, $2, $3, $4, $5)`,
			at, linkID, visitor, source, bot); err != nil {
			t.Fatal(err)
		}
	}
	click(day(-1).Add(time.Hour), "v1", "X (Twitter)", false)
	click(day(-1).Add(2*time.Hour), "v1", "X (Twitter)", false)
	click(day(-1).Add(3*time.Hour), "v2", "", false)
	click(day(-1).Add(4*time.Hour), "bot", "", true)
	click(day(-3).Add(time.Hour), "v3", "Google", false) // default partition
	click(day(-3).Add(2*time.Hour), "v4", "Google", false)
	click(old.Add(time.Hour), "v5", "Google", false)
	click(today.Add(time.Minute), "v6", "Google", false)

	m := New(conn, Config{RawRetentionDays: 30, LookbackDays: 60})
	m.now = func() time.Time { return now }
	expected, err := queries.GetClickTotalsByLink(ctx, db.GetClickTotalsByLinkParams{LinkID: linkID, CreatedAt: day(-45)})
	if err != nil {
		t.Fatal(err)
	}

	report, err := m.Run(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.DaysRolled) != 60 {
		t.Errorf("rolled %d days, want every closed day in the 60-day lookback", len(report.DaysRolled))
	}
	if len(report.PartitionsDropped) != 1 || report.PartitionsDropped[0] != partitionPrefix+old.Format(dayLayout) {
		t.Errorf("dropped %v, want only the 40-day-old partition", report.PartitionsDropped)
	}

	var human, bots, visitors int64
	if err := conn.QueryRow("SELECT human, bots, visitors FROM click_daily WHERE link_id=$1 AND day=$2", linkID, day(-1)).Scan(&human, &bots, &visitors); err != nil {
		t.Fatal(err)
	}
	if human != 3 || bots != 1 || visitors != 2 {
		t.Errorf("yesterday rolled up as %d/%d/%d, want 3 human, 1 bot, 2 visitors", human, bots, visitors)
	}
	var twitter int64
	if err := conn.QueryRow("SELECT clicks FROM click_daily_dims WHERE link_id=$1 AND day=$2 AND dimension='sources' AND value='X (Twitter)'",
		linkID, day(-1)).Scan(&twitter); err != nil || twitter != 2 {
		t.Errorf("yesterday's X (Twitter) source = %d (err %v), want 2", twitter, err)
	}

	// Rolled-up past days plus raw clicks from today equal the raw totals taken before the 40-day-old
	// partition was dropped.
	rolled, err := queries.GetRollupTotalsByLink(ctx, db.GetRollupTotalsByLinkParams{LinkID: linkID, Since: day(-45), Until: today})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := queries.GetClickTotalsByLink(ctx, db.GetClickTotalsByLinkParams{LinkID: linkID, CreatedAt: today})
	if err != nil {
		t.Fatal(err)
	}
	if got := rolled.Clicks + raw.Clicks; got != expected.Clicks {
		t.Errorf("rollups + today = %d human clicks, want %d", got, expected.Clicks)
	}
	if got := rolled.BotClicks + raw.BotClicks; got != expected.BotClicks {
		t.Errorf("rollups + today = %d bot clicks, want %d", got, expected.BotClicks)
	}

	again, err := m.Run(ctx, false)
	if err != nil || len(again.DaysRolled) != 0 || len(again.PartitionsDropped) != 0 {
		t.Fatalf("second pass = %+v, err %v; want nothing left to do", again, err)
	}

	t.Run("late click for yesterday is picked up after the grace period", func(t *testing.T) {
		click(day(-1).Add(23*time.Hour), "v7", "", false)
		if _, err := conn.Exec("UPDATE click_rollup_days SET computed_at=$1 WHERE day=$2", today.Add(time.Minute), day(-1)); err != nil {
			t.Fatal(err)
		}
		m.now = func() time.Time { return today.Add(3 * time.Hour) }
		report, err := m.Run(ctx, false)
		if err != nil || len(report.DaysRolled) != 1 {
			t.Fatalf("refresh pass = %+v, err %v; want yesterday rolled again", report, err)
		}
		if err := conn.QueryRow("SELECT human FROM click_daily WHERE link_id=$1 AND day=$2", linkID, day(-1)).Scan(&human); err != nil || human != 4 {
			t.Errorf("after refresh yesterday has %d human clicks (err %v), want 4", human, err)
		}
	})

	t.Run("partitions are created ahead", func(t *testing.T) {
		m.now = func() time.Time { return today.AddDate(0, 0, 10) }
		if err := m.EnsurePartitions(ctx); err != nil {
			t.Fatal(err)
		}
		var exists bool
		name := partitionPrefix + today.AddDate(0, 0, 13).Format(dayLayout)
		if err := conn.QueryRow("SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists); err != nil || !exists {
			t.Errorf("%s exists = %v (err %v), want true", name, exists, err)
		}
	})
}
