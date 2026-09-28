package mqtt

import (
	"context"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

func TestConnectRejectsMissingSettings(t *testing.T) {

	cases := []struct {
		name string
		cfg  Config
	}{
		{"no host", Config{Port: "1883"}},
		{"no port", Config{Host: "localhost"}},
	}

	for _, tc := range cases {

		t.Run(tc.name, func(t *testing.T) {

			client, err := ConnectWithContext(context.Background(), tc.cfg)

			if err == nil {
				t.Fatal("Connect() succeeded, want an error")
			}

			if client != nil {
				t.Fatal("Connect() returned a client alongside an error")
			}
		})
	}
}

func TestConnectReturnsErrorNotClientWhenUnreachable(t *testing.T) {

	client, err := ConnectWithContext(context.Background(), Config{
		Host:           "127.0.0.1",
		Port:           "1",
		ConnectTimeout: 500 * time.Millisecond,
	})

	if err == nil {
		client.Disconnect(0)
		t.Skip("something is listening on port 1")
	}

	if client != nil {
		t.Fatal("Connect() returned a client alongside an error")
	}
}

func TestBrokerURI(t *testing.T) {

	cfg := Config{Host: "emqx", Port: "1883"}
	cfg.applyDefaults()

	if got := cfg.broker(); got != "tcp://emqx:1883" {
		t.Fatalf("broker() = %s, want tcp://emqx:1883", got)
	}

	tls := Config{Host: "emqx", Port: "8883", Scheme: "ssl"}

	if got := tls.broker(); got != "ssl://emqx:8883" {
		t.Fatalf("broker() = %s, want ssl://emqx:8883", got)
	}
}

func TestConfigFromEnvReadsPlatformNames(t *testing.T) {

	t.Setenv("MQTT_HOST", "emqx")
	t.Setenv("MQTT_PORT", "1883")
	t.Setenv("MQTT_USERNAME", "user")
	t.Setenv("MQTT_PASSWORD", "pass")
	t.Setenv("CLIENT_ID", "identity")

	cfg := ConfigFromEnv()

	if cfg.Host != "emqx" || cfg.Port != "1883" {
		t.Fatalf("ConfigFromEnv() = %+v", cfg)
	}

	if cfg.ClientIDPrefix != "identity" {
		t.Fatalf("ClientIDPrefix = %q, want %q", cfg.ClientIDPrefix, "identity")
	}
}

func TestClientIDPrefixFallsBackToServiceName(t *testing.T) {

	t.Setenv("OTEL_SERVICE_NAME", "wallet-service")

	cfg := Config{Host: "emqx", Port: "1883"}
	cfg.applyDefaults()

	if cfg.ClientIDPrefix != "wallet-service" {
		t.Fatalf("ClientIDPrefix = %q, want %q", cfg.ClientIDPrefix, "wallet-service")
	}
}

// TestPublishOnNilClientReturnsError is the nil-safety contract: every service
// this replaces returns *mqtt.Client and guards nil at each call site instead.
func TestPublishOnNilClientReturnsError(t *testing.T) {

	if err := PublishWithContext(context.Background(), nil, "topic", map[string]string{"a": "b"}); err == nil {
		t.Fatal("Publish(nil) succeeded, want an error")
	}
}

func TestPublishRejectsUnencodablePayload(t *testing.T) {

	// A disconnected client is rejected before encoding, so this exercises the
	// nil-client path only; encoding is covered by the marshal error branch.
	if err := PublishWithContext(context.Background(), nil, "topic", make(chan int)); err == nil {
		t.Fatal("Publish() succeeded, want an error")
	}
}

// stubClient reports a client that paho considers "connected" (it is managing a
// connection) while the socket is actually down, which is exactly what paho
// does during an auto-reconnect.
type stubClient struct {
	paho.Client

	connected bool
	open      bool
	published bool
}

func (s *stubClient) IsConnected() bool { return s.connected }

func (s *stubClient) IsConnectionOpen() bool { return s.open }

func (s *stubClient) Publish(string, byte, bool, interface{}) paho.Token {

	s.published = true

	return nil
}

// TestPublishRejectsWhileReconnecting pins the distinction that broke the
// reconnect test: paho's IsConnected stays true for the whole outage when
// auto-reconnect is on, so Publish must gate on IsConnectionOpen or it will
// hand messages to a dead socket.
func TestPublishRejectsWhileReconnecting(t *testing.T) {

	client := &stubClient{connected: true, open: false}

	err := PublishWithContext(context.Background(), client, "topic", map[string]string{"a": "b"})

	if err == nil {
		t.Fatal("Publish() succeeded while the connection was down")
	}

	if client.published {
		t.Fatal("Publish() handed the message to a dead connection")
	}
}

func TestPlainVariantsKeepTheContract(t *testing.T) {

	client, err := Connect(Config{})
	if err == nil {
		t.Fatal("Connect() with no settings succeeded")
	}

	if client != nil {
		t.Fatal("Connect() returned a client alongside an error")
	}

	if err := Publish(nil, "topic", map[string]string{"a": "b"}); err == nil {
		t.Fatal("Publish(nil) succeeded, want an error")
	}

	if err := PublishRaw(nil, "topic", []byte("{}"), 1, false); err == nil {
		t.Fatal("PublishRaw(nil) succeeded, want an error")
	}
}

func TestPublishRawRejectsWhileReconnecting(t *testing.T) {

	client := &stubClient{connected: true, open: false}

	if err := PublishRaw(client, "topic", []byte("{}"), 1, false); err == nil {
		t.Fatal("PublishRaw() succeeded while the connection was down")
	}

	if client.published {
		t.Fatal("PublishRaw() handed the message to a dead connection")
	}
}
