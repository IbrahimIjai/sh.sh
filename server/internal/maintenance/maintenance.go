// Package maintenance keeps click storage in shape: daily partitions ahead of time, rollups of
// closed UTC days, and raw retention.
package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
	"github.com/lib/pq"
)

const (
	lockKey         = 7_461_001
	partitionPrefix = "clicks_p"
	dayLayout       = "20060102"
)

type Config struct {
	// RawRetentionDays drops daily raw partitions older than this, once their day is rolled up.
	RawRetentionDays int
	// Every is how often Start runs a pass.
	Every time.Duration
	// LookbackDays is how far back a pass looks for closed days without rollups. Default 365.
	LookbackDays int
	// RefreshAfter re-rolls yesterday once this long after midnight, to include late batches. Default 2h.
	RefreshAfter time.Duration
	// DaysAhead is how many future days get a partition. Default 3.
	DaysAhead int
}

type Report struct {
	Skipped           bool     `json:"skipped,omitempty"`
	PartitionsCreated []string `json:"partitions_created,omitempty"`
	DaysRolled        []string `json:"days_rolled,omitempty"`
	PartitionsDropped []string `json:"partitions_dropped,omitempty"`
}

type Maintainer struct {
	conn    *sql.DB
	queries *db.Queries
	cfg     Config
	now     func() time.Time
}

func New(conn *sql.DB, cfg Config) *Maintainer {
	if cfg.RawRetentionDays <= 0 {
		cfg.RawRetentionDays = 30
	}
	if cfg.Every <= 0 {
		cfg.Every = 15 * time.Minute
	}
	if cfg.LookbackDays <= 0 {
		cfg.LookbackDays = 365
	}
	if cfg.RefreshAfter <= 0 {
		cfg.RefreshAfter = 2 * time.Hour
	}
	if cfg.DaysAhead <= 0 {
		cfg.DaysAhead = 3
	}
	return &Maintainer{conn: conn, queries: db.New(conn), cfg: cfg, now: time.Now}
}

// Start runs a pass now and then every cfg.Every until ctx is done. A pass is skipped while
// another instance holds the lock.
func (m *Maintainer) Start(ctx context.Context) {
	go func() {
		m.logPass(ctx)
		ticker := time.NewTicker(m.cfg.Every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.logPass(ctx)
			}
		}
	}()
}

func (m *Maintainer) logPass(ctx context.Context) {
	report, err := m.Run(ctx, false)
	if err != nil {
		log.Printf("maintenance: %v", err)
	}
	if len(report.PartitionsCreated)+len(report.DaysRolled)+len(report.PartitionsDropped) > 0 {
		log.Printf("maintenance: created %d partitions, rolled up %d days, dropped %d partitions",
			len(report.PartitionsCreated), len(report.DaysRolled), len(report.PartitionsDropped))
	}
}

// Run makes one pass. With wait, it waits for another instance's pass instead of skipping.
func (m *Maintainer) Run(ctx context.Context, wait bool) (Report, error) {
	conn, err := m.conn.Conn(ctx)
	if err != nil {
		return Report{}, err
	}
	defer conn.Close()

	locked := true
	if wait {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
			return Report{}, err
		}
	} else if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&locked); err != nil {
		return Report{}, err
	}
	if !locked {
		return Report{Skipped: true}, nil
	}
	defer conn.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey)

	var report Report
	today := dayOf(m.now())
	var errs []error
	created, err := m.ensurePartitions(ctx, today)
	report.PartitionsCreated, errs = created, append(errs, err)
	rolled, err := m.rollUp(ctx, today)
	report.DaysRolled, errs = rolled, append(errs, err)
	dropped, err := m.dropExpired(ctx, today)
	report.PartitionsDropped, errs = dropped, append(errs, err)
	return report, errors.Join(errs...)
}

// EnsurePartitions creates today's and upcoming daily partitions. Run it before accepting traffic
// so today's clicks don't land in the default partition.
func (m *Maintainer) EnsurePartitions(ctx context.Context) error {
	_, err := m.ensurePartitions(ctx, dayOf(m.now()))
	return err
}

func (m *Maintainer) ensurePartitions(ctx context.Context, today time.Time) ([]string, error) {
	var created []string
	var errs []error
	for d := today.AddDate(0, 0, -1); !d.After(today.AddDate(0, 0, m.cfg.DaysAhead)); d = d.AddDate(0, 0, 1) {
		name := partitionPrefix + d.Format(dayLayout)
		var exists bool
		if err := m.conn.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists); err != nil {
			errs = append(errs, err)
			continue
		}
		if exists {
			continue
		}
		stmt := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s PARTITION OF clicks FOR VALUES FROM ('%s') TO ('%s')",
			pq.QuoteIdentifier(name), d.Format(time.RFC3339), d.AddDate(0, 0, 1).Format(time.RFC3339))
		if _, err := m.conn.ExecContext(ctx, stmt); err != nil {
			errs = append(errs, fmt.Errorf("create %s: %w", name, err))
			continue
		}
		created = append(created, name)
	}
	return created, errors.Join(errs...)
}

func (m *Maintainer) rollUp(ctx context.Context, today time.Time) ([]string, error) {
	from := today.AddDate(0, 0, -m.cfg.LookbackDays)
	days, err := m.queries.ListRolledDays(ctx, db.ListRolledDaysParams{Since: from, Until: today})
	if err != nil {
		return nil, err
	}
	done := make(map[string]bool, len(days))
	for _, d := range days {
		done[d.Format(time.DateOnly)] = true
	}

	var rolled []string
	for d := from; d.Before(today); d = d.AddDate(0, 0, 1) {
		if done[d.Format(time.DateOnly)] {
			continue
		}
		if err := m.rollDay(ctx, d); err != nil {
			return rolled, fmt.Errorf("roll up %s: %w", d.Format(time.DateOnly), err)
		}
		rolled = append(rolled, d.Format(time.DateOnly))
	}

	yesterday := today.AddDate(0, 0, -1)
	refreshAt := today.Add(m.cfg.RefreshAfter)
	if m.now().Before(refreshAt) {
		return rolled, nil
	}
	computedAt, err := m.queries.GetRollupDay(ctx, yesterday)
	if err != nil {
		return rolled, err
	}
	if computedAt.Before(refreshAt) {
		if err := m.rollDay(ctx, yesterday); err != nil {
			return rolled, fmt.Errorf("refresh %s: %w", yesterday.Format(time.DateOnly), err)
		}
		rolled = append(rolled, yesterday.Format(time.DateOnly))
	}
	return rolled, nil
}

// RollDay replaces the rollups of one closed UTC day from its raw clicks.
func (m *Maintainer) RollDay(ctx context.Context, day time.Time) error {
	return m.rollDay(ctx, dayOf(day))
}

func (m *Maintainer) rollDay(ctx context.Context, day time.Time) error {
	tx, err := m.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := m.queries.WithTx(tx)
	window := db.InsertRollupDailyParams{Day: day, DayStart: day, DayEnd: day.AddDate(0, 0, 1)}
	steps := []func() error{
		func() error { return q.DeleteRollupDaily(ctx, day) },
		func() error { return q.DeleteRollupDimensions(ctx, day) },
		func() error { return q.InsertRollupDaily(ctx, window) },
		func() error { return q.InsertRollupDimensions(ctx, db.InsertRollupDimensionsParams(window)) },
		func() error { return q.MarkRollupDay(ctx, day) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (m *Maintainer) dropExpired(ctx context.Context, today time.Time) ([]string, error) {
	rows, err := m.conn.QueryContext(ctx, `SELECT c.relname FROM pg_inherits i
		JOIN pg_class c ON c.oid = i.inhrelid
		WHERE i.inhparent = 'clicks'::regclass`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	cutoff := today.AddDate(0, 0, -m.cfg.RawRetentionDays)
	var dropped []string
	var errs []error
	for _, name := range names {
		day, err := time.Parse(dayLayout, strings.TrimPrefix(name, partitionPrefix))
		if !strings.HasPrefix(name, partitionPrefix) || err != nil || !day.Before(cutoff) {
			continue
		}
		// Raw rows are only dropped once the day's rollups exist.
		if _, err := m.queries.GetRollupDay(ctx, day); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			errs = append(errs, err)
			continue
		}
		if _, err := m.conn.ExecContext(ctx, "DROP TABLE "+pq.QuoteIdentifier(name)); err != nil {
			errs = append(errs, fmt.Errorf("drop %s: %w", name, err))
			continue
		}
		dropped = append(dropped, name)
	}
	return dropped, errors.Join(errs...)
}

func dayOf(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
