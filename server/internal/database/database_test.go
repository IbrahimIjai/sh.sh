package database

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestPoolBoundsAndCancellation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	conn, err := Open(dsn, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	first, err := conn.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := conn.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(new(int)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("query with full pool = %v, want deadline exceeded", err)
	}
	stats := conn.Stats()
	if stats.OpenConnections != 2 || stats.WaitCount != 1 || stats.MaxOpenConnections != 2 {
		t.Fatalf("unexpected pool stats: %+v", stats)
	}
	first.Close()
	second.Close()
	if err := conn.Ping(); err != nil {
		t.Fatal(err)
	}
	if stats := conn.Stats(); stats.Idle != 2 || stats.OpenConnections != 2 {
		t.Fatalf("connections were not reused: %+v", stats)
	}
}
