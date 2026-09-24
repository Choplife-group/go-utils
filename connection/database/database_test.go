package database

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOpenRejectsMissingSettings(t *testing.T) {

	cases := []struct {
		name string
		cfg  Config
	}{
		{"no host", Config{Port: "3306", Username: "root", Name: "identity"}},
		{"no port", Config{Host: "localhost", Username: "root", Name: "identity"}},
		{"no username", Config{Host: "localhost", Port: "3306", Name: "identity"}},
		{"no name", Config{Host: "localhost", Port: "3306", Username: "root"}},
	}

	for _, tc := range cases {

		t.Run(tc.name, func(t *testing.T) {

			db, err := Open(context.Background(), tc.cfg)

			if err == nil {
				t.Fatal("Open() succeeded, want an error")
			}

			// The services this replaces build a malformed DSN from blank
			// values and hand back a pool that fails later.
			if db != nil {
				t.Fatal("Open() returned a pool alongside an error")
			}
		})
	}
}

func TestOpenErrorHidesPassword(t *testing.T) {

	const password = "sup3rs3cr3t"

	db, err := Open(context.Background(), Config{
		Host:        "127.0.0.1",
		Port:        "1",
		Username:    "root",
		Password:    password,
		Name:        "identity",
		PingTimeout: 500 * time.Millisecond,
	})

	if err == nil {
		db.Close()
		t.Skip("something is listening on port 1")
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("Open() error leaked the password: %s", err)
	}

	if db != nil {
		t.Fatal("Open() returned a pool alongside an error")
	}
}

func TestDSNMatchesPlatformParameters(t *testing.T) {

	cfg := Config{Host: "db", Port: "3306", Username: "root", Password: "secret", Name: "identity"}

	want := "root:secret@tcp(db:3306)/identity?charset=utf8&parseTime=True&multiStatements=true"

	if got := cfg.dsn(); got != want {
		t.Fatalf("dsn() = %s, want %s", got, want)
	}
}

func TestConfigFromEnvReadsPlatformNames(t *testing.T) {

	t.Setenv("DATABASE_HOST", "db")
	t.Setenv("DATABASE_PORT", "3306")
	t.Setenv("DATABASE_USERNAME", "root")
	t.Setenv("DATABASE_PASSWORD", "secret")
	t.Setenv("DATABASE_NAME", "identity")
	t.Setenv("DATABASE_IDLE_CONNECTION", "7")
	t.Setenv("DATABASE_MAX_CONNECTION", "21")
	t.Setenv("DATABASE_CONNECTION_LIFETIME", "120")

	cfg := ConfigFromEnv()

	if cfg.Host != "db" || cfg.Name != "identity" {
		t.Fatalf("ConfigFromEnv() = %+v", cfg)
	}

	if cfg.MaxIdleConns != 7 || cfg.MaxOpenConns != 21 {
		t.Fatalf("pool settings = %d/%d, want 7/21", cfg.MaxIdleConns, cfg.MaxOpenConns)
	}

	if cfg.ConnMaxLifetime != 120*time.Second {
		t.Fatalf("ConnMaxLifetime = %s, want 120s", cfg.ConnMaxLifetime)
	}
}

// TestConfigFromEnvPrefixSelectsSecondDatabase covers the rewards-service shape,
// where a reports database sits behind its own prefix.
func TestConfigFromEnvPrefixSelectsSecondDatabase(t *testing.T) {

	t.Setenv("DATABASE_HOST", "primary")
	t.Setenv("REPORTS_DATABASE_HOST", "reports")
	t.Setenv("REPORTS_DATABASE_NAME", "reports")

	if got := ReportsConfigFromEnv().Host; got != "reports" {
		t.Fatalf("Host = %q, want %q", got, "reports")
	}

	if got := ConfigFromEnv().Host; got != "primary" {
		t.Fatalf("Host = %q, want %q", got, "primary")
	}
}

func TestApplyDefaults(t *testing.T) {

	cfg := Config{}
	cfg.applyDefaults()

	if cfg.MaxIdleConns != DefaultMaxIdleConns {
		t.Errorf("MaxIdleConns = %d, want %d", cfg.MaxIdleConns, DefaultMaxIdleConns)
	}

	if cfg.MaxOpenConns != DefaultMaxOpenConns {
		t.Errorf("MaxOpenConns = %d, want %d", cfg.MaxOpenConns, DefaultMaxOpenConns)
	}

	if cfg.ConnMaxLifetime != DefaultConnMaxLifetime {
		t.Errorf("ConnMaxLifetime = %s, want %s", cfg.ConnMaxLifetime, DefaultConnMaxLifetime)
	}

	if cfg.PingTimeout != DefaultPingTimeout {
		t.Errorf("PingTimeout = %s, want %s", cfg.PingTimeout, DefaultPingTimeout)
	}
}

// TestPingOnNilPoolReturnsError is the nil-safety contract: a health check must
// report a missing pool, not panic on it.
func TestPingOnNilPoolReturnsError(t *testing.T) {

	if err := Ping(context.Background(), nil); err == nil {
		t.Fatal("Ping(nil) succeeded, want an error")
	}
}

// TestConfigConstructorsStayInSync guards the duplication between
// ConfigFromEnv and ReportsConfigFromEnv. Both are spelled out variable by
// variable so every name is greppable, which means a field added to one can be
// forgotten in the other. Setting every variable to the same value under both
// prefixes makes any such omission show up as a differing field.
func TestConfigConstructorsStayInSync(t *testing.T) {

	values := map[string]string{
		"DATABASE_HOST":                "db-host",
		"DATABASE_PORT":                "3307",
		"DATABASE_USERNAME":            "db-user",
		"DATABASE_PASSWORD":            "db-pass",
		"DATABASE_NAME":                "db-name",
		"DATABASE_IDLE_CONNECTION":     "7",
		"DATABASE_MAX_CONNECTION":      "21",
		"DATABASE_CONNECTION_LIFETIME": "120",
	}

	for name, value := range values {
		t.Setenv(name, value)
		t.Setenv("REPORTS_"+name, value)
	}

	own := ConfigFromEnv()
	reports := ReportsConfigFromEnv()

	if own != reports {
		t.Fatalf("constructors have drifted apart:\n own     = %+v\n reports = %+v", own, reports)
	}

	// Sanity check that the values really were picked up, so the comparison is
	// not two identically empty structs.
	if own.Host != "db-host" || own.MaxOpenConns != 21 {
		t.Fatalf("ConfigFromEnv did not read the environment: %+v", own)
	}
}
