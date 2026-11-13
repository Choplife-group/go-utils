// ConnectMQTT opens MQTT clients for real-time publishing to EMQX.
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
// bounded interval. ConnectMQTT returns the paho.Client interface by value; the
// services this replaces returned *mqtt.Client, a pointer to an interface, which
// is why their publish paths are littered with nil checks.

package connection

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
	DefaultMQTTScheme                    = "tcp"
	DefaultMQTTKeepAlive                 = 120 * time.Second
	DefaultMQTTConnectTimeout            = 10 * time.Second
	DefaultMQTTMaxReconnectInterval      = 30 * time.Second
	DefaultMQTTConnectRetryInterval      = 2 * time.Second
	DefaultMQTTPublishTimeout            = 5 * time.Second
	DefaultMQTTQoS                  byte = 0
)

// MQTTConfig describes an MQTT client. The zero value is not usable — Host and Port
// are required — but every other field has a default applied by ConnectMQTT.
type MQTTConfig struct {
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
func (c *MQTTConfig) applyDefaults() {

	if c.Scheme == "" {
		c.Scheme = DefaultMQTTScheme
	}

	if c.ClientIDPrefix == "" {

		c.ClientIDPrefix = os.Getenv("OTEL_SERVICE_NAME")

		if c.ClientIDPrefix == "" {
			c.ClientIDPrefix = "service"
		}
	}

	if c.KeepAlive <= 0 {
		c.KeepAlive = DefaultMQTTKeepAlive
	}

	if c.ConnectTimeout <= 0 {
		c.ConnectTimeout = DefaultMQTTConnectTimeout
	}

	if c.ConnectRetryInterval <= 0 {
		c.ConnectRetryInterval = DefaultMQTTConnectRetryInterval
	}

	if c.MaxReconnectInterval <= 0 {
		c.MaxReconnectInterval = DefaultMQTTMaxReconnectInterval
	}
}

// validate reports the first required field that is missing.
func (c *MQTTConfig) validate() error {

	switch {
	case c.Host == "":
		return fmt.Errorf("mqtt: host not configured")
	case c.Port == "":
		return fmt.Errorf("mqtt: port not configured")
	}

	return nil
}

// broker returns the broker URI the client dials.
func (c *MQTTConfig) broker() string {

	return fmt.Sprintf("%s://%s:%s", c.Scheme, c.Host, c.Port)
}

// MQTTConfigFromEnv reads an MQTTConfig from the environment.
func MQTTConfigFromEnv() MQTTConfig {

	return MQTTConfig{
		Host:                 env.String("MQTT_HOST", ""),
		Port:                 env.String("MQTT_PORT", ""),
		Username:             env.String("MQTT_USERNAME", ""),
		Password:             env.String("MQTT_PASSWORD", ""),
		Scheme:               env.String("MQTT_SCHEME", DefaultMQTTScheme),
		ClientIDPrefix:       env.String("CLIENT_ID", ""),
		KeepAlive:            env.Seconds("MQTT_KEEP_ALIVE", DefaultMQTTKeepAlive),
		ConnectTimeout:       env.Seconds("MQTT_CONNECT_TIMEOUT", DefaultMQTTConnectTimeout),
		MaxReconnectInterval: env.Seconds("MQTT_MAX_RECONNECT_INTERVAL", DefaultMQTTMaxReconnectInterval),
		CleanSession:         env.Bool("MQTT_CLEAN_SESSION", true),
	}
}

// ConnectMQTTFromEnv connects using the settings read from the environment.
func ConnectMQTTFromEnv() (paho.Client, error) {

	return ConnectMQTTWithContext(context.Background(), MQTTConfigFromEnv())
}

// ConnectMQTTFromEnvWithContext is ConnectFromEnv with the first connection
// attempt bound to ctx.
func ConnectMQTTFromEnvWithContext(ctx context.Context) (paho.Client, error) {

	return ConnectMQTTWithContext(ctx, MQTTConfigFromEnv())
}

// ConnectMQTT validates cfg and establishes a client, waiting up to ConnectTimeout
// for the first connection. It never returns a non-nil client alongside an
// error, and never returns a nil client with a nil error.
//
// Once connected, the client redials on its own if the broker goes away, so
// callers hold on to it for the lifetime of the process.
func ConnectMQTT(cfg MQTTConfig) (paho.Client, error) {

	return ConnectMQTTWithContext(context.Background(), cfg)
}

// ConnectMQTTWithContext is ConnectMQTT with the first connection attempt bound to
// ctx as well as ConnectTimeout.
func ConnectMQTTWithContext(ctx context.Context, cfg MQTTConfig) (paho.Client, error) {

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

	if !waitMQTTToken(ctx, token, cfg.ConnectTimeout) {

		client.Disconnect(0)

		return nil, fmt.Errorf("mqtt: connect %s: timed out after %s", cfg.broker(), cfg.ConnectTimeout)
	}

	if err := token.Error(); err != nil {

		client.Disconnect(0)

		return nil, fmt.Errorf("mqtt: connect %s: %w", cfg.broker(), err)
	}

	return client, nil
}

// PublishMQTT marshals payload as JSON and publishes it to topic at QoS 0. A nil or
// disconnected client is reported as an error rather than panicking or being
// silently dropped.
func PublishMQTT(client paho.Client, topic string, payload any) error {

	return PublishMQTTWithQoSWithContext(context.Background(), client, topic, payload, DefaultMQTTQoS, false)
}

// PublishMQTTWithContext is PublishMQTT bounded by ctx.
func PublishMQTTWithContext(ctx context.Context, client paho.Client, topic string, payload any) error {

	return PublishMQTTWithQoSWithContext(ctx, client, topic, payload, DefaultMQTTQoS, false)
}

// PublishMQTTWithQoS publishes at an explicit quality of service, optionally
// retaining the message on the broker.
func PublishMQTTWithQoS(client paho.Client, topic string, payload any, qos byte, retained bool) error {

	return PublishMQTTWithQoSWithContext(context.Background(), client, topic, payload, qos, retained)
}

// PublishMQTTWithQoSWithContext is PublishMQTTWithQoS bounded by ctx.
func PublishMQTTWithQoSWithContext(ctx context.Context, client paho.Client, topic string, payload any, qos byte, retained bool) error {

	if err := checkMQTTClient(client); err != nil {
		return err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mqtt: encode payload for %s: %w", topic, err)
	}

	return sendMQTT(ctx, client, topic, body, qos, retained)
}

// PublishMQTTRaw publishes an already-encoded body as is, for callers that build
// the message themselves. Passing a string to PublishMQTT would JSON-quote it.
func PublishMQTTRaw(client paho.Client, topic string, body []byte, qos byte, retained bool) error {

	return PublishMQTTRawWithContext(context.Background(), client, topic, body, qos, retained)
}

// PublishMQTTRawWithContext is PublishMQTTRaw bounded by ctx.
func PublishMQTTRawWithContext(ctx context.Context, client paho.Client, topic string, body []byte, qos byte, retained bool) error {

	if err := checkMQTTClient(client); err != nil {
		return err
	}

	return sendMQTT(ctx, client, topic, body, qos, retained)
}

// checkMQTTClient reports a nil or disconnected client.
func checkMQTTClient(client paho.Client) error {

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

	return nil
}

// sendMQTT publishes body and waits for the broker to accept it.
func sendMQTT(ctx context.Context, client paho.Client, topic string, body []byte, qos byte, retained bool) error {

	token := client.Publish(topic, qos, retained, body)

	if !waitMQTTToken(ctx, token, DefaultMQTTPublishTimeout) {
		return fmt.Errorf("mqtt: publish %s: timed out after %s", topic, DefaultMQTTPublishTimeout)
	}

	if err := token.Error(); err != nil {
		return fmt.Errorf("mqtt: publish %s: %w", topic, err)
	}

	return nil
}

// waitMQTTToken reports whether token completed before ctx was cancelled or the
// timeout elapsed. paho's own Wait blocks uninterruptibly, so the token is
// polled through its Done channel instead to keep cancellation working.
func waitMQTTToken(ctx context.Context, token paho.Token, timeout time.Duration) bool {

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
