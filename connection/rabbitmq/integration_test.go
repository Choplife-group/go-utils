//go:build integration

package rabbitmq

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// These tests need a live broker and are excluded from the default build, so
// the tag-and-release workflow's `go build ./... && go vet ./...` gate stays
// green without one. Run them with:
//
//	docker run -d --name rabbitmq-it -p 5672:5672 rabbitmq:3-management
//	go test -tags integration ./connection/rabbitmq/ -v

// testConfig points at the broker described by the environment, defaulting to a
// local container.
func testConfig(t *testing.T) Config {

	t.Helper()

	cfg := ConfigFromEnv()

	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}

	if cfg.Port == "" {
		cfg.Port = "5672"
	}

	if cfg.Username == "" {
		cfg.Username = "guest"
		cfg.Password = "guest"
	}

	cfg.ReconnectMin = 500 * time.Millisecond
	cfg.ReconnectMax = 3 * time.Second

	return cfg
}

// TestPublishAndConsume proves the happy path end to end.
func TestPublishAndConsume(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := Dial(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	defer conn.Close()

	queue := fmt.Sprintf("go-utils-it-%d", time.Now().UnixNano())
	received := make(chan string, 1)

	go conn.Consume(ctx, ConsumerConfig{Queue: queue}, func(_ context.Context, delivery amqp.Delivery) error {

		received <- string(delivery.Body)

		return nil
	})

	// Give the consumer a moment to declare and bind its topology.
	time.Sleep(2 * time.Second)

	if err := conn.Publish(ctx, queue, map[string]string{"hello": "world"}, 0); err != nil {
		t.Fatalf("Publish() = %v", err)
	}

	select {
	case body := <-received:

		if body != `{"hello":"world"}` {
			t.Fatalf("received %s", body)
		}

	case <-time.After(10 * time.Second):
		t.Fatal("no delivery received")
	}
}

// TestHandlerPanicDoesNotKillTheConsumer covers the failure mode that takes
// services down today: a panic inside a queue goroutine, where Echo's recover
// middleware cannot reach it.
func TestHandlerPanicDoesNotKillTheConsumer(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := Dial(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	defer conn.Close()

	queue := fmt.Sprintf("go-utils-it-panic-%d", time.Now().UnixNano())
	survived := make(chan string, 1)
	first := true

	go conn.Consume(ctx, ConsumerConfig{Queue: queue}, func(_ context.Context, delivery amqp.Delivery) error {

		if first {
			first = false

			panic("deliberate panic in handler")
		}

		survived <- string(delivery.Body)

		return nil
	})

	time.Sleep(2 * time.Second)

	if err := conn.Publish(ctx, queue, map[string]string{"n": "1"}, 0); err != nil {
		t.Fatalf("Publish() = %v", err)
	}

	time.Sleep(time.Second)

	if err := conn.Publish(ctx, queue, map[string]string{"n": "2"}, 0); err != nil {
		t.Fatalf("Publish() = %v", err)
	}

	select {
	case <-survived:
		// The consumer processed a message after the panic.

	case <-time.After(15 * time.Second):
		t.Fatal("consumer did not survive a panicking handler")
	}
}

// TestReconnectAfterBrokerRestart is the test this package exists for. It needs
// a container it is allowed to restart, named by RABBITMQ_CONTAINER.
func TestReconnectAfterBrokerRestart(t *testing.T) {

	container := os.Getenv("RABBITMQ_CONTAINER")
	if container == "" {
		t.Skip("set RABBITMQ_CONTAINER to the broker container name to run this")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := Dial(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	defer conn.Close()

	queue := fmt.Sprintf("go-utils-it-restart-%d", time.Now().UnixNano())
	received := make(chan string, 4)

	go conn.Consume(ctx, ConsumerConfig{Queue: queue}, func(_ context.Context, delivery amqp.Delivery) error {

		received <- string(delivery.Body)

		return nil
	})

	time.Sleep(2 * time.Second)

	if err := conn.Publish(ctx, queue, map[string]string{"phase": "before"}, 0); err != nil {
		t.Fatalf("Publish() before restart = %v", err)
	}

	select {
	case <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("no delivery before the restart")
	}

	if out, err := exec.Command("docker", "restart", container).CombinedOutput(); err != nil {
		t.Fatalf("docker restart %s: %v: %s", container, err, out)
	}

	// The connection must drop and come back on its own, without the process
	// exiting and without any caller intervention.
	if !waitFor(func() bool { return !conn.IsConnected() }, 30*time.Second) {
		t.Fatal("connection never registered as dropped")
	}

	if !waitFor(conn.IsConnected, 60*time.Second) {
		t.Fatal("connection never came back")
	}

	// Publishing after the restart must work, which means the consumer
	// re-declared its topology on the fresh connection.
	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {

		if err := conn.Publish(ctx, queue, map[string]string{"phase": "after"}, 0); err == nil {
			break
		}

		time.Sleep(time.Second)
	}

	select {
	case <-received:
	case <-time.After(30 * time.Second):
		t.Fatal("no delivery after the restart")
	}
}

// waitFor polls condition until it holds or the timeout elapses.
func waitFor(condition func() bool, timeout time.Duration) bool {

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {

		if condition() {
			return true
		}

		time.Sleep(200 * time.Millisecond)
	}

	return false
}
