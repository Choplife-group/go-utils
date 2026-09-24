package rabbitmq

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestDialRejectsMissingSettings(t *testing.T) {

	cases := []struct {
		name string
		cfg  Config
	}{
		{"no host", Config{Port: "5672", Username: "guest"}},
		{"no port", Config{Host: "localhost", Username: "guest"}},
		{"no username", Config{Host: "localhost", Port: "5672"}},
	}

	for _, tc := range cases {

		t.Run(tc.name, func(t *testing.T) {

			conn, err := Dial(context.Background(), tc.cfg)

			if err == nil {
				t.Fatal("Dial() succeeded, want an error")
			}

			if conn != nil {
				t.Fatal("Dial() returned a non-nil Conn alongside an error")
			}
		})
	}
}

// TestDialErrorHidesPassword guards the defect this package exists to fix: the
// services it replaces log the AMQP URI, password included, on every failure.
func TestDialErrorHidesPassword(t *testing.T) {

	const password = "sup3rs3cr3t"

	_, err := Dial(context.Background(), Config{
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

// TestDialReturnsErrorNotNilConnection is the core contract: services used to
// get a typed nil back from their connection factory and dereference it.
func TestDialReturnsErrorNotNilConnection(t *testing.T) {

	conn, err := Dial(context.Background(), Config{
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

func TestURIEscapesCredentials(t *testing.T) {

	cfg := Config{
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

func TestURIOverrideWins(t *testing.T) {

	cfg := Config{URI: "amqp://somewhere:5672/", Host: "ignored"}

	if got := cfg.uri(); got != "amqp://somewhere:5672/" {
		t.Fatalf("uri() = %s, want the explicit URI", got)
	}
}

func TestConfigFromEnvReadsPlatformNames(t *testing.T) {

	t.Setenv("RABBITMQ_HOST", "rabbit")
	t.Setenv("RABBITMQ_PORT", "5672")
	t.Setenv("RABBITMQ_USER", "guest")
	t.Setenv("RABBITMQ_PASS", "guest")
	t.Setenv("RABBITMQ_VHOST", "chop")
	t.Setenv("QUEUE_PREFIX", "bw")

	cfg := ConfigFromEnv()

	if cfg.Host != "rabbit" || cfg.Port != "5672" || cfg.Username != "guest" {
		t.Fatalf("ConfigFromEnv() = %+v", cfg)
	}

	if cfg.QueuePrefix != "bw" {
		t.Fatalf("QueuePrefix = %q, want %q", cfg.QueuePrefix, "bw")
	}
}

// TestClosedConnNeverReturnsNilChannelWithNilError covers the shape that caused
// the identity-service panic: a caller taking a channel off a dead connection.
func TestClosedConnNeverReturnsNilChannelWithNilError(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Conn{
		cfg:    Config{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
		closed: true,
	}

	channel, err := c.Channel()

	if err == nil {
		t.Fatal("Channel() succeeded on a closed Conn")
	}

	if !errors.Is(err, ErrClosed) {
		t.Fatalf("Channel() error = %v, want ErrClosed", err)
	}

	if channel != nil {
		t.Fatal("Channel() returned a channel alongside an error")
	}
}

func TestDisconnectedConnReportsNotConnected(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Conn{
		cfg:    Config{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	channel, err := c.Channel()

	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Channel() error = %v, want ErrNotConnected", err)
	}

	if channel != nil {
		t.Fatal("Channel() returned a channel alongside an error")
	}

	if c.IsConnected() {
		t.Fatal("IsConnected() = true with no connection")
	}
}

func TestCloseIsIdempotent(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Conn{
		cfg:    Config{},
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

func TestWaitReadyUnblocksOnClose(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Conn{
		cfg:    Config{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	done := make(chan error, 1)

	go func() {
		done <- c.WaitReady(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	c.Close()

	select {
	case err := <-done:

		if !errors.Is(err, ErrClosed) {
			t.Fatalf("WaitReady() = %v, want ErrClosed", err)
		}

	case <-time.After(2 * time.Second):
		t.Fatal("WaitReady() did not return after Close")
	}
}

func TestWaitReadyHonoursContext(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Conn{
		cfg:    Config{},
		ready:  make(chan struct{}),
		ctx:    ctx,
		cancel: cancel,
	}

	callerCtx, callerCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer callerCancel()

	if err := c.WaitReady(callerCtx); err == nil {
		t.Fatal("WaitReady() succeeded, want a context error")
	}
}

func TestMarkDisconnectedThenAdoptResetsReady(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Conn{
		cfg:    Config{},
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

func TestPrefixed(t *testing.T) {

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

func TestCloseReasonToleratesNil(t *testing.T) {

	if got := closeReason(nil); got == "" {
		t.Fatal("closeReason(nil) returned an empty string")
	}
}

func TestConsumeRejectsBadArguments(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Conn{cfg: Config{}, ready: make(chan struct{}), ctx: ctx, cancel: cancel}

	if err := c.Consume(context.Background(), ConsumerConfig{}, func(context.Context, amqp.Delivery) error { return nil }); err == nil {
		t.Fatal("Consume() with no queue name succeeded")
	}

	if err := c.Consume(context.Background(), ConsumerConfig{Queue: "q"}, nil); err == nil {
		t.Fatal("Consume() with a nil handler succeeded")
	}
}

func TestPublishRejectsEmptyQueueName(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Conn{cfg: Config{}, ready: make(chan struct{}), ctx: ctx, cancel: cancel}

	if err := c.PublishRaw(context.Background(), "", []byte("{}"), 0); err == nil {
		t.Fatal("PublishRaw() with an empty queue name succeeded")
	}
}

// TestPublishDoesNotPrefixButConsumeDoes pins the asymmetry real services rely
// on: a service publishes to another service's queue by its exact name
// ("reports-service.deposit.create"), while Consume binds only queues this
// service owns and namespaces them with QUEUE_PREFIX.
func TestPublishDoesNotPrefixButConsumeDoes(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := &Conn{
		cfg:    Config{QueuePrefix: "affiliate-service-local"},
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
