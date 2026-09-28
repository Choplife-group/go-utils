package rabbitmq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Content types stamped on published messages. The WithContext publishers send
// DefaultContentType; the plain ones send LegacyContentType, which is what
// services' own publishers sent before this package existed.
const (
	DefaultContentType = "application/json"
	LegacyContentType  = "text/plain"
)

// exchangeType is the exchange kind every queue on the platform uses.
const exchangeType = "direct"

// format is how a published message is labelled and stored.
type format struct {
	contentType  string
	deliveryMode uint8
	timestamp    bool
}

var (
	// legacyFormat reproduces the services' original publishers exactly:
	// text/plain, no delivery mode (transient) and no timestamp.
	legacyFormat = format{contentType: LegacyContentType}

	// jsonFormat labels the body as JSON and persists it across a broker restart.
	jsonFormat = format{contentType: DefaultContentType, deliveryMode: amqp.Persistent, timestamp: true}
)

// publishing builds the AMQP message for body.
func (f format) publishing(body []byte, priority uint8) amqp.Publishing {

	message := amqp.Publishing{
		ContentType:  f.contentType,
		Body:         body,
		Priority:     priority,
		DeliveryMode: f.deliveryMode,
	}

	if f.timestamp {
		message.Timestamp = time.Now()
	}

	return message
}

// prefixed applies QUEUE_PREFIX to a queue name and lowercases it, matching how
// services have always derived their queue, exchange and routing key names.
func prefixed(prefix, queue string) string {

	prefix = strings.TrimSpace(prefix)
	if prefix != "" {
		queue = fmt.Sprintf("%s.%s", prefix, queue)
	}

	return strings.ToLower(queue)
}

// Publish encodes payload as JSON and publishes it to the named queue, creating
// the durable direct exchange if it does not exist. The message is sent as
// text/plain and transient, exactly as services' own publishers sent it, so
// adopting this package changes nothing on the wire; PublishWithContext is the
// JSON-labelled, persistent form.
//
// The name is used exactly as given: QUEUE_PREFIX is NOT applied. Publishing is
// usually aimed at another service's queue ("reports-service.deposit.create"),
// and a caller targeting its own queue prefixes the name itself. Consume, which
// only ever binds queues this service owns, does apply the prefix.
//
// If the connection dropped just before the call, Publish waits for the
// supervisor to restore it and tries once more, so a broker restart costs a
// short delay rather than a lost message.
func (c *Conn) Publish(name string, payload any, priority uint8) error {

	return c.publishPayloads(context.Background(), name, []any{payload}, priority, legacyFormat)
}

// PublishWithContext is Publish bounded by ctx, sending the message as
// application/json and persistent.
func (c *Conn) PublishWithContext(ctx context.Context, name string, payload any, priority uint8) error {

	return c.publishPayloads(ctx, name, []any{payload}, priority, jsonFormat)
}

// PublishRaw publishes an already-encoded body in the same format as Publish.
func (c *Conn) PublishRaw(name string, body []byte, priority uint8) error {

	return c.publish(context.Background(), name, [][]byte{body}, priority, legacyFormat)
}

// PublishRawWithContext publishes an already-encoded body in the same format as
// PublishWithContext.
func (c *Conn) PublishRawWithContext(ctx context.Context, name string, body []byte, priority uint8) error {

	return c.publish(ctx, name, [][]byte{body}, priority, jsonFormat)
}

// PublishBatch publishes payloads to the named queue in order, in the same
// format as Publish, on a single channel with the exchange declared once.
//
// Every payload is encoded before anything is sent, so a payload that cannot be
// encoded fails the batch without publishing part of it. If the connection drops
// part-way, PublishBatch waits for the redial and resumes from the message that
// failed rather than resending the ones before it.
func (c *Conn) PublishBatch(name string, payloads []any, priority uint8) error {

	return c.publishPayloads(context.Background(), name, payloads, priority, legacyFormat)
}

// PublishBatchWithContext is PublishBatch bounded by ctx, sending each message
// as application/json and persistent.
func (c *Conn) PublishBatchWithContext(ctx context.Context, name string, payloads []any, priority uint8) error {

	return c.publishPayloads(ctx, name, payloads, priority, jsonFormat)
}

// publishPayloads encodes every payload as JSON and publishes the lot.
func (c *Conn) publishPayloads(ctx context.Context, name string, payloads []any, priority uint8, f format) error {

	bodies := make([][]byte, len(payloads))

	for i, payload := range payloads {

		body, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("rabbitmq: encode payload %d for %s: %w", i, name, err)
		}

		bodies[i] = body
	}

	return c.publish(ctx, name, bodies, priority, f)
}

// publish sends bodies in order, retrying once from the first unsent message if
// the connection was lost.
func (c *Conn) publish(ctx context.Context, name string, bodies [][]byte, priority uint8, f format) error {

	if c == nil {
		return ErrNotConnected
	}

	if name == "" {
		return fmt.Errorf("rabbitmq: publish: queue name is empty")
	}

	if len(bodies) == 0 {
		return nil
	}

	var lastErr error

	for attempt := 0; attempt < 2; attempt++ {

		if attempt > 0 {

			c.dropIfDead()

			waitCtx, cancel := context.WithTimeout(ctx, c.cfg.ReconnectMax)
			err := c.WaitReadyWithContext(waitCtx)
			cancel()

			if err != nil {
				return lastErr
			}
		}

		sent, err := c.publishOnce(ctx, name, bodies, priority, f)
		if err == nil {
			return nil
		}

		bodies = bodies[sent:]
		lastErr = err

		if !isRetryable(err) {
			return err
		}
	}

	return lastErr
}

// publishOnce sends bodies on one channel and reports how many were handed to
// the broker before any failure.
func (c *Conn) publishOnce(ctx context.Context, name string, bodies [][]byte, priority uint8, f format) (int, error) {

	channel, err := c.Channel()
	if err != nil {
		return 0, err
	}

	defer channel.Close()

	target := strings.ToLower(name)

	if err := channel.ExchangeDeclare(target, exchangeType, true, false, false, false, nil); err != nil {
		return 0, fmt.Errorf("rabbitmq: declare exchange %s: %w", target, err)
	}

	for i, body := range bodies {

		publishCtx, cancel := context.WithTimeout(ctx, DefaultPublishTimeout)
		err := channel.PublishWithContext(publishCtx, target, target, false, false, f.publishing(body, priority))
		cancel()

		if err != nil {
			return i, fmt.Errorf("rabbitmq: publish to %s: %w", target, err)
		}
	}

	return len(bodies), nil
}

// isRetryable reports whether an error is the kind a reconnection would fix.
func isRetryable(err error) bool {

	if err == nil {
		return false
	}

	if errors.Is(err, ErrNotConnected) || errors.Is(err, amqp.ErrClosed) {
		return true
	}

	if errors.Is(err, ErrClosed) {
		return false
	}

	var amqpErr *amqp.Error
	if errors.As(err, &amqpErr) && amqpErr != nil {
		return amqpErr.Code == amqp.ChannelError || amqpErr.Code == amqp.ConnectionForced
	}

	return false
}
