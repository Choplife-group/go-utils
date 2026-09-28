// Package database opens instrumented MySQL connection pools.
//
// Settings are read from the environment. The service's own database reads:
//
//	DATABASE_HOST                 (required) server host
//	DATABASE_PORT                 (required) server port
//	DATABASE_USERNAME             (required) user
//	DATABASE_PASSWORD             (optional) password
//	DATABASE_NAME                 (required) schema to connect to
//	DATABASE_IDLE_CONNECTION      (optional) idle pool size, default 5
//	DATABASE_MAX_CONNECTION       (optional) open pool size, default 10
//	DATABASE_CONNECTION_LIFETIME  (optional) connection lifetime in seconds, default 60
//
// The reports database reads the same names behind a REPORTS_ prefix — spelled
// out in full by ReportsConfigFromEnv — and is opened with OpenReportsFromEnv.
//
// Open validates its settings and pings the server before returning, so a
// misconfigured or unreachable database surfaces as an error at startup rather
// than as a nil pool that panics on first use. Nothing here logs the DSN.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/uptrace/opentelemetry-go-extra/otelsql"
	semconv "go.opentelemetry.io/otel/semconv/v1.20.0"

	"github.com/choplife-group/go-utils/connection/internal/env"
)

// Default pool settings, matching the values services have been using.
const (
	DefaultMaxIdleConns    = 5
	DefaultMaxOpenConns    = 10
	DefaultConnMaxLifetime = 60 * time.Second
	DefaultPingTimeout     = 5 * time.Second
)

// Config describes a MySQL connection pool. The zero value is not usable —
// Host, Port, Username and Name are required — but every other field has a
// default applied by Open.
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	Name     string

	MaxIdleConns    int
	MaxOpenConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration

	// PingTimeout bounds the connectivity check Open performs.
	PingTimeout time.Duration
}

// applyDefaults fills in fields left at their zero value.
func (c *Config) applyDefaults() {

	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = DefaultMaxIdleConns
	}

	if c.MaxOpenConns <= 0 {
		c.MaxOpenConns = DefaultMaxOpenConns
	}

	if c.ConnMaxLifetime <= 0 {
		c.ConnMaxLifetime = DefaultConnMaxLifetime
	}

	if c.ConnMaxIdleTime <= 0 {
		c.ConnMaxIdleTime = c.ConnMaxLifetime
	}

	if c.PingTimeout <= 0 {
		c.PingTimeout = DefaultPingTimeout
	}
}

// validate reports the first required field that is missing.
func (c *Config) validate() error {

	switch {
	case c.Host == "":
		return fmt.Errorf("database: host not configured")
	case c.Port == "":
		return fmt.Errorf("database: port not configured")
	case c.Username == "":
		return fmt.Errorf("database: username not configured")
	case c.Name == "":
		return fmt.Errorf("database: name not configured")
	}

	return nil
}

// dsn builds the MySQL connection string. The parameters match what the
// platform's services have always used, so behaviour is unchanged.
func (c *Config) dsn() string {

	return fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?charset=utf8&parseTime=True&multiStatements=true",
		c.Username, c.Password, c.Host, c.Port, c.Name,
	)
}

// ConfigFromEnv reads the service's own database settings from the DATABASE_*
// variables.
func ConfigFromEnv() Config {

	return Config{
		Host:            env.String("DATABASE_HOST", ""),
		Port:            env.String("DATABASE_PORT", ""),
		Username:        env.String("DATABASE_USERNAME", ""),
		Password:        env.String("DATABASE_PASSWORD", ""),
		Name:            env.String("DATABASE_NAME", ""),
		MaxIdleConns:    env.Int("DATABASE_IDLE_CONNECTION", DefaultMaxIdleConns),
		MaxOpenConns:    env.Int("DATABASE_MAX_CONNECTION", DefaultMaxOpenConns),
		ConnMaxLifetime: env.Seconds("DATABASE_CONNECTION_LIFETIME", DefaultConnMaxLifetime),
	}
}

// ReportsConfigFromEnv reads the reports database settings from the
// REPORTS_DATABASE_* variables. It mirrors ConfigFromEnv field for field, and
// TestConfigConstructorsStayInSync fails if the two ever drift apart.
func ReportsConfigFromEnv() Config {

	return Config{
		Host:            env.String("REPORTS_DATABASE_HOST", ""),
		Port:            env.String("REPORTS_DATABASE_PORT", ""),
		Username:        env.String("REPORTS_DATABASE_USERNAME", ""),
		Password:        env.String("REPORTS_DATABASE_PASSWORD", ""),
		Name:            env.String("REPORTS_DATABASE_NAME", ""),
		MaxIdleConns:    env.Int("REPORTS_DATABASE_IDLE_CONNECTION", DefaultMaxIdleConns),
		MaxOpenConns:    env.Int("REPORTS_DATABASE_MAX_CONNECTION", DefaultMaxOpenConns),
		ConnMaxLifetime: env.Seconds("REPORTS_DATABASE_CONNECTION_LIFETIME", DefaultConnMaxLifetime),
	}
}

// OpenFromEnv opens the service's own database from the DATABASE_* variables.
func OpenFromEnv() (*sql.DB, error) {

	return OpenWithContext(context.Background(), ConfigFromEnv())
}

// OpenFromEnvWithContext is OpenFromEnv with the startup ping bound to ctx.
func OpenFromEnvWithContext(ctx context.Context) (*sql.DB, error) {

	return OpenWithContext(ctx, ConfigFromEnv())
}

// OpenReportsFromEnv opens the reports database from the REPORTS_DATABASE_*
// variables, for the services that write to reports directly.
func OpenReportsFromEnv() (*sql.DB, error) {

	return OpenWithContext(context.Background(), ReportsConfigFromEnv())
}

// OpenReportsFromEnvWithContext is OpenReportsFromEnv with the startup ping
// bound to ctx.
func OpenReportsFromEnvWithContext(ctx context.Context) (*sql.DB, error) {

	return OpenWithContext(ctx, ReportsConfigFromEnv())
}

// Open validates cfg, opens an OpenTelemetry-instrumented pool and verifies it
// with a ping. It never returns a non-nil pool alongside an error, and never
// returns a nil pool with a nil error.
func Open(cfg Config) (*sql.DB, error) {

	return OpenWithContext(context.Background(), cfg)
}

// OpenWithContext is Open with the startup ping bound to ctx as well as
// PingTimeout.
func OpenWithContext(ctx context.Context, cfg Config) (*sql.DB, error) {

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	cfg.applyDefaults()

	dsn := cfg.dsn()

	db, err := otelsql.Open("mysql", dsn,
		otelsql.WithAttributes(semconv.DBSystemMySQL),
		otelsql.WithDBName(cfg.Name),
	)
	if err != nil {
		return nil, fmt.Errorf("database: open %s: %w", env.Redact(dsn), err)
	}

	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()

	if err := db.PingContext(pingCtx); err != nil {

		db.Close()

		return nil, fmt.Errorf("database: ping %s: %w", env.Redact(dsn), err)
	}

	otelsql.ReportDBStatsMetrics(db)

	return db, nil
}

// Ping checks that a pool is still reachable, and is what a health-check
// handler should call. A nil pool is reported as an error rather than panicking.
func Ping(db *sql.DB) error {

	return PingWithContext(context.Background(), db)
}

// PingWithContext is Ping bounded by ctx.
func PingWithContext(ctx context.Context, db *sql.DB) error {

	if db == nil {
		return fmt.Errorf("database: not initialised")
	}

	return db.PingContext(ctx)
}
