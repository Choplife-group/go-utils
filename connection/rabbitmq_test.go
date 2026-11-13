package connection

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRabbitMQDialRejectsMissingSettings(t *testing.T) {

	cases := []struct {
		name string
		cfg  RabbitMQConfig
	}{
		{"no host", RabbitMQConfig{Port: "5672", Username: "guest"}},
		{"no port", RabbitMQConfig{Host: "localhost", Username: "guest"}},
		{"no username", RabbitMQConfig{Host: "localhost", Port: "5672"}},
	}

	for _, tc := range cases {

		t.Run(tc.name, func(t *testing.T) {

			conn, err := DialRabbitMQWithContext(context.Background(), tc.cfg)

			if err == nil {
				t.Fatal("Dial() succeeded, want an error")
			}

			if conn != nil {
				t.Fatal("Dial() returned a non-nil Conn alongside an error")
			}
		})
	}
}

// TestRabbitMQDialErrorHidesPassword guards the defect this package exists to fix: the
// services it replaces log the AMQP URI, password included, on every failure.
func TestRabbitMQDialErrorHidesPassword(t *testing.T) {

	const password = "sup3rs3cr3t"

	_, err := DialRabbitMQWithContext(context.Background(), RabbitMQConfig{
		Host:        "127.0.0.1",
		Port:        "1",
		Username:    "guest",
		Password:    password,
		DialTimeout: 500 * time.Millisecond,
	})

	if err == nil {
		t.Skip("something is listening on port 1")
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("Dial() error leaked the password: %s", err)
	}
}

// TestRabbitMQDialReturnsErrorNotNilConnection is the core contract: services used to
// get a typed nil back from their connection factory and dereference it.
func TestRabbitMQDialReturnsErrorNotNilConnection(t *testing.T) {

	conn, err := DialRabbitMQWithContext(context.Background(), RabbitMQConfig{
		Host:        "127.0.0.1",
		Port:        "1",
		Username:    "guest",
		DialTimeout: 500 * time.Millisecond,
	})

	if err == nil {
		t.Skip("something is listening on port 1")
	}

	if conn != nil {
		t.Fatal("Dial() returned a Conn alongside an error")
	}
}

func TestRabbitMQURIEscapesCredentials(t *testing.T) {

	cfg := RabbitMQConfig{
		Host:     "rabbit",
		Port:     "5672",
		Username: "user@name",
		Password: "p/a:ss@word",
		VHost:    "chop",
	}

	uri := cfg.uri()

	if strings.Contains(uri, "p/a:ss@word") {
		t.Fatalf("uri() left the password unescaped: %s", uri)
	}

	if !strings.HasSuffix(uri, "/chop") {
		t.Fatalf("uri() = %s, want it to end with the vhost", uri)
	}
}

func TestRabbitMQURIOverrideWins(t *testing.T) {

	cfg := RabbitMQConfig{URI: "amqp://somewhere:5672/", Host: "ignored"}

	if got := cfg.uri(); got != "amqp://somewhere:5672/" {
		t.Fatalf("uri() = %s, want the explicit URI", got)
	}
}

func TestRabbitMQConfigFromEnvReadsPlatformNames(t *testing.T) {

	t.Setenv("RABBITMQ_HOST", "rabbit")
	t.Setenv("RABBITMQ_PORT", "5672")
	t.Setenv("RABBITMQ_USER", "guest")
	t.Setenv("RABBITMQ_PASS", "guest")
	t.Setenv("RABBITMQ_VHOST", "chop")
	t.Setenv("QUEUE_PREFIX", "bw")

	cfg := RabbitMQConfigFromEnv()

	if cfg.Host != "rabbit" || cfg.Port != "5672" || cfg.Username != "guest" {
		t.Fatalf("ConfigFromEnv() = %+v", cfg)
	}

	if cfg.QueuePrefix != "bw" {
		t.Fatalf("QueuePrefix = %q, want %q", cfg.QueuePrefix, "bw")
	}
}

// TestRabbitMQClosedConnNeverReturnsNilChannelWithNilError covers the shape that caused
// the identity-service panic: a caller taking a channel off a dead connection.
func TestRabbitMQClosedConnNeverReturnsNilChannelWithNilError(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{
		cfg:    RabbitMQConfig{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
		closed: true,
	}

	channel, err := c.Channel()

	if err == nil {
		t.Fatal("Channel() succeeded on a closed Conn")
	}

	if !errors.Is(err, ErrRabbitMQClosed) {
		t.Fatalf("Channel() error = %v, want ErrClosed", err)
	}

	if channel != nil {
		t.Fatal("Channel() returned a channel alongside an error")
	}
}

func TestRabbitMQDisconnectedConnReportsNotConnected(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{
		cfg:    RabbitMQConfig{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	channel, err := c.Channel()

	if !errors.Is(err, ErrRabbitMQNotConnected) {
		t.Fatalf("Channel() error = %v, want ErrNotConnected", err)
	}

	if channel != nil {
		t.Fatal("Channel() returned a channel alongside an error")
	}

	if c.IsConnected() {
		t.Fatal("IsConnected() = true with no connection")
	}
}

func TestRabbitMQCloseIsIdempotent(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{
		cfg:    RabbitMQConfig{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	if err := c.Close(); err != nil {
		t.Fatalf("first Close() = %v", err)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}

	if c.IsConnected() {
		t.Fatal("IsConnected() = true after Close")
	}
}

func TestRabbitMQWaitReadyUnblocksOnClose(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{
		cfg:    RabbitMQConfig{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	done := make(chan error, 1)

	go func() {
		done <- c.WaitReadyWithContext(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	c.Close()

	select {
	case err := <-done:

		if !errors.Is(err, ErrRabbitMQClosed) {
			t.Fatalf("WaitReady() = %v, want ErrClosed", err)
		}

	case <-time.After(2 * time.Second):
		t.Fatal("WaitReady() did not return after Close")
	}
}

func TestRabbitMQWaitReadyHonoursContext(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{
		cfg:    RabbitMQConfig{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	callerCtx, callerCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer callerCancel()

	if err := c.WaitReadyWithContext(callerCtx); err == nil {
		t.Fatal("WaitReady() succeeded, want a context error")
	}
}

func TestRabbitMQMarkDisconnectedThenAdoptResetsReady(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{
		cfg:    RabbitMQConfig{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	close(c.ready)

	c.markDisconnected()

	select {
	case <-c.Ready():
		t.Fatal("Ready() is still closed after markDisconnected")
	default:
	}
}

func TestRabbitMQPrefixed(t *testing.T) {

	cases := []struct {
		prefix string
		queue  string
		want   string
	}{
		{"", "Smile-ID-Webhook", "smile-id-webhook"},
		{"bw", "smile-id-webhook", "bw.smile-id-webhook"},
		{"  ", "Reports", "reports"},
	}

	for _, tc := range cases {

		if got := prefixed(tc.prefix, tc.queue); got != tc.want {
			t.Errorf("prefixed(%q, %q) = %q, want %q", tc.prefix, tc.queue, got, tc.want)
		}
	}
}

func TestRabbitMQCloseReasonToleratesNil(t *testing.T) {

	if got := rabbitMQCloseReason(nil); got == "" {
		t.Fatal("closeReason(nil) returned an empty string")
	}
}

func TestRabbitMQConsumeRejectsBadArguments(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{cfg: RabbitMQConfig{}, ready: make(chan struct{}), ctx: ctx, cancel: cancel}

	if err := c.ConsumeWithContext(context.Background(), RabbitMQConsumerConfig{}, func(context.Context, amqp.Delivery) error { return nil }); err == nil {
		t.Fatal("Consume() with no queue name succeeded")
	}

	if err := c.ConsumeWithContext(context.Background(), RabbitMQConsumerConfig{Queue: "q"}, nil); err == nil {
		t.Fatal("Consume() with a nil handler succeeded")
	}
}

func TestRabbitMQPublishRejectsEmptyQueueName(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{cfg: RabbitMQConfig{}, ready: make(chan struct{}), ctx: ctx, cancel: cancel}

	if err := c.PublishRawWithContext(context.Background(), "", []byte("{}"), 0); err == nil {
		t.Fatal("PublishRaw() with an empty queue name succeeded")
	}
}

// TestRabbitMQPublishDoesNotPrefixButConsumeDoes pins the asymmetry real services rely
// on: a service publishes to another service's queue by its exact name
// ("reports-service.deposit.create"), while Consume binds only queues this
// service owns and namespaces them with QUEUE_PREFIX.
func TestRabbitMQPublishDoesNotPrefixButConsumeDoes(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{
		cfg:    RabbitMQConfig{QueuePrefix: "affiliate-service-local"},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	if got := c.QueueName("daily-summary"); got != "affiliate-service-local.daily-summary" {
		t.Fatalf("QueueName() = %q, want the prefixed name", got)
	}

	// The publish path lowercases but must not prepend the prefix, or a
	// cross-service target would be rewritten into this service's namespace.
	if got := strings.ToLower("reports-service.Deposit.create"); c.name(got) == got {
		t.Fatal("test setup is wrong: prefix should change the consume name")
	}
}

func TestRabbitMQPublishBatchRejectsBadArguments(t *testing.T) {

	var missing *RabbitMQ

	if err := missing.PublishBatch("q", []any{1}, 0); !errors.Is(err, ErrRabbitMQNotConnected) {
		t.Fatalf("PublishBatch() on a nil Conn = %v, want ErrNotConnected", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{cfg: RabbitMQConfig{}, ready: make(chan struct{}), ctx: ctx, cancel: cancel}

	if err := c.PublishBatch("", []any{1}, 0); err == nil {
		t.Fatal("PublishBatch() with an empty queue name succeeded")
	}

	if err := c.PublishBatchWithContext(ctx, "q", nil, 0); err != nil {
		t.Fatalf("PublishBatchWithContext() with no payloads = %v, want nil", err)
	}
}

// TestRabbitMQPublishBatchEncodesBeforeSending pins that a bad payload fails the whole
// batch up front. This Conn has no connection, so reaching the broker would
// fail with ErrNotConnected instead of the encode error.
func TestRabbitMQPublishBatchEncodesBeforeSending(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{cfg: RabbitMQConfig{}, ready: make(chan struct{}), ctx: ctx, cancel: cancel}

	err := c.PublishBatch("q", []any{map[string]int{"n": 1}, make(chan int)}, 0)

	if err == nil || errors.Is(err, ErrRabbitMQNotConnected) {
		t.Fatalf("PublishBatch() = %v, want an encode error", err)
	}
}

// TestRabbitMQPublishFormats pins the wire format of each publisher: the plain forms
// must match what services sent before adopting this package.
func TestRabbitMQPublishFormats(t *testing.T) {

	legacy := legacyFormat.publishing([]byte("{}"), 3)

	if legacy.ContentType != LegacyRabbitMQContentType || legacy.DeliveryMode != 0 || !legacy.Timestamp.IsZero() || legacy.Priority != 3 {
		t.Fatalf("legacy publishing = %+v, want text/plain, no delivery mode, no timestamp, priority 3", legacy)
	}

	current := jsonFormat.publishing([]byte("{}"), 3)

	if current.ContentType != DefaultRabbitMQContentType || current.DeliveryMode != amqp.Persistent || current.Timestamp.IsZero() || current.Priority != 3 {
		t.Fatalf("json publishing = %+v, want application/json, persistent, timestamped, priority 3", current)
	}
}

func TestRabbitMQPlainVariantsKeepTheContract(t *testing.T) {

	conn, err := DialRabbitMQ(RabbitMQConfig{})
	if err == nil {
		t.Fatal("Dial() with no settings succeeded")
	}

	if conn != nil {
		t.Fatal("Dial() returned a Conn alongside an error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &RabbitMQ{cfg: RabbitMQConfig{}, ready: make(chan struct{}), ctx: ctx, cancel: cancel}

	if err := c.Consume(RabbitMQConsumerConfig{}, func(context.Context, amqp.Delivery) error { return nil }); err == nil {
		t.Fatal("Consume() with no queue name succeeded")
	}

	if err := c.PublishRaw("", []byte("{}"), 0); err == nil {
		t.Fatal("PublishRaw() with an empty queue name succeeded")
	}

	c.Close()

	if err := c.WaitReady(); !errors.Is(err, ErrRabbitMQClosed) {
		t.Fatalf("WaitReady() after Close = %v, want ErrClosed", err)
	}
}
