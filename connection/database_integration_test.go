//go:build integration

package connection

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
//	go test -tags integration ./connection/ -run TestDatabase -v

func TestDatabaseOpenAgainstLiveServer(t *testing.T) {

	cfg := DatabaseConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set DATABASE_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	db, err := OpenDatabaseWithContext(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	defer db.Close()

	if err := PingDatabaseWithContext(ctx, db); err != nil {
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

func TestDatabaseOpenWithoutContextAgainstLiveServer(t *testing.T) {

	cfg := DatabaseConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set DATABASE_HOST to run this")
	}

	db, err := OpenDatabase(cfg)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	defer db.Close()

	if err := PingDatabase(db); err != nil {
		t.Fatalf("Ping() = %v", err)
	}
}

// TestDatabaseFromEnvConstructorsAgainstLiveServer opens every environment-driven
// constructor in both forms. The reports settings are pointed at the same
// server, since the test stack has one.
func TestDatabaseFromEnvConstructorsAgainstLiveServer(t *testing.T) {

	if DatabaseConfigFromEnv().Host == "" {
		t.Skip("set DATABASE_HOST to run this")
	}

	for _, name := range []string{"HOST", "PORT", "USERNAME", "PASSWORD", "NAME"} {
		t.Setenv("REPORTS_DATABASE_"+name, os.Getenv("DATABASE_"+name))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	constructors := map[string]func() (*sql.DB, error){
		"OpenFromEnv":                   OpenDatabaseFromEnv,
		"OpenFromEnvWithContext":        func() (*sql.DB, error) { return OpenDatabaseFromEnvWithContext(ctx) },
		"OpenReportsFromEnv":            OpenReportsDatabaseFromEnv,
		"OpenReportsFromEnvWithContext": func() (*sql.DB, error) { return OpenReportsDatabaseFromEnvWithContext(ctx) },
	}

	for name, open := range constructors {

		db, err := open()
		if err != nil {
			t.Fatalf("%s() = %v", name, err)
		}

		if err := PingDatabase(db); err != nil {
			t.Fatalf("%s(): Ping() = %v", name, err)
		}

		db.Close()
	}
}
