//go:build integration

package mqtt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

// Run with a live broker:
//
//	docker run -d --name emqx-it -p 1884:1883 emqx/emqx:latest
//	MQTT_HOST=127.0.0.1 MQTT_PORT=1884 go test -tags integration ./connection/mqtt/ -v

func TestConnectAndPublishAgainstLiveBroker(t *testing.T) {

	cfg := ConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set MQTT_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ConnectWithContext(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect() = %v", err)
	}

	defer client.Disconnect(250)

	if !client.IsConnectionOpen() {
		t.Fatal("IsConnectionOpen() = false after Connect")
	}

	topic := fmt.Sprintf("go-utils-it/%d", time.Now().UnixNano())
	received := make(chan string, 1)

	token := client.Subscribe(topic, 0, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})

	if !token.WaitTimeout(10 * time.Second) {
		t.Fatal("Subscribe timed out")
	}

	if err := token.Error(); err != nil {
		t.Fatalf("Subscribe() = %v", err)
	}

	if err := PublishWithContext(ctx, client, topic, map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("Publish() = %v", err)
	}

	select {
	case body := <-received:

		if body != `{"hello":"world"}` {
			t.Fatalf("received %s", body)
		}

	case <-time.After(10 * time.Second):
		t.Fatal("no message received")
	}
}

// TestReconnectAfterBrokerRestart proves the client comes back on its own when
// the broker restarts, which is the whole reason Connect turns on paho's
// auto-reconnect. It needs a container it is allowed to restart, named by
// MQTT_CONTAINER.
//
// Subscriptions are re-established from the OnConnect callback rather than
// assumed to survive: with CleanSession set the broker discards the session on
// disconnect, so a subscriber that does not re-subscribe goes quietly deaf.
func TestReconnectAfterBrokerRestart(t *testing.T) {

	container := os.Getenv("MQTT_CONTAINER")
	if container == "" {
		t.Skip("set MQTT_CONTAINER to the broker container name to run this")
	}

	cfg := ConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set MQTT_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	topic := fmt.Sprintf("go-utils-it/restart/%d", time.Now().UnixNano())
	received := make(chan string, 8)
	connects := make(chan struct{}, 8)

	cfg.OnConnect = func() {
		select {
		case connects <- struct{}{}:
		default:
		}
	}

	client, err := ConnectWithContext(ctx, cfg)
	if err != nil {
		t.Fatalf("Connect() = %v", err)
	}

	defer client.Disconnect(250)

	subscribe := func() {

		token := client.Subscribe(topic, 0, func(_ paho.Client, message paho.Message) {
			received <- string(message.Payload())
		})

		if !token.WaitTimeout(15 * time.Second) {
			t.Error("Subscribe timed out")

			return
		}

		if err := token.Error(); err != nil {
			t.Errorf("Subscribe() = %v", err)
		}
	}

	subscribe()

	if err := PublishWithContext(ctx, client, topic, map[string]string{"phase": "before"}); err != nil {
		t.Fatalf("Publish() before restart = %v", err)
	}

	select {
	case <-received:
	case <-time.After(15 * time.Second):
		t.Fatal("no message before the restart")
	}

	// Drain the connect signal from the initial connection.
	select {
	case <-connects:
	default:
	}

	if out, err := exec.Command("docker", "restart", container).CombinedOutput(); err != nil {
		t.Fatalf("docker restart %s: %v: %s", container, err, out)
	}

	if !waitFor(func() bool { return !client.IsConnectionOpen() }, 60*time.Second) {
		t.Fatal("client never registered as disconnected")
	}

	if !waitFor(client.IsConnectionOpen, 120*time.Second) {
		t.Fatal("client never reconnected")
	}

	// Wait for the OnConnect callback so the re-subscribe happens on the new
	// session rather than the dead one.
	select {
	case <-connects:
	case <-time.After(30 * time.Second):
		t.Fatal("OnConnect never fired after the reconnect")
	}

	subscribe()

	deadline := time.Now().Add(60 * time.Second)

	for time.Now().Before(deadline) {

		if err := PublishWithContext(ctx, client, topic, map[string]string{"phase": "after"}); err == nil {
			break
		}

		time.Sleep(2 * time.Second)
	}

	select {
	case <-received:
	case <-time.After(30 * time.Second):
		t.Fatal("no message after the restart")
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

// TestPlainVariantsAgainstLiveBroker drives the no-ctx forms end to end, and
// checks PublishRaw delivers a pre-encoded body untouched — the way services
// send websocket messages they have already marshalled.
func TestPlainVariantsAgainstLiveBroker(t *testing.T) {

	cfg := ConfigFromEnv()

	if cfg.Host == "" {
		t.Skip("set MQTT_HOST to run this")
	}

	client, err := Connect(cfg)
	if err != nil {
		t.Fatalf("Connect() = %v", err)
	}

	defer client.Disconnect(250)

	topic := fmt.Sprintf("go-utils-it/%d", time.Now().UnixNano())
	received := make(chan string, 2)

	token := client.Subscribe(topic, 1, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})

	if !token.WaitTimeout(10 * time.Second) {
		t.Fatal("Subscribe timed out")
	}

	if err := token.Error(); err != nil {
		t.Fatalf("Subscribe() = %v", err)
	}

	if err := PublishRaw(client, topic, []byte(`{"event":"deposit","status":-1}`), 1, false); err != nil {
		t.Fatalf("PublishRaw() = %v", err)
	}

	if err := Publish(client, topic, map[string]string{"hello": "world"}); err != nil {
		t.Fatalf("Publish() = %v", err)
	}

	// Connect turns off paho's ordering, so the two may arrive either way round.
	want := map[string]bool{`{"event":"deposit","status":-1}`: true, `{"hello":"world"}`: true}

	for range 2 {

		select {
		case body := <-received:

			if !want[body] {
				t.Fatalf("received unexpected %s", body)
			}

			delete(want, body)

		case <-time.After(10 * time.Second):
			t.Fatalf("messages not received: %v", want)
		}
	}
}

// TestFromEnvConstructorsAndPublishForms connects with both environment-driven
// constructors and sends through every publish form not covered above.
func TestFromEnvConstructorsAndPublishForms(t *testing.T) {

	if ConfigFromEnv().Host == "" {
		t.Skip("set MQTT_HOST to run this")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ConnectFromEnv()
	if err != nil {
		t.Fatalf("ConnectFromEnv() = %v", err)
	}

	defer client.Disconnect(250)

	publisher, err := ConnectFromEnvWithContext(ctx)
	if err != nil {
		t.Fatalf("ConnectFromEnvWithContext() = %v", err)
	}

	defer publisher.Disconnect(250)

	topic := fmt.Sprintf("go-utils-it/%d", time.Now().UnixNano())
	received := make(chan string, 3)

	token := client.Subscribe(topic, 1, func(_ paho.Client, message paho.Message) {
		received <- string(message.Payload())
	})

	if !token.WaitTimeout(10*time.Second) || token.Error() != nil {
		t.Fatalf("Subscribe() = %v", token.Error())
	}

	if err := PublishWithQoS(publisher, topic, map[string]string{"form": "qos"}, 1, false); err != nil {
		t.Fatalf("PublishWithQoS() = %v", err)
	}

	if err := PublishWithQoSWithContext(ctx, publisher, topic, map[string]string{"form": "qos-ctx"}, 1, false); err != nil {
		t.Fatalf("PublishWithQoSWithContext() = %v", err)
	}

	if err := PublishRawWithContext(ctx, publisher, topic, []byte(`{"form":"raw-ctx"}`), 1, false); err != nil {
		t.Fatalf("PublishRawWithContext() = %v", err)
	}

	want := map[string]bool{`{"form":"qos"}`: true, `{"form":"qos-ctx"}`: true, `{"form":"raw-ctx"}`: true}

	for range 3 {

		select {
		case body := <-received:

			if !want[body] {
				t.Fatalf("received unexpected %s", body)
			}

			delete(want, body)

		case <-time.After(10 * time.Second):
			t.Fatalf("messages not received: %v", want)
		}
	}
}
