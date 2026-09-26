//go:build integration

package redis

import (
	"context"
	"testing"
	"time"
)

// Run with a live Redis:
//
//	docker run -d --name redis-it -p 6380:6379 redis:7
//	REDIS_HOST=127.0.0.1 REDIS_PORT=6380 go test -tags integration ./connection/redis/ -v

func TestOpenAgainstLiveServer(t *testing.T) {

	cfg := ConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set REDIS_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := OpenWithContext(ctx, cfg)
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

// TestOpenFromEnvReturnsTheSameClient proves the memoisation that replaces the
// per-call 1000-connection pool every service builds today.
func TestOpenFromEnvReturnsTheSameClient(t *testing.T) {

	if ConfigFromEnv().Host == "" {
		t.Skip("set REDIS_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	first, err := OpenFromEnvWithContext(ctx)
	if err != nil {
		t.Fatalf("OpenFromEnv() = %v", err)
	}

	second, err := OpenFromEnvWithContext(ctx)
	if err != nil {
		t.Fatalf("OpenFromEnv() = %v", err)
	}

	if first != second {
		t.Fatal("OpenFromEnv() built a second client for the same prefix")
	}
}

func TestOpenWithoutContextAgainstLiveServer(t *testing.T) {

	cfg := ConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set REDIS_HOST to run this")
	}

	client, err := Open(cfg)
	if err != nil {
		t.Fatalf("Open() = %v", err)
	}

	defer client.Close()

	if err := Ping(client); err != nil {
		t.Fatalf("Ping() = %v", err)
	}
}

// TestFromEnvConstructorsShareClientsPerRole proves both forms of each
// constructor hand back the same cached client, and that the global client is
// never the local one — auth must read the instance identity-service writes to.
func TestFromEnvConstructorsShareClientsPerRole(t *testing.T) {

	if ConfigFromEnv().Host == "" || GlobalConfigFromEnv().Host == "" {
		t.Skip("set REDIS_HOST and GLOBAL_REDIS_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	local, err := OpenFromEnv()
	if err != nil {
		t.Fatalf("OpenFromEnv() = %v", err)
	}

	localWithContext, err := OpenFromEnvWithContext(ctx)
	if err != nil {
		t.Fatalf("OpenFromEnvWithContext() = %v", err)
	}

	global, err := OpenGlobalFromEnv()
	if err != nil {
		t.Fatalf("OpenGlobalFromEnv() = %v", err)
	}

	globalWithContext, err := OpenGlobalFromEnvWithContext(ctx)
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

	if got, want := global.Options().DB, GlobalConfigFromEnv().DB; got != want {
		t.Fatalf("global client DB = %d, want %d", got, want)
	}

	if err := PingWithContext(ctx, global); err != nil {
		t.Fatalf("PingWithContext(global) = %v", err)
	}
}
