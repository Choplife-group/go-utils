package redis

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestOpenRejectsMissingSettings(t *testing.T) {

	cases := []struct {
		name string
		cfg  Config
	}{
		{"no host", Config{Port: "6379"}},
		{"no port", Config{Host: "localhost"}},
	}

	for _, tc := range cases {

		t.Run(tc.name, func(t *testing.T) {

			client, err := Open(context.Background(), tc.cfg)

			if err == nil {
				t.Fatal("Open() succeeded, want an error")
			}

			if client != nil {
				t.Fatal("Open() returned a client alongside an error")
			}
		})
	}
}

func TestOpenReturnsErrorNotClientWhenUnreachable(t *testing.T) {

	client, err := Open(context.Background(), Config{
		Host:        "127.0.0.1",
		Port:        "1",
		DialTimeout: 500 * time.Millisecond,
		PingTimeout: 500 * time.Millisecond,
	})

	if err == nil {
		client.Close()
		t.Skip("something is listening on port 1")
	}

	if client != nil {
		t.Fatal("Open() returned a client alongside an error")
	}
}

func TestConfigFromEnvReadsPlatformNames(t *testing.T) {

	t.Setenv("REDIS_HOST", "cache")
	t.Setenv("REDIS_PORT", "6379")
	t.Setenv("REDIS_PASSWORD", "secret")
	t.Setenv("REDIS_DATABASE_NUMBER", "3")

	cfg := ConfigFromEnv()

	if cfg.Host != "cache" || cfg.Port != "6379" || cfg.Password != "secret" {
		t.Fatalf("ConfigFromEnv() = %+v", cfg)
	}

	if cfg.DB != 3 {
		t.Fatalf("DB = %d, want 3", cfg.DB)
	}
}

// TestGlobalPrefixSelectsTheSharedInstance covers the distinction auth depends
// on: tokens live in the global instance, not the service-local one.
func TestGlobalPrefixSelectsTheSharedInstance(t *testing.T) {

	t.Setenv("REDIS_HOST", "local-cache")
	t.Setenv("GLOBAL_REDIS_HOST", "global-cache")
	t.Setenv("GLOBAL_REDIS_DATABASE_NUMBER", "2")

	if got := GlobalConfigFromEnv().Host; got != "global-cache" {
		t.Fatalf("Host = %q, want %q", got, "global-cache")
	}

	if got := GlobalConfigFromEnv().DB; got != 2 {
		t.Fatalf("DB = %d, want 2", got)
	}

	if got := ConfigFromEnv().Host; got != "local-cache" {
		t.Fatalf("Host = %q, want %q", got, "local-cache")
	}
}

func TestDefaultDatabaseNumberIsOne(t *testing.T) {

	t.Setenv("REDIS_HOST", "cache")
	t.Setenv("REDIS_PORT", "6379")

	if got := ConfigFromEnv().DB; got != DefaultDB {
		t.Fatalf("DB = %d, want %d", got, DefaultDB)
	}
}

// TestApplyDefaultsDropsTheCopyPastedPoolSize documents the deliberate change
// away from the PoolSize: 1000 every service inherited.
func TestApplyDefaultsDropsTheCopyPastedPoolSize(t *testing.T) {

	cfg := Config{}
	cfg.applyDefaults()

	want := 10 * runtime.GOMAXPROCS(0)

	if cfg.PoolSize != want {
		t.Fatalf("PoolSize = %d, want %d", cfg.PoolSize, want)
	}

	if cfg.MinIdleConns > cfg.PoolSize {
		t.Fatalf("MinIdleConns %d exceeds PoolSize %d", cfg.MinIdleConns, cfg.PoolSize)
	}
}

func TestApplyDefaultsClampsMinIdleToPoolSize(t *testing.T) {

	cfg := Config{PoolSize: 2, MinIdleConns: 50}
	cfg.applyDefaults()

	if cfg.MinIdleConns != 2 {
		t.Fatalf("MinIdleConns = %d, want it clamped to 2", cfg.MinIdleConns)
	}
}

// TestPingOnNilClientReturnsError is the nil-safety contract for health checks.
func TestPingOnNilClientReturnsError(t *testing.T) {

	if err := Ping(context.Background(), nil); err == nil {
		t.Fatal("Ping(nil) succeeded, want an error")
	}
}

// TestConfigConstructorsStayInSync guards the duplication between
// ConfigFromEnv and GlobalConfigFromEnv, in the same way the database package
// guards its own pair: a field added to one and forgotten in the other shows up
// here as a differing value.
func TestConfigConstructorsStayInSync(t *testing.T) {

	values := map[string]string{
		"REDIS_HOST":                 "cache-host",
		"REDIS_PORT":                 "6380",
		"REDIS_PASSWORD":             "cache-pass",
		"REDIS_DATABASE_NUMBER":      "4",
		"REDIS_POOL_SIZE":            "33",
		"REDIS_MIN_IDLE_CONNECTIONS": "3",
		"REDIS_TRACING":              "true",
	}

	for name, value := range values {
		t.Setenv(name, value)
		t.Setenv("GLOBAL_"+name, value)
	}

	own := ConfigFromEnv()
	global := GlobalConfigFromEnv()

	if own != global {
		t.Fatalf("constructors have drifted apart:\n local  = %+v\n global = %+v", own, global)
	}

	if own.Host != "cache-host" || own.DB != 4 || !own.Tracing {
		t.Fatalf("ConfigFromEnv did not read the environment: %+v", own)
	}
}
