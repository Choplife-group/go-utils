// Package redis opens Redis clients for the platform's two roles.
//
// Services use two Redis instances: a service-local cache and the shared global
// instance that identity-service writes tokens into and every auth middleware
// reads. Each has its own constructor, and the local one reads:
//
//	REDIS_HOST                    (required) server host
//	REDIS_PORT                    (required) server port
//	REDIS_PASSWORD                (optional) password
//	REDIS_DATABASE_NUMBER         (optional) logical database, default 1
//	REDIS_POOL_SIZE               (optional) pool size, default 10 per CPU
//	REDIS_MIN_IDLE_CONNECTIONS    (optional) idle pool floor, default 10
//
// The shared platform cache reads the same names behind a GLOBAL_ prefix —
// spelled out in full by GlobalConfigFromEnv — and is opened with
// OpenGlobalFromEnv.
//
// Each client is opened once and cached: calling a constructor twice hands back
// the same client rather than building a second connection pool. Callers must
// therefore NOT call Close on a client obtained here — a health check that
// closed its client would take the whole service's cache down with it.
package redis

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"

	"github.com/choplife-group/go-utils/connection/internal/env"
)

// Cache keys for the two roles, so the local and global clients are never
// collapsed into one — auth reads the global instance and must not be handed
// the service-local one.
const (
	localRole  = "local"
	globalRole = "global"
)

// Default client settings.
const (
	DefaultDB              = 1
	DefaultMinIdleConns    = 10
	DefaultConnMaxIdleTime = 60 * time.Second
	DefaultDialTimeout     = 5 * time.Second
	DefaultPingTimeout     = 5 * time.Second
)

// Config describes a Redis client. The zero value is not usable — Host and Port
// are required — but every other field has a default applied by Open.
type Config struct {
	Host     string
	Port     string
	Password string
	DB       int

	PoolSize        int
	MinIdleConns    int
	ConnMaxIdleTime time.Duration
	DialTimeout     time.Duration

	// PingTimeout bounds the connectivity check Open performs.
	PingTimeout time.Duration

	// Tracing instruments the client with OpenTelemetry when set.
	Tracing bool
}

// applyDefaults fills in fields left at their zero value.
//
// PoolSize deliberately falls through to go-redis's own default of ten
// connections per CPU. The services this package replaces all hardcoded a pool
// of 1000, which was copied between repos rather than measured and reserves far
// more sockets than any of them use.
func (c *Config) applyDefaults() {

	if c.PoolSize <= 0 {
		c.PoolSize = 10 * runtime.GOMAXPROCS(0)
	}

	if c.MinIdleConns <= 0 {
		c.MinIdleConns = DefaultMinIdleConns
	}

	if c.MinIdleConns > c.PoolSize {
		c.MinIdleConns = c.PoolSize
	}

	if c.ConnMaxIdleTime <= 0 {
		c.ConnMaxIdleTime = DefaultConnMaxIdleTime
	}

	if c.DialTimeout <= 0 {
		c.DialTimeout = DefaultDialTimeout
	}

	if c.PingTimeout <= 0 {
		c.PingTimeout = DefaultPingTimeout
	}
}

// validate reports the first required field that is missing.
func (c *Config) validate() error {

	switch {
	case c.Host == "":
		return fmt.Errorf("redis: host not configured")
	case c.Port == "":
		return fmt.Errorf("redis: port not configured")
	}

	return nil
}

// addr returns the host:port the client dials.
func (c *Config) addr() string {

	return fmt.Sprintf("%s:%s", c.Host, c.Port)
}

// ConfigFromEnv reads the service-local cache settings from the REDIS_*
// variables.
func ConfigFromEnv() Config {

	return Config{
		Host:         env.String("REDIS_HOST", ""),
		Port:         env.String("REDIS_PORT", ""),
		Password:     env.String("REDIS_PASSWORD", ""),
		DB:           env.Int("REDIS_DATABASE_NUMBER", DefaultDB),
		PoolSize:     env.Int("REDIS_POOL_SIZE", 0),
		MinIdleConns: env.Int("REDIS_MIN_IDLE_CONNECTIONS", DefaultMinIdleConns),
		Tracing:      env.Bool("REDIS_TRACING", false),
	}
}

// GlobalConfigFromEnv reads the shared platform cache settings from the
// GLOBAL_REDIS_* variables. It mirrors ConfigFromEnv field for field, and
// TestConfigConstructorsStayInSync fails if the two ever drift apart.
func GlobalConfigFromEnv() Config {

	return Config{
		Host:         env.String("GLOBAL_REDIS_HOST", ""),
		Port:         env.String("GLOBAL_REDIS_PORT", ""),
		Password:     env.String("GLOBAL_REDIS_PASSWORD", ""),
		DB:           env.Int("GLOBAL_REDIS_DATABASE_NUMBER", DefaultDB),
		PoolSize:     env.Int("GLOBAL_REDIS_POOL_SIZE", 0),
		MinIdleConns: env.Int("GLOBAL_REDIS_MIN_IDLE_CONNECTIONS", DefaultMinIdleConns),
		Tracing:      env.Bool("GLOBAL_REDIS_TRACING", false),
	}
}

var (
	mu      sync.Mutex
	clients = map[string]*redis.Client{}
)

// OpenFromEnv returns the service-local cache client, read from the REDIS_*
// variables and opened on first use. Repeated calls return the same client, so
// do not Close it.
func OpenFromEnv(ctx context.Context) (*redis.Client, error) {

	return openCached(ctx, localRole, ConfigFromEnv())
}

// OpenGlobalFromEnv returns the shared platform cache client, read from the
// GLOBAL_REDIS_* variables. This is the instance identity-service writes tokens
// into and the one auth middleware must be given — never the local cache.
// Repeated calls return the same client, so do not Close it.
func OpenGlobalFromEnv(ctx context.Context) (*redis.Client, error) {

	return openCached(ctx, globalRole, GlobalConfigFromEnv())
}

// openCached returns the client cached under key, opening it on first use.
// Only successful opens are cached, so a transient failure at startup does not
// poison every later call.
func openCached(ctx context.Context, key string, cfg Config) (*redis.Client, error) {

	mu.Lock()
	defer mu.Unlock()

	if client, ok := clients[key]; ok {
		return client, nil
	}

	client, err := Open(ctx, cfg)
	if err != nil {
		return nil, err
	}

	clients[key] = client

	return client, nil
}

// Open validates cfg, builds a client and verifies it with a ping. It never
// returns a non-nil client alongside an error, and never returns a nil client
// with a nil error.
//
// Unlike OpenFromEnv, the client returned here is not cached and is the
// caller's to Close.
func Open(ctx context.Context, cfg Config) (*redis.Client, error) {

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	cfg.applyDefaults()

	client := redis.NewClient(&redis.Options{
		Addr:            cfg.addr(),
		Password:        cfg.Password,
		DB:              cfg.DB,
		PoolSize:        cfg.PoolSize,
		MinIdleConns:    cfg.MinIdleConns,
		ConnMaxIdleTime: cfg.ConnMaxIdleTime,
		DialTimeout:     cfg.DialTimeout,
	})

	if cfg.Tracing {

		if err := redisotel.InstrumentTracing(client); err != nil {

			client.Close()

			return nil, fmt.Errorf("redis: instrument %s: %w", cfg.addr(), err)
		}
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()

	if err := client.Ping(pingCtx).Err(); err != nil {

		client.Close()

		return nil, fmt.Errorf("redis: ping %s: %w", cfg.addr(), err)
	}

	return client, nil
}

// Ping checks that a client is still reachable, and is what a health-check
// handler should call. A nil client is reported as an error rather than
// panicking.
func Ping(ctx context.Context, client *redis.Client) error {

	if client == nil {
		return fmt.Errorf("redis: not initialised")
	}

	return client.Ping(ctx).Err()
}
