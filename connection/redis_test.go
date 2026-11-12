package connection

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestRedisOpenRejectsMissingSettings(t *testing.T) {

	cases := []struct {
		name string
		cfg  RedisConfig
	}{
		{"no host", RedisConfig{Port: "6379"}},
		{"no port", RedisConfig{Host: "localhost"}},
	}

	for _, tc := range cases {

		t.Run(tc.name, func(t *testing.T) {

			client, err := OpenRedisWithContext(context.Background(), tc.cfg)

			if err == nil {
				t.Fatal("Open() succeeded, want an error")
			}

			if client != nil {
				t.Fatal("Open() returned a client alongside an error")
			}
		})
	}
}

func TestRedisOpenReturnsErrorNotClientWhenUnreachable(t *testing.T) {

	client, err := OpenRedisWithContext(context.Background(), RedisConfig{
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

func TestRedisConfigFromEnvReadsPlatformNames(t *testing.T) {

	t.Setenv("REDIS_HOST", "cache")
	t.Setenv("REDIS_PORT", "6379")
	t.Setenv("REDIS_PASSWORD", "secret")
	t.Setenv("REDIS_DATABASE_NUMBER", "3")

	cfg := RedisConfigFromEnv()

	if cfg.Host != "cache" || cfg.Port != "6379" || cfg.Password != "secret" {
		t.Fatalf("ConfigFromEnv() = %+v", cfg)
	}

	if cfg.DB != 3 {
		t.Fatalf("DB = %d, want 3", cfg.DB)
	}
}

// TestRedisGlobalPrefixSelectsTheSharedInstance covers the distinction auth depends
// on: tokens live in the global instance, not the service-local one.
func TestRedisGlobalPrefixSelectsTheSharedInstance(t *testing.T) {

	t.Setenv("REDIS_HOST", "local-cache")
	t.Setenv("GLOBAL_REDIS_HOST", "global-cache")
	t.Setenv("GLOBAL_REDIS_DATABASE_NUMBER", "2")

	if got := GlobalRedisConfigFromEnv().Host; got != "global-cache" {
		t.Fatalf("Host = %q, want %q", got, "global-cache")
	}

	if got := GlobalRedisConfigFromEnv().DB; got != 2 {
		t.Fatalf("DB = %d, want 2", got)
	}

	if got := RedisConfigFromEnv().Host; got != "local-cache" {
		t.Fatalf("Host = %q, want %q", got, "local-cache")
	}
}

func TestRedisDefaultDatabaseNumberIsOne(t *testing.T) {

	t.Setenv("REDIS_HOST", "cache")
	t.Setenv("REDIS_PORT", "6379")

	if got := RedisConfigFromEnv().DB; got != DefaultRedisDB {
		t.Fatalf("DB = %d, want %d", got, DefaultRedisDB)
	}
}

// TestRedisApplyDefaultsDropsTheCopyPastedPoolSize documents the deliberate change
// away from the PoolSize: 1000 every service inherited.
func TestRedisApplyDefaultsDropsTheCopyPastedPoolSize(t *testing.T) {

	cfg := RedisConfig{}
	cfg.applyDefaults()

	want := 10 * runtime.GOMAXPROCS(0)

	if cfg.PoolSize != want {
		t.Fatalf("PoolSize = %d, want %d", cfg.PoolSize, want)
	}

	if cfg.MinIdleConns > cfg.PoolSize {
		t.Fatalf("MinIdleConns %d exceeds PoolSize %d", cfg.MinIdleConns, cfg.PoolSize)
	}
}

func TestRedisApplyDefaultsClampsMinIdleToPoolSize(t *testing.T) {

	cfg := RedisConfig{PoolSize: 2, MinIdleConns: 50}
	cfg.applyDefaults()

	if cfg.MinIdleConns != 2 {
		t.Fatalf("MinIdleConns = %d, want it clamped to 2", cfg.MinIdleConns)
	}
}

// TestRedisPingOnNilClientReturnsError is the nil-safety contract for health checks.
func TestRedisPingOnNilClientReturnsError(t *testing.T) {

	if err := PingRedisWithContext(context.Background(), nil); err == nil {
		t.Fatal("Ping(nil) succeeded, want an error")
	}
}

// TestRedisConfigConstructorsStayInSync guards the duplication between
// ConfigFromEnv and GlobalConfigFromEnv, in the same way the database package
// guards its own pair: a field added to one and forgotten in the other shows up
// here as a differing value.
func TestRedisConfigConstructorsStayInSync(t *testing.T) {

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

	own := RedisConfigFromEnv()
	global := GlobalRedisConfigFromEnv()

	if own != global {
		t.Fatalf("constructors have drifted apart:\n local  = %+v\n global = %+v", own, global)
	}

	if own.Host != "cache-host" || own.DB != 4 || !own.Tracing {
		t.Fatalf("ConfigFromEnv did not read the environment: %+v", own)
	}
}

// TestRedisPlainVariantsKeepTheContract covers the no-ctx forms services call
// today, which must validate and nil-check exactly like the WithContext forms.
func TestRedisPlainVariantsKeepTheContract(t *testing.T) {

	client, err := OpenRedis(RedisConfig{})
	if err == nil {
		t.Fatal("Open() with no settings succeeded")
	}

	if client != nil {
		t.Fatal("Open() returned a client alongside an error")
	}

	if err := PingRedis(nil); err == nil {
		t.Fatal("Ping(nil) succeeded, want an error")
	}
}
