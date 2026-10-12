package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ibrahimijai/sh.sh/db/migrations"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Migrate applies every pending migration in db/migrations and returns the files it applied.
// A Postgres advisory lock makes instances that start together take turns.
func Migrate(ctx context.Context, conn *sql.DB) (applied []string, version int64, err error) {
	provider, err := newMigrator(conn)
	if err != nil {
		return nil, 0, err
	}
	results, err := provider.Up(ctx)
	if partial, ok := errors.AsType[*goose.PartialError](err); ok {
		results = partial.Applied
	}
	for _, result := range results {
		applied = append(applied, result.Source.Path)
	}
	if err != nil {
		return applied, 0, fmt.Errorf("apply migrations: %w", err)
	}
	version, err = provider.GetDBVersion(ctx)
	if err != nil {
		return applied, 0, fmt.Errorf("read schema version: %w", err)
	}
	return applied, version, nil
}

func newMigrator(conn *sql.DB) (*goose.Provider, error) {
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return nil, fmt.Errorf("migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, migrations.FS, goose.WithSessionLocker(locker))
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	return provider, nil
}
