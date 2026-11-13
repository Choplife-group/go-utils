//go:build integration

package connection

import (
	"context"
	"testing"
	"time"
)

// Run with a live Redis:
//
//	docker run -d --name redis-it -p 6380:6379 redis:7
//	REDIS_HOST=127.0.0.1 REDIS_PORT=6380 go test -tags integration ./connection/ -run TestRedis -v

func TestRedisOpenAgainstLiveServer(t *testing.T) {

	cfg := RedisConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set REDIS_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := OpenRedisWithContext(ctx, cfg)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	defer client.Close()

	if err := client.Set(ctx, "go-utils-it", "value", time.Minute).Err(); err != nil {
		t.Fatalf("Set() = %v", err)
	}

	got, err := client.Get(ctx, "go-utils-it").Result()
	if err != nil {
		t.Fatalf("Get() = %v", err)
	}

	if got != "value" {
		t.Fatalf("Get() = %q, want %q", got, "value")
	}

	client.Del(ctx, "go-utils-it")
}

// TestRedisOpenFromEnvReturnsTheSameClient proves the memoisation that replaces the
// per-call 1000-connection pool every service builds today.
func TestRedisOpenFromEnvReturnsTheSameClient(t *testing.T) {

	if RedisConfigFromEnv().Host == "" {
		t.Skip("set REDIS_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := OpenRedisFromEnvWithContext(ctx)
	if err != nil {
		t.Fatalf("OpenFromEnv() = %v", err)
	}

	second, err := OpenRedisFromEnvWithContext(ctx)
	if err != nil {
		t.Fatalf("OpenFromEnv() = %v", err)
	}

	if first != second {
		t.Fatal("OpenFromEnv() built a second client for the same prefix")
	}
}

func TestRedisOpenWithoutContextAgainstLiveServer(t *testing.T) {

	cfg := RedisConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set REDIS_HOST to run this")
	}

	client, err := OpenRedis(cfg)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	defer client.Close()

	if err := PingRedis(client); err != nil {
		t.Fatalf("Ping() = %v", err)
	}
}

// TestRedisFromEnvConstructorsShareClientsPerRole proves both forms of each
// constructor hand back the same cached client, and that the global client is
// never the local one — auth must read the instance identity-service writes to.
func TestRedisFromEnvConstructorsShareClientsPerRole(t *testing.T) {

	if RedisConfigFromEnv().Host == "" || GlobalRedisConfigFromEnv().Host == "" {
		t.Skip("set REDIS_HOST and GLOBAL_REDIS_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	local, err := OpenRedisFromEnv()
	if err != nil {
		t.Fatalf("OpenFromEnv() = %v", err)
	}

	localWithContext, err := OpenRedisFromEnvWithContext(ctx)
	if err != nil {
		t.Fatalf("OpenFromEnvWithContext() = %v", err)
	}

	global, err := OpenGlobalRedisFromEnv()
	if err != nil {
		t.Fatalf("OpenGlobalFromEnv() = %v", err)
	}

	globalWithContext, err := OpenGlobalRedisFromEnvWithContext(ctx)
	if err != nil {
		t.Fatalf("OpenGlobalFromEnvWithContext() = %v", err)
	}

	if local != localWithContext {
		t.Fatal("OpenFromEnv() and OpenFromEnvWithContext() returned different clients")
	}

	if global != globalWithContext {
		t.Fatal("OpenGlobalFromEnv() and OpenGlobalFromEnvWithContext() returned different clients")
	}

	if local == global {
		t.Fatal("the local and global roles share a client")
	}

	if got, want := global.Options().DB, GlobalRedisConfigFromEnv().DB; got != want {
		t.Fatalf("global client DB = %d, want %d", got, want)
	}

	if err := PingRedisWithContext(ctx, global); err != nil {
		t.Fatalf("PingWithContext(global) = %v", err)
	}
}
