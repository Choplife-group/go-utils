//go:build integration

package database

import (
	"context"
	"database/sql"
	"os"
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

	db, err := OpenWithContext(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	defer db.Close()

	if err := PingWithContext(ctx, db); err != nil {
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

func TestOpenWithoutContextAgainstLiveServer(t *testing.T) {

	cfg := ConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set DATABASE_HOST to run this")
	}

	db, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	defer db.Close()

	if err := Ping(db); err != nil {
		t.Fatalf("Ping() = %v", err)
	}
}

// TestFromEnvConstructorsAgainstLiveServer opens every environment-driven
// constructor in both forms. The reports settings are pointed at the same
// server, since the test stack has one.
func TestFromEnvConstructorsAgainstLiveServer(t *testing.T) {

	if ConfigFromEnv().Host == "" {
		t.Skip("set DATABASE_HOST to run this")
	}

	for _, name := range []string{"HOST", "PORT", "USERNAME", "PASSWORD", "NAME"} {
		t.Setenv("REPORTS_DATABASE_"+name, os.Getenv("DATABASE_"+name))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	constructors := map[string]func() (*sql.DB, error){
		"OpenFromEnv":                   OpenFromEnv,
		"OpenFromEnvWithContext":        func() (*sql.DB, error) { return OpenFromEnvWithContext(ctx) },
		"OpenReportsFromEnv":            OpenReportsFromEnv,
		"OpenReportsFromEnvWithContext": func() (*sql.DB, error) { return OpenReportsFromEnvWithContext(ctx) },
	}

	for name, open := range constructors {

		db, err := open()
		if err != nil {
			t.Fatalf("%s() = %v", name, err)
		}

		if err := Ping(db); err != nil {
			t.Fatalf("%s(): Ping() = %v", name, err)
		}

		db.Close()
	}
}
