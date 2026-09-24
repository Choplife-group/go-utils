package rabbitmq

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/sirupsen/logrus"

	"github.com/choplife-group/go-utils/connection/internal/backoff"
)

// Default consumer settings.
const (
	DefaultPrefetchCount = 1
	DefaultMaxPriority   = 5
)

// Handler processes one delivery. Returning nil acknowledges the message;
// returning an error rejects it according to the consumer's Requeue setting.
//
// A panic inside a Handler is recovered and treated as an error, so one
// malformed message cannot take the service down.
type Handler func(ctx context.Context, delivery amqp.Delivery) error

// ConsumerConfig describes a queue to consume. Only Queue is required.
type ConsumerConfig struct {
	// Queue is the unprefixed queue name; QUEUE_PREFIX is applied to it.
	Queue string

	// PrefetchCount bounds how many unacknowledged messages the broker will
	// hand this consumer at once.
	PrefetchCount int

	// MaxPriority declares the queue's priority range.
	MaxPriority int

	// Requeue returns a failed message to the queue instead of dropping it.
	// Leave it false unless the queue has a dead-letter policy, since a message
	// that always fails would otherwise loop forever.
	Requeue bool
}

// applyDefaults fills in fields left at their zero value.
func (cfg *ConsumerConfig) applyDefaults() {

	if cfg.PrefetchCount <= 0 {
		cfg.PrefetchCount = DefaultPrefetchCount
	}

	if cfg.MaxPriority <= 0 {
		cfg.MaxPriority = DefaultMaxPriority
	}
}

// Consume declares the queue's topology and delivers messages to handler until
// ctx is cancelled or the Conn is closed.
//
// It runs as a loop rather than recursing on reconnect: when the broker drops
// the connection, it waits for the supervisor to redial, re-declares the
// exchange, queue and binding, and resumes consuming. Re-declaring every time is
// what makes it survive a broker that came back with no topology.
//
// Consume blocks, so callers run it in a goroutine — one per queue.
func (c *Conn) Consume(ctx context.Context, cfg ConsumerConfig, handler Handler) error {

	if c == nil {
		return ErrNotConnected
	}

	if cfg.Queue == "" {
		return fmt.Errorf("rabbitmq: consume: queue name is empty")
	}

	if handler == nil {
		return fmt.Errorf("rabbitmq: consume: handler is nil")
	}

	cfg.applyDefaults()

	queue := c.name(cfg.Queue)
	schedule := backoff.Backoff{Min: c.cfg.ReconnectMin, Max: c.cfg.ReconnectMax}

	for {

		if err := ctx.Err(); err != nil {
			return nil
		}

		if c.isClosed() {
			return nil
		}

		err := c.consumeOnce(ctx, cfg, queue, handler)

		switch {
		case err == nil:
			// The delivery channel closed cleanly, so the connection is healthy
			// and the next attempt starts from the shortest delay again.
			schedule.Reset()

		case errors.Is(err, context.Canceled), errors.Is(err, ErrClosed):
			return nil

		default:
			logrus.WithContext(ctx).
				WithFields(logrus.Fields{
					"description": "Error consuming rabbitmq queue",
					"data":        queue,
				}).
				Error(err.Error())
		}

		if err := c.WaitReady(ctx); err != nil {
			return nil
		}

		if !schedule.Wait(ctx) {
			return nil
		}
	}
}

// consumeOnce establishes the topology and drains deliveries until the channel
// closes or ctx is cancelled.
func (c *Conn) consumeOnce(ctx context.Context, cfg ConsumerConfig, queue string, handler Handler) error {

	channel, err := c.Channel()
	if err != nil {
		return err
	}

	defer channel.Close()

	if err := channel.ExchangeDeclare(queue, exchangeType, true, false, false, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: declare exchange %s: %w", queue, err)
	}

	args := amqp.Table{"x-max-priority": int32(cfg.MaxPriority)}

	if _, err := channel.QueueDeclare(queue, true, false, false, false, args); err != nil {
		return fmt.Errorf("rabbitmq: declare queue %s: %w", queue, err)
	}

	if err := channel.QueueBind(queue, queue, queue, false, nil); err != nil {
		return fmt.Errorf("rabbitmq: bind queue %s: %w", queue, err)
	}

	if err := channel.Qos(cfg.PrefetchCount, 0, false); err != nil {
		return fmt.Errorf("rabbitmq: set qos on %s: %w", queue, err)
	}

	deliveries, err := channel.Consume(queue, queue, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("rabbitmq: consume %s: %w", queue, err)
	}

	// Buffered so amqp can report the channel closing without blocking.
	notify := channel.NotifyClose(make(chan *amqp.Error, 1))

	for {

		select {
		case <-ctx.Done():
			return context.Canceled

		case reason := <-notify:

			if reason == nil {
				return nil
			}

			return fmt.Errorf("rabbitmq: channel %s closed: %w", queue, reason)

		case delivery, ok := <-deliveries:

			if !ok {
				return nil
			}

			c.handleDelivery(ctx, cfg, queue, delivery, handler)
		}
	}
}

// handleDelivery runs the handler for one message and settles the delivery.
// A handler that panics is recovered and the message is rejected, rather than
// the panic unwinding into the consumer goroutine and killing the process.
func (c *Conn) handleDelivery(ctx context.Context, cfg ConsumerConfig, queue string, delivery amqp.Delivery, handler Handler) {

	err := invoke(ctx, delivery, handler)

	if err != nil {

		logrus.WithContext(ctx).
			WithFields(logrus.Fields{
				"description": "Error handling rabbitmq delivery",
				"data":        queue,
			}).
			Error(err.Error())

		if nackErr := delivery.Nack(false, cfg.Requeue); nackErr != nil {

			logrus.WithContext(ctx).
				WithFields(logrus.Fields{
					"description": "Error rejecting rabbitmq delivery",
					"data":        queue,
				}).
				Error(nackErr.Error())
		}

		return
	}

	if ackErr := delivery.Ack(false); ackErr != nil {

		logrus.WithContext(ctx).
			WithFields(logrus.Fields{
				"description": "Error acknowledging rabbitmq delivery",
				"data":        queue,
			}).
			Error(ackErr.Error())
	}
}

// invoke calls handler, converting a panic into an error.
func invoke(ctx context.Context, delivery amqp.Delivery, handler Handler) (err error) {

	defer func() {

		if reason := recover(); reason != nil {
			err = fmt.Errorf("rabbitmq: handler panicked: %v", reason)
		}
	}()

	return handler(ctx, delivery)
}
