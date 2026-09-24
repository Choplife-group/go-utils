// Package mqtt opens MQTT clients for real-time publishing to EMQX.
//
// Settings are read from the environment:
//
//	MQTT_HOST                  (required) broker host
//	MQTT_PORT                  (required) broker port
//	MQTT_USERNAME              (optional) username
//	MQTT_PASSWORD              (optional) password
//	MQTT_SCHEME                (optional) "tcp", "ssl" or "ws", default "tcp"
//	CLIENT_ID                  (optional) client id prefix, default OTEL_SERVICE_NAME
//	MQTT_KEEP_ALIVE            (optional) keep-alive in seconds, default 120
//	MQTT_CONNECT_TIMEOUT       (optional) connect timeout in seconds, default 10
//	MQTT_MAX_RECONNECT_INTERVAL (optional) reconnect ceiling in seconds, default 30
//
// Reconnection is handled by the paho client, which redials on its own with a
// bounded interval. Connect returns the mqtt.Client interface by value; the
// services this replaces returned *mqtt.Client, a pointer to an interface, which
// is why their publish paths are littered with nil checks.
package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/choplife-group/go-utils/connection/internal/env"
)

// Default client settings.
const (
	DefaultScheme                    = "tcp"
	DefaultKeepAlive                 = 120 * time.Second
	DefaultConnectTimeout            = 10 * time.Second
	DefaultMaxReconnectInterval      = 30 * time.Second
	DefaultConnectRetryInterval      = 2 * time.Second
	DefaultPublishTimeout            = 5 * time.Second
	DefaultQoS                  byte = 0
)

// Config describes an MQTT client. The zero value is not usable — Host and Port
// are required — but every other field has a default applied by Connect.
type Config struct {
	Host     string
	Port     string
	Username string
	Password string

	// Scheme is the broker transport: "tcp", "ssl" or "ws".
	Scheme string

	// ClientIDPrefix is suffixed with a timestamp to form the client id, since
	// a broker rejects a second connection using an id already in use.
	ClientIDPrefix string

	KeepAlive            time.Duration
	ConnectTimeout       time.Duration
	ConnectRetryInterval time.Duration
	MaxReconnectInterval time.Duration

	// CleanSession discards any broker-side session on connect. The platform
	// publishes transient websocket updates, so this defaults to true.
	CleanSession bool

	// OnConnect and OnConnectionLost are optional observers. They must not
	// block: paho invokes them on its own connection goroutine.
	OnConnect        func()
	OnConnectionLost func(error)
}

// applyDefaults fills in fields left at their zero value.
func (c *Config) applyDefaults() {

	if c.Scheme == "" {
		c.Scheme = DefaultScheme
	}

	if c.ClientIDPrefix == "" {

		c.ClientIDPrefix = os.Getenv("OTEL_SERVICE_NAME")

		if c.ClientIDPrefix == "" {
			c.ClientIDPrefix = "service"
		}
	}

	if c.KeepAlive <= 0 {
		c.KeepAlive = DefaultKeepAlive
	}

	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = DefaultConnectTimeout
	}

	if c.ConnectRetryInterval <= 0 {
		c.ConnectRetryInterval = DefaultConnectRetryInterval
	}

	if c.MaxReconnectInterval <= 0 {
		c.MaxReconnectInterval = DefaultMaxReconnectInterval
	}
}

// validate reports the first required field that is missing.
func (c *Config) validate() error {

	switch {
	case c.Host == "":
		return fmt.Errorf("mqtt: host not configured")
	case c.Port == "":
		return fmt.Errorf("mqtt: port not configured")
	}

	return nil
}

// broker returns the broker URI the client dials.
func (c *Config) broker() string {

	return fmt.Sprintf("%s://%s:%s", c.Scheme, c.Host, c.Port)
}

// ConfigFromEnv reads a Config from the environment.
func ConfigFromEnv() Config {

	return Config{
		Host:                 env.String("MQTT_HOST", ""),
		Port:                 env.String("MQTT_PORT", ""),
		Username:             env.String("MQTT_USERNAME", ""),
		Password:             env.String("MQTT_PASSWORD", ""),
		Scheme:               env.String("MQTT_SCHEME", DefaultScheme),
		ClientIDPrefix:       env.String("CLIENT_ID", ""),
		KeepAlive:            env.Seconds("MQTT_KEEP_ALIVE", DefaultKeepAlive),
		ConnectTimeout:       env.Seconds("MQTT_CONNECT_TIMEOUT", DefaultConnectTimeout),
		MaxReconnectInterval: env.Seconds("MQTT_MAX_RECONNECT_INTERVAL", DefaultMaxReconnectInterval),
		CleanSession:         env.Bool("MQTT_CLEAN_SESSION", true),
	}
}

// ConnectFromEnv connects using the settings read from the environment.
func ConnectFromEnv(ctx context.Context) (paho.Client, error) {

	return Connect(ctx, ConfigFromEnv())
}

// Connect validates cfg and establishes a client, waiting up to ConnectTimeout
// for the first connection. It never returns a non-nil client alongside an
// error, and never returns a nil client with a nil error.
//
// Once connected, the client redials on its own if the broker goes away, so
// callers hold on to it for the lifetime of the process.
func Connect(ctx context.Context, cfg Config) (paho.Client, error) {

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	cfg.applyDefaults()

	opts := paho.NewClientOptions()
	opts.AddBroker(cfg.broker())
	opts.SetClientID(fmt.Sprintf("%s-%d", cfg.ClientIDPrefix, time.Now().UnixNano()))
	opts.SetUsername(cfg.Username)
	opts.SetPassword(cfg.Password)
	opts.SetKeepAlive(cfg.KeepAlive)
	opts.SetCleanSession(cfg.CleanSession)
	opts.SetConnectTimeout(cfg.ConnectTimeout)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(cfg.ConnectRetryInterval)
	opts.SetMaxReconnectInterval(cfg.MaxReconnectInterval)
	opts.SetOrderMatters(false)

	if cfg.OnConnect != nil {

		opts.OnConnect = func(paho.Client) {
			cfg.OnConnect()
		}
	}

	if cfg.OnConnectionLost != nil {

		opts.OnConnectionLost = func(_ paho.Client, err error) {
			cfg.OnConnectionLost(err)
		}
	}

	client := paho.NewClient(opts)

	token := client.Connect()

	if !waitToken(ctx, token, cfg.ConnectTimeout) {

		client.Disconnect(0)

		return nil, fmt.Errorf("mqtt: connect %s: timed out after %s", cfg.broker(), cfg.ConnectTimeout)
	}

	if err := token.Error(); err != nil {

		client.Disconnect(0)

		return nil, fmt.Errorf("mqtt: connect %s: %w", cfg.broker(), err)
	}

	return client, nil
}

// Publish marshals payload as JSON and publishes it to topic at QoS 0. A nil or
// disconnected client is reported as an error rather than panicking or being
// silently dropped.
func Publish(ctx context.Context, client paho.Client, topic string, payload any) error {

	return PublishWithQoS(ctx, client, topic, payload, DefaultQoS, false)
}

// PublishWithQoS publishes at an explicit quality of service, optionally
// retaining the message on the broker.
func PublishWithQoS(ctx context.Context, client paho.Client, topic string, payload any, qos byte, retained bool) error {

	if client == nil {
		return fmt.Errorf("mqtt: not initialised")
	}

	// IsConnectionOpen, not IsConnected: with auto-reconnect enabled paho's
	// IsConnected stays true for the whole outage because it reports "the
	// client is managing a connection", not "the socket is up". Guarding on it
	// would hand the message to a dead connection and surface as a token
	// timeout instead of a clear error.
	if !client.IsConnectionOpen() {
		return fmt.Errorf("mqtt: not connected")
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mqtt: encode payload for %s: %w", topic, err)
	}

	token := client.Publish(topic, qos, retained, body)

	if !waitToken(ctx, token, DefaultPublishTimeout) {
		return fmt.Errorf("mqtt: publish %s: timed out after %s", topic, DefaultPublishTimeout)
	}

	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: publish %s: %w", topic, err)
	}

	return nil
}

// waitToken reports whether token completed before ctx was cancelled or the
// timeout elapsed. paho's own Wait blocks uninterruptibly, so the token is
// polled through its Done channel instead to keep cancellation working.
func waitToken(ctx context.Context, token paho.Token, timeout time.Duration) bool {

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-token.Done():
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}
