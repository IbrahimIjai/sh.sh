package analytics

import (
	"cmp"
	"context"
	"database/sql"
	"slices"

	db "github.com/ibrahimijai/sh.sh/internal/db/generated"
)

// PostgresStore writes click batches. With asyncCommit, a batch's commit doesn't wait for the WAL
// flush: a PostgreSQL crash can lose up to about three times wal_writer_delay (0.6 s by default)
// of committed clicks, in exchange for far fewer disk flushes. Links and accounts are unaffected.
type PostgresStore struct {
	conn        *sql.DB
	asyncCommit bool
}

func NewPostgresStore(conn *sql.DB, asyncCommit bool) *PostgresStore {
	return &PostgresStore{conn: conn, asyncCommit: asyncCommit}
}

// RecordClicks stores the batch and returns how many of its clicks were new.
func (s *PostgresStore) RecordClicks(ctx context.Context, arg db.RecordClicksParams) (int64, error) {
	if !s.asyncCommit {
		return db.New(s.conn).RecordClicks(ctx, arg)
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SET LOCAL synchronous_commit = off"); err != nil {
		return 0, err
	}
	inserted, err := db.New(tx).RecordClicks(ctx, arg)
	if err != nil {
		return 0, err
	}
	return inserted, tx.Commit()
}

// BatchParams sorts clicks by storage key, so batches that overlap on replay take their row locks
// in the same order, and turns them into RecordClicks arguments.
func BatchParams(clicks []Click) db.RecordClicksParams {
	slices.SortFunc(clicks, func(a, b Click) int {
		return cmp.Or(a.CreatedAt.Compare(b.CreatedAt), cmp.Compare(a.EventID, b.EventID))
	})
	var params db.RecordClicksParams
	for _, c := range clicks {
		params.EventIds = append(params.EventIds, c.EventID)
		params.CreatedAts = append(params.CreatedAts, c.CreatedAt)
		params.LinkIds = append(params.LinkIds, c.LinkID)
		params.VisitorHashes = append(params.VisitorHashes, c.VisitorHash)
		params.RefererHosts = append(params.RefererHosts, c.RefererHost)
		params.Sources = append(params.Sources, c.Source)
		params.UtmSources = append(params.UtmSources, c.UTMSource)
		params.UtmMediums = append(params.UtmMediums, c.UTMMedium)
		params.UtmCampaigns = append(params.UtmCampaigns, c.UTMCampaign)
		params.DeviceTypes = append(params.DeviceTypes, c.DeviceType)
		params.Browsers = append(params.Browsers, c.Browser)
		params.Oses = append(params.Oses, c.OS)
		params.Countries = append(params.Countries, c.Country)
		params.Cities = append(params.Cities, c.City)
		params.IsBots = append(params.IsBots, c.IsBot)
	}
	return params
}
