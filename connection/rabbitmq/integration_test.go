//go:build integration

package rabbitmq

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sync"
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

	conn, err := DialWithContext(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	defer conn.Close()

	queue := fmt.Sprintf("go-utils-it-%d", time.Now().UnixNano())
	received := make(chan string, 1)

	go conn.ConsumeWithContext(ctx, ConsumerConfig{Queue: queue}, func(_ context.Context, delivery amqp.Delivery) error {

		received <- string(delivery.Body)

		return nil
	})

	// Give the consumer a moment to declare and bind its topology.
	time.Sleep(2 * time.Second)

	if err := conn.PublishWithContext(ctx, queue, map[string]string{"hello": "world"}, 0); err != nil {
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

	conn, err := DialWithContext(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	defer conn.Close()

	queue := fmt.Sprintf("go-utils-it-panic-%d", time.Now().UnixNano())
	survived := make(chan string, 1)
	first := true

	go conn.ConsumeWithContext(ctx, ConsumerConfig{Queue: queue}, func(_ context.Context, delivery amqp.Delivery) error {

		if first {
			first = false

			panic("deliberate panic in handler")
		}

		survived <- string(delivery.Body)

		return nil
	})

	time.Sleep(2 * time.Second)

	if err := conn.PublishWithContext(ctx, queue, map[string]string{"n": "1"}, 0); err != nil {
		t.Fatalf("Publish() = %v", err)
	}

	time.Sleep(time.Second)

	if err := conn.PublishWithContext(ctx, queue, map[string]string{"n": "2"}, 0); err != nil {
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

	conn, err := DialWithContext(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	defer conn.Close()

	queue := fmt.Sprintf("go-utils-it-restart-%d", time.Now().UnixNano())
	received := make(chan string, 4)

	go conn.ConsumeWithContext(ctx, ConsumerConfig{Queue: queue}, func(_ context.Context, delivery amqp.Delivery) error {

		received <- string(delivery.Body)

		return nil
	})

	time.Sleep(2 * time.Second)

	if err := conn.PublishWithContext(ctx, queue, map[string]string{"phase": "before"}, 0); err != nil {
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

		if err := conn.PublishWithContext(ctx, queue, map[string]string{"phase": "after"}, 0); err == nil {
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

// TestPublishBatchDeliversInOrder proves a batch arrives complete, in order and
// in the JSON format the WithContext publishers use.
func TestPublishBatchDeliversInOrder(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := DialWithContext(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	defer conn.Close()

	const total = 500

	queue := fmt.Sprintf("go-utils-it-%d", time.Now().UnixNano())
	received := make(chan amqp.Delivery, total)

	go conn.ConsumeWithContext(ctx, ConsumerConfig{Queue: queue, PrefetchCount: 50}, func(_ context.Context, delivery amqp.Delivery) error {

		received <- delivery

		return nil
	})

	time.Sleep(2 * time.Second)

	payloads := make([]any, total)

	for i := range payloads {
		payloads[i] = map[string]int{"n": i}
	}

	if err := conn.PublishBatchWithContext(ctx, queue, payloads, 0); err != nil {
		t.Fatalf("PublishBatchWithContext() = %v", err)
	}

	for i := 0; i < total; i++ {

		select {
		case delivery := <-received:

			if want := fmt.Sprintf(`{"n":%d}`, i); string(delivery.Body) != want {
				t.Fatalf("delivery %d = %s, want %s", i, delivery.Body, want)
			}

			if delivery.ContentType != DefaultContentType || delivery.DeliveryMode != amqp.Persistent {
				t.Fatalf("delivery %d content type %q mode %d, want %q persistent", i, delivery.ContentType, delivery.DeliveryMode, DefaultContentType)
			}

		case <-time.After(10 * time.Second):
			t.Fatalf("received %d of %d deliveries", i, total)
		}
	}
}

// TestPlainVariantsKeepTheLegacyWireFormat drives the no-ctx forms end to end
// and checks Publish and PublishBatch send exactly what services' own
// publishers did: text/plain and not persistent.
func TestPlainVariantsKeepTheLegacyWireFormat(t *testing.T) {

	conn, err := Dial(testConfig(t))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	defer conn.Close()

	queue := fmt.Sprintf("go-utils-it-%d", time.Now().UnixNano())
	received := make(chan amqp.Delivery, 3)

	go conn.Consume(ConsumerConfig{Queue: queue}, func(_ context.Context, delivery amqp.Delivery) error {

		received <- delivery

		return nil
	})

	time.Sleep(2 * time.Second)

	if err := conn.Publish(queue, map[string]int{"n": 0}, 0); err != nil {
		t.Fatalf("Publish() = %v", err)
	}

	if err := conn.PublishBatch(queue, []any{map[string]int{"n": 1}, map[string]int{"n": 2}}, 0); err != nil {
		t.Fatalf("PublishBatch() = %v", err)
	}

	for i := 0; i < 3; i++ {

		select {
		case delivery := <-received:

			if want := fmt.Sprintf(`{"n":%d}`, i); string(delivery.Body) != want {
				t.Fatalf("delivery %d = %s, want %s", i, delivery.Body, want)
			}

			if delivery.ContentType != LegacyContentType || delivery.DeliveryMode == amqp.Persistent {
				t.Fatalf("delivery %d content type %q mode %d, want %q and not persistent", i, delivery.ContentType, delivery.DeliveryMode, LegacyContentType)
			}

		case <-time.After(10 * time.Second):
			t.Fatalf("received %d of 3 deliveries", i)
		}
	}
}

// TestFromEnvAndRawPublishForms dials with both environment-driven
// constructors and checks each raw publisher's body and wire format.
func TestFromEnvAndRawPublishForms(t *testing.T) {

	if ConfigFromEnv().Host == "" {
		t.Skip("set RABBITMQ_HOST to run this")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := DialFromEnv()
	if err != nil {
		t.Fatalf("DialFromEnv() = %v", err)
	}

	defer conn.Close()

	withContext, err := DialFromEnvWithContext(ctx)
	if err != nil {
		t.Fatalf("DialFromEnvWithContext() = %v", err)
	}

	defer withContext.Close()

	queue := fmt.Sprintf("go-utils-it-%d", time.Now().UnixNano())
	received := make(chan amqp.Delivery, 2)

	go conn.ConsumeWithContext(ctx, ConsumerConfig{Queue: queue}, func(_ context.Context, delivery amqp.Delivery) error {

		received <- delivery

		return nil
	})

	time.Sleep(2 * time.Second)

	target := conn.QueueName(queue)

	if err := conn.PublishRaw(target, []byte(`{"form":"raw"}`), 0); err != nil {
		t.Fatalf("PublishRaw() = %v", err)
	}

	if err := withContext.PublishRawWithContext(ctx, target, []byte(`{"form":"raw-ctx"}`), 0); err != nil {
		t.Fatalf("PublishRawWithContext() = %v", err)
	}

	want := map[string]string{`{"form":"raw"}`: LegacyContentType, `{"form":"raw-ctx"}`: DefaultContentType}

	for range 2 {

		select {
		case delivery := <-received:

			contentType, ok := want[string(delivery.Body)]
			if !ok {
				t.Fatalf("received unexpected %s", delivery.Body)
			}

			if delivery.ContentType != contentType {
				t.Fatalf("%s content type = %q, want %q", delivery.Body, delivery.ContentType, contentType)
			}

			delete(want, string(delivery.Body))

		case <-time.After(10 * time.Second):
			t.Fatalf("messages not received: %v", want)
		}
	}
}

// TestConsumeStopsOnCancelOrClose proves ConsumeWithContext returns when its
// ctx is cancelled and Consume returns when the Conn is closed, so neither
// leaks a goroutine on shutdown.
func TestConsumeStopsOnCancelOrClose(t *testing.T) {

	conn, err := Dial(testConfig(t))
	if err != nil {
		t.Fatalf("Dial() = %v", err)
	}

	defer conn.Close()

	handler := func(context.Context, amqp.Delivery) error { return nil }

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := make(chan error, 1)

	go func() {
		cancelled <- conn.ConsumeWithContext(ctx, ConsumerConfig{Queue: fmt.Sprintf("go-utils-it-%d", time.Now().UnixNano())}, handler)
	}()

	closed := make(chan error, 1)

	go func() {
		closed <- conn.Consume(ConsumerConfig{Queue: fmt.Sprintf("go-utils-it-%d-b", time.Now().UnixNano())}, handler)
	}()

	time.Sleep(2 * time.Second)

	cancel()

	select {
	case err := <-cancelled:

		if err != nil {
			t.Fatalf("ConsumeWithContext() = %v, want nil", err)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("ConsumeWithContext() did not return after its ctx was cancelled")
	}

	select {
	case <-closed:
		t.Fatal("Consume() returned although the Conn is still open")
	default:
	}

	conn.Close()

	select {
	case err := <-closed:

		if err != nil {
			t.Fatalf("Consume() = %v, want nil", err)
		}

	case <-time.After(5 * time.Second):
		t.Fatal("Consume() did not return after Close")
	}
}

// TestPublishBatchResumesAfterConnectionDrop drops the publisher's socket
// part-way through a batch and checks the batch resumes from the message that
// failed: it must finish, reach the last message and send nothing twice.
//
// Messages already written to the socket when it dies can be lost, since
// publishing is unconfirmed; the test logs how many rather than failing on it.
func TestPublishBatchResumesAfterConnectionDrop(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	publisher, err := DialWithContext(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("Dial() publisher = %v", err)
	}

	defer publisher.Close()

	consumer, err := DialWithContext(ctx, testConfig(t))
	if err != nil {
		t.Fatalf("Dial() consumer = %v", err)
	}

	defer consumer.Close()

	const total = 100000

	queue := fmt.Sprintf("go-utils-it-%d", time.Now().UnixNano())

	var (
		mu      sync.Mutex
		seen    = make(map[int]int, total)
		started = make(chan struct{})
		once    sync.Once
	)

	go consumer.ConsumeWithContext(ctx, ConsumerConfig{Queue: queue, PrefetchCount: 1000}, func(_ context.Context, delivery amqp.Delivery) error {

		var message struct {
			N int `json:"n"`
		}

		if err := json.Unmarshal(delivery.Body, &message); err != nil {
			return err
		}

		mu.Lock()
		seen[message.N]++
		mu.Unlock()

		once.Do(func() { close(started) })

		return nil
	})

	time.Sleep(2 * time.Second)

	payloads := make([]any, total)

	for i := range payloads {
		payloads[i] = map[string]int{"n": i}
	}

	done := make(chan error, 1)

	go func() {
		done <- publisher.PublishBatchWithContext(ctx, publisher.QueueName(queue), payloads, 0)
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the batch never started arriving")
	}

	publisher.mu.RLock()
	socket := publisher.conn
	publisher.mu.RUnlock()

	select {
	case err := <-done:
		t.Skipf("the batch finished before the drop (err %v); raise total", err)
	default:
	}

	if socket == nil {
		t.Fatal("publisher has no live connection to drop")
	}

	socket.Close()

	select {
	case err := <-done:

		if err != nil {
			t.Fatalf("PublishBatchWithContext() after a drop = %v, want nil", err)
		}

	case <-time.After(60 * time.Second):
		t.Fatal("PublishBatchWithContext() did not return after the drop")
	}

	deadline := time.Now().Add(60 * time.Second)

	for time.Now().Before(deadline) {

		mu.Lock()
		last := seen[total-1]
		mu.Unlock()

		if last > 0 {
			break
		}

		time.Sleep(200 * time.Millisecond)
	}

	// Let anything still queued ahead of the last message drain.
	time.Sleep(2 * time.Second)

	mu.Lock()
	defer mu.Unlock()

	if seen[total-1] == 0 {
		t.Fatal("the last message never arrived, so the batch did not resume to the end")
	}

	var missing, duplicated int

	for i := 0; i < total; i++ {

		switch seen[i] {
		case 0:
			missing++
		case 1:
		default:
			duplicated++
		}
	}

	if duplicated > 0 {
		t.Fatalf("after the drop: %d of %d sent twice, want none", duplicated, total)
	}

	t.Logf("after the drop: %d of %d in flight on the dead socket were lost", missing, total)
}
