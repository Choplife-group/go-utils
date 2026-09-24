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

	client, err := Open(ctx, cfg)
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

	first, err := OpenFromEnv(ctx)
	if err != nil {
		t.Fatalf("OpenFromEnv() = %v", err)
	}

	second, err := OpenFromEnv(ctx)
	if err != nil {
		t.Fatalf("OpenFromEnv() = %v", err)
	}

	if first != second {
		t.Fatal("OpenFromEnv() built a second client for the same prefix")
	}
}
