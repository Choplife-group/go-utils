// Package rabbitmq opens supervised RabbitMQ connections.
//
// Settings are read from the environment:
//
//	RABBITMQ_HOST                 (required) broker host
//	RABBITMQ_PORT                 (required) broker port
//	RABBITMQ_USER                 (required) username
//	RABBITMQ_PASS                 (optional) password
//	RABBITMQ_VHOST                (optional) virtual host
//	RABBITMQ_HEARTBEAT            (optional) heartbeat in seconds, default 10
//	RABBITMQ_RECONNECT_MIN        (optional) first redial delay in seconds, default 1
//	RABBITMQ_RECONNECT_MAX        (optional) redial delay ceiling in seconds, default 30
//	QUEUE_PREFIX                  (optional) namespace prepended to queue names
//
// Dial establishes the first connection and returns an error if it cannot, so a
// broker that is down is visible at startup instead of surfacing later as a nil
// dereference. After that a supervisor goroutine owns the connection: when the
// broker drops it, the supervisor redials with jittered exponential backoff
// until it is back, and Consume re-declares its topology and resumes. Nothing in
// this package panics, calls os.Exit or logs a credential.
package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"

	"github.com/choplife-group/go-utils/connection/internal/backoff"
	"github.com/choplife-group/go-utils/connection/internal/env"
)

// Default connection settings.
const (
	DefaultHeartbeat      = 10 * time.Second
	DefaultDialTimeout    = 10 * time.Second
	DefaultReconnectMin   = 1 * time.Second
	DefaultReconnectMax   = 30 * time.Second
	DefaultPublishTimeout = 5 * time.Second
)

// Errors returned when a connection cannot serve a request. They are typed so
// callers can distinguish "come back in a moment" from "this is over".
var (
	// ErrNotConnected is returned while a redial is in flight.
	ErrNotConnected = errors.New("rabbitmq: not connected")

	// ErrClosed is returned once Close has been called.
	ErrClosed = errors.New("rabbitmq: connection closed")
)

// Config describes a RabbitMQ connection. The zero value is not usable — Host,
// Port and Username are required — but every other field has a default applied
// by Dial.
type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	VHost    string

	// URI overrides the host/port/credential fields when set.
	URI string

	// Name labels the connection in RabbitMQ's management UI.
	Name string

	Heartbeat    time.Duration
	DialTimeout  time.Duration
	ReconnectMin time.Duration
	ReconnectMax time.Duration

	// QueuePrefix namespaces every queue, exchange and routing key.
	QueuePrefix string
}

// applyDefaults fills in fields left at their zero value.
func (c *Config) applyDefaults() {

	if c.Heartbeat <= 0 {
		c.Heartbeat = DefaultHeartbeat
	}

	if c.DialTimeout <= 0 {
		c.DialTimeout = DefaultDialTimeout
	}

	if c.ReconnectMin <= 0 {
		c.ReconnectMin = DefaultReconnectMin
	}

	if c.ReconnectMax <= 0 {
		c.ReconnectMax = DefaultReconnectMax
	}

	if c.Name == "" {
		c.Name = "go-utils"
	}
}

// validate reports the first required field that is missing.
func (c *Config) validate() error {

	if c.URI != "" {
		return nil
	}

	switch {
	case c.Host == "":
		return fmt.Errorf("rabbitmq: host not configured")
	case c.Port == "":
		return fmt.Errorf("rabbitmq: port not configured")
	case c.Username == "":
		return fmt.Errorf("rabbitmq: username not configured")
	}

	return nil
}

// uri returns the AMQP URI to dial. Credentials are percent-encoded, so a
// password containing a "/" or "@" connects rather than producing a confusing
// parse failure.
func (c *Config) uri() string {

	if c.URI != "" {
		return c.URI
	}

	return fmt.Sprintf("amqp://%s:%s@%s:%s/%s",
		url.QueryEscape(c.Username),
		url.QueryEscape(c.Password),
		c.Host,
		c.Port,
		url.PathEscape(c.VHost),
	)
}

// ConfigFromEnv reads a Config from the environment.
func ConfigFromEnv() Config {

	return Config{
		Host:         env.String("RABBITMQ_HOST", ""),
		Port:         env.String("RABBITMQ_PORT", ""),
		Username:     env.String("RABBITMQ_USER", ""),
		Password:     env.String("RABBITMQ_PASS", ""),
		VHost:        env.String("RABBITMQ_VHOST", ""),
		URI:          env.String("RABBITMQ_URI", ""),
		Name:         env.String("OTEL_SERVICE_NAME", ""),
		Heartbeat:    env.Seconds("RABBITMQ_HEARTBEAT", DefaultHeartbeat),
		ReconnectMin: env.Seconds("RABBITMQ_RECONNECT_MIN", DefaultReconnectMin),
		ReconnectMax: env.Seconds("RABBITMQ_RECONNECT_MAX", DefaultReconnectMax),
		QueuePrefix:  env.String("QUEUE_PREFIX", ""),
	}
}

// Conn is a RabbitMQ connection that redials itself. It is safe for concurrent
// use, and a single Conn is meant to be shared by every publisher and consumer
// in a service.
type Conn struct {
	cfg Config

	mu     sync.RWMutex
	conn   *amqp.Connection
	ready  chan struct{}
	closed bool

	ctx    context.Context
	cancel context.CancelFunc
}

// DialFromEnv connects using the settings read from the environment.
func DialFromEnv() (*Conn, error) {

	return DialWithContext(context.Background(), ConfigFromEnv())
}

// DialFromEnvWithContext is DialFromEnv with the Conn's lifetime bound to ctx.
func DialFromEnvWithContext(ctx context.Context) (*Conn, error) {

	return DialWithContext(ctx, ConfigFromEnv())
}

// Dial validates cfg and establishes the first connection, returning an error
// if the broker cannot be reached. It never returns a non-nil Conn alongside an
// error, and never returns a nil Conn with a nil error.
//
// The returned Conn supervises itself until Close is called.
func Dial(cfg Config) (*Conn, error) {

	return DialWithContext(context.Background(), cfg)
}

// DialWithContext is Dial with the Conn also stopping when ctx is cancelled,
// so ctx should live as long as the service does.
func DialWithContext(ctx context.Context, cfg Config) (*Conn, error) {

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	cfg.applyDefaults()

	amqpConn, err := dial(cfg)
	if err != nil {
		return nil, err
	}

	supervisorCtx, cancel := context.WithCancel(ctx)

	c := &Conn{
		cfg:    cfg,
		conn:   amqpConn,
		ready:  make(chan struct{}),
		ctx:    supervisorCtx,
		cancel: cancel,
	}

	close(c.ready)

	go c.supervise(amqpConn)

	return c, nil
}

// dial opens a single connection to the broker.
func dial(cfg Config) (*amqp.Connection, error) {

	uri := cfg.uri()

	conn, err := amqp.DialConfig(uri, amqp.Config{
		Heartbeat: cfg.Heartbeat,
		Dial:      amqp.DefaultDial(cfg.DialTimeout),
		Properties: amqp.Table{
			"connection_name": cfg.Name,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("rabbitmq: dial %s: %w", env.Redact(uri), err)
	}

	if conn == nil {
		return nil, fmt.Errorf("rabbitmq: dial %s: no connection returned", env.Redact(uri))
	}

	return conn, nil
}

// supervise watches conn and redials when the broker drops it. It tracks the
// connection itself rather than reading c.conn, which dropIfDead may already
// have cleared.
func (c *Conn) supervise(conn *amqp.Connection) {

	defer recoverPanic(c.ctx, "rabbitmq supervisor")

	for {

		// The notify channel is buffered so amqp can report the close without
		// blocking on a receiver that is not there yet.
		notify := conn.NotifyClose(make(chan *amqp.Error, 1))

		select {
		case <-c.ctx.Done():
			return

		case reason := <-notify:

			if c.isClosed() {
				return
			}

			logrus.WithContext(c.ctx).
				WithFields(logrus.Fields{
					"description": "rabbitmq connection closed, reconnecting",
					"data":        closeReason(reason),
				}).
				Error(closeReason(reason))

			c.markDisconnected()

			next, ok := c.redial()
			if !ok {
				return
			}

			conn = next
		}
	}
}

// redial retries the connection until it succeeds, the Conn is closed or the
// supervisor context is cancelled. It returns the restored connection and
// whether there is one.
func (c *Conn) redial() (*amqp.Connection, bool) {

	schedule := backoff.Backoff{Min: c.cfg.ReconnectMin, Max: c.cfg.ReconnectMax}

	for {

		if c.isClosed() {
			return nil, false
		}

		if !schedule.Wait(c.ctx) {
			return nil, false
		}

		conn, err := dial(c.cfg)
		if err != nil {

			logrus.WithContext(c.ctx).
				WithFields(logrus.Fields{
					"description": "Error reconnecting to rabbitmq",
					"data":        schedule.Attempts(),
				}).
				Error(err.Error())

			continue
		}

		if c.adopt(conn) {
			return conn, true
		}

		// Close was called while we were dialling.
		conn.Close()

		return nil, false
	}
}

// adopt installs a freshly dialled connection and wakes everything waiting on
// Ready. It reports false if the Conn was closed in the meantime.
func (c *Conn) adopt(conn *amqp.Connection) bool {

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return false
	}

	c.conn = conn

	select {
	case <-c.ready:
		c.ready = make(chan struct{})
	default:
	}

	close(c.ready)

	return true
}

// markDisconnected clears the dead connection and re-arms the ready channel so
// callers block until a redial succeeds.
func (c *Conn) markDisconnected() {

	c.mu.Lock()
	defer c.mu.Unlock()

	c.conn = nil

	select {
	case <-c.ready:
		c.ready = make(chan struct{})
	default:
	}
}

// dropIfDead marks the Conn disconnected when the connection it holds has
// already closed but the supervisor has not caught up yet. Without it, a caller
// that hit the dead socket would find Ready still closed and retry on the same
// dead connection instead of waiting for the redial.
func (c *Conn) dropIfDead() {

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed || c.conn == nil || !c.conn.IsClosed() {
		return
	}

	c.conn = nil

	select {
	case <-c.ready:
		c.ready = make(chan struct{})
	default:
	}
}

// Channel opens a new AMQP channel on the current connection. It returns
// ErrNotConnected while a redial is in flight and ErrClosed once the Conn has
// been closed — never a nil channel with a nil error.
//
// Channels are not safe for concurrent use, so each consumer and each publish
// takes its own.
func (c *Conn) Channel() (*amqp.Channel, error) {

	c.mu.RLock()
	conn := c.conn
	closed := c.closed
	c.mu.RUnlock()

	if closed {
		return nil, ErrClosed
	}

	if conn == nil {
		return nil, ErrNotConnected
	}

	channel, err := conn.Channel()
	if err != nil {

		if conn.IsClosed() {

			c.dropIfDead()

			return nil, ErrNotConnected
		}

		return nil, fmt.Errorf("rabbitmq: open channel: %w", err)
	}

	if channel == nil {
		return nil, ErrNotConnected
	}

	return channel, nil
}

// IsConnected reports whether a live connection to the broker is held.
func (c *Conn) IsConnected() bool {

	c.mu.RLock()
	defer c.mu.RUnlock()

	return !c.closed && c.conn != nil
}

// isClosed reports whether Close has been called.
func (c *Conn) isClosed() bool {

	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.closed
}

// Ready returns a channel that is closed while the connection is live and open
// while a redial is in flight.
func (c *Conn) Ready() <-chan struct{} {

	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.ready
}

// WaitReady blocks until the connection is live or the Conn is closed.
func (c *Conn) WaitReady() error {

	return c.WaitReadyWithContext(context.Background())
}

// WaitReadyWithContext is WaitReady that also gives up when ctx is cancelled.
func (c *Conn) WaitReadyWithContext(ctx context.Context) error {

	if c.isClosed() {
		return ErrClosed
	}

	select {
	case <-c.Ready():

		if c.isClosed() {
			return ErrClosed
		}

		return nil

	case <-ctx.Done():
		return ctx.Err()

	case <-c.ctx.Done():
		return ErrClosed
	}
}

// Close stops the supervisor and closes the underlying connection. It is safe
// to call more than once.
func (c *Conn) Close() error {

	c.mu.Lock()

	if c.closed {
		c.mu.Unlock()

		return nil
	}

	c.closed = true
	conn := c.conn
	c.conn = nil

	select {
	case <-c.ready:
	default:
		close(c.ready)
	}

	c.mu.Unlock()

	c.cancel()

	if conn == nil {
		return nil
	}

	if err := conn.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
		return fmt.Errorf("rabbitmq: close: %w", err)
	}

	return nil
}

// name applies the configured prefix to a queue, exchange or routing key.
func (c *Conn) name(queue string) string {

	return prefixed(c.cfg.QueuePrefix, queue)
}

// QueueName returns the fully prefixed name of one of this service's own
// queues, which is what Consume binds. Use it when publishing back to a queue
// this service consumes, since Publish does not prefix on its own.
func (c *Conn) QueueName(queue string) string {

	return c.name(queue)
}

// closeReason describes why a connection or channel closed, tolerating the nil
// error amqp delivers on a clean shutdown.
func closeReason(reason *amqp.Error) string {

	if reason == nil {
		return "rabbitmq: connection closed"
	}

	return fmt.Sprintf("rabbitmq: connection closed: %s", reason.Error())
}

// recoverPanic keeps a panic in a background goroutine from taking the process
// down, reporting it to Uptrace instead. HTTP handlers are covered by Echo's
// recover middleware; the goroutines in this package are not.
func recoverPanic(ctx context.Context, where string) {

	reason := recover()
	if reason == nil {
		return
	}

	logrus.WithContext(ctx).
		WithFields(logrus.Fields{
			"description": "Error recovering from panic",
			"data":        where,
		}).
		Error(fmt.Sprintf("%v", reason))
}
