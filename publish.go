package rabbitmq

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrNotConfirmed is returned by Publish, with ConfigConnection.PublisherConfirms on, when the broker did not
// confirm the message. The chain also carries the cause: the broker's *amqp.Error when it closed the producer
// channel first (e.g. 404 NOT_FOUND for a missing exchange), or ctx.Err() when ctx ended while waiting. In
// that last case, and when the connection dropped, the message may still have been enqueued.
var ErrNotConfirmed = errors.New("rabbitmq: publish not confirmed by the broker")

// Publish publishes body to exchange with routing key. With ConfigConnection.PublisherConfirms on, it also
// waits for the broker to confirm the message, honoring ctx, and returns ErrNotConfirmed when it doesn't.
func (r *rabbit) Publish(ctx context.Context, body []byte, config ConfigPublish) (err error) {
	st, release, err := r.acquire()
	if err != nil {
		return
	}
	defer release()
	msg := amqp.Publishing{
		Headers:         config.Headers,
		ContentType:     config.ContentType,
		ContentEncoding: config.ContentEncoding,
		Priority:        config.Priority,
		CorrelationId:   config.CorrelationID,
		MessageId:       config.MessageID,
		Body:            body,
		DeliveryMode:    config.DeliveryMode,
		ReplyTo:         config.ReplyTo,
		Expiration:      config.Expiration,
		Timestamp:       config.Timestamp,
		Type:            config.Type,
		UserId:          config.UserId,
		AppId:           config.AppId,
	}
	if !st.confirms {
		return st.chProducer.PublishWithContext(ctx, config.Exchange, config.RoutingKey, config.Mandatory, config.Immediate, msg)
	}
	confirm, err := st.chProducer.PublishWithDeferredConfirmWithContext(ctx, config.Exchange, config.RoutingKey, config.Mandatory, config.Immediate, msg)
	if err != nil {
		return err
	}
	return st.waitConfirm(ctx, confirm)
}

func (st *state) waitConfirm(ctx context.Context, confirm *amqp.DeferredConfirmation) error {
	if confirm == nil { // only when the channel is not in confirm mode, which openProducer rules out
		return nil
	}
	acked, err := confirm.WaitContext(ctx)
	switch {
	case err != nil:
		return fmt.Errorf("%w: %w", ErrNotConfirmed, err)
	case acked:
		return nil
	case !st.chProducer.IsClosed():
		return fmt.Errorf("%w: the broker nacked it", ErrNotConfirmed)
	}
	// The channel closing nacks the pending confirms, after it handed the reason to the NotifyClose listeners.
	var reason error = amqp.ErrClosed
	select {
	case <-st.producerReason.done:
		if st.producerReason.err != nil {
			reason = st.producerReason.err
		}
	case <-ctx.Done():
	}
	return fmt.Errorf("%w: producer channel closed before the confirm: %w", ErrNotConfirmed, reason)
}
