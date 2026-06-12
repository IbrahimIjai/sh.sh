package auth

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

func openDatabase(databaseURL string) (*sql.DB, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open auth database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping auth database: %w", err)
	}

	return db, nil
}
