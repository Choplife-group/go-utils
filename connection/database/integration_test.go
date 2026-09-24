//go:build integration

package database

import (
	"context"
	"testing"
	"time"
)

// Run with a live MySQL:
//
//	docker run -d --name mysql-it -p 3307:3306 -e MYSQL_ROOT_PASSWORD=secret -e MYSQL_DATABASE=identity mysql:8
//	DATABASE_HOST=127.0.0.1 DATABASE_PORT=3307 DATABASE_USERNAME=root \
//	DATABASE_PASSWORD=secret DATABASE_NAME=identity \
//	go test -tags integration ./connection/database/ -v

func TestOpenAgainstLiveServer(t *testing.T) {

	cfg := ConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set DATABASE_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	defer db.Close()

	if err := Ping(ctx, db); err != nil {
		t.Fatalf("Ping() = %v", err)
	}

	// The pool settings must actually reach the pool, since services rely on
	// them to bound connections against a shared database.
	stats := db.Stats()

	if stats.MaxOpenConnections != cfg.MaxOpenConns {
		t.Fatalf("MaxOpenConnections = %d, want %d", stats.MaxOpenConnections, cfg.MaxOpenConns)
	}

	var result int

	if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&result); err != nil {
		t.Fatalf("query = %v", err)
	}

	if result != 1 {
		t.Fatalf("SELECT 1 returned %d", result)
	}
}
