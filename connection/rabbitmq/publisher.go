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

// DefaultContentType labels published messages, which the platform encodes as
// JSON.
const DefaultContentType = "application/json"

// exchangeType is the exchange kind every queue on the platform uses.
const exchangeType = "direct"

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
// the durable direct exchange if it does not exist.
//
// The name is used exactly as given: QUEUE_PREFIX is NOT applied. Publishing is
// usually aimed at another service's queue ("reports-service.deposit.create"),
// and a caller targeting its own queue prefixes the name itself. Consume, which
// only ever binds queues this service owns, does apply the prefix.
//
// If the connection dropped just before the call, Publish waits for the
// supervisor to restore it and tries once more, so a broker restart costs a
// short delay rather than a lost message.
func (c *Conn) Publish(ctx context.Context, name string, payload any, priority uint8) error {

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("rabbitmq: encode payload for %s: %w", name, err)
	}

	return c.PublishRaw(ctx, name, body, priority)
}

// PublishRaw publishes an already-encoded body.
func (c *Conn) PublishRaw(ctx context.Context, name string, body []byte, priority uint8) error {

	if c == nil {
		return ErrNotConnected
	}

	if name == "" {
		return fmt.Errorf("rabbitmq: publish: queue name is empty")
	}

	var lastErr error

	for attempt := 0; attempt < 2; attempt++ {

		if attempt > 0 {

			waitCtx, cancel := context.WithTimeout(ctx, c.cfg.ReconnectMax)
			err := c.WaitReady(waitCtx)
			cancel()

			if err != nil {
				return lastErr
			}
		}

		err := c.publishOnce(ctx, name, body, priority)
		if err == nil {
			return nil
		}

		lastErr = err

		if !isRetryable(err) {
			return err
		}
	}

	return lastErr
}

// publishOnce performs a single publish attempt on its own channel.
func (c *Conn) publishOnce(ctx context.Context, name string, body []byte, priority uint8) error {

	channel, err := c.Channel()
	if err != nil {
		return err
	}

	defer channel.Close()

	target := strings.ToLower(name)

	if err := channel.ExchangeDeclare(target, exchangeType, true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare exchange %s: %w", target, err)
	}

	publishCtx, cancel := context.WithTimeout(ctx, DefaultPublishTimeout)
	defer cancel()

	err = channel.PublishWithContext(publishCtx, target, target, false, false, amqp.Publishing{
		ContentType:  DefaultContentType,
		Body:         body,
		Priority:     priority,
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now(),
	})
	if err != nil {
		return fmt.Errorf("rabbitmq: publish to %s: %w", target, err)
	}

	return nil
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
