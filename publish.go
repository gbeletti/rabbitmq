package rabbitmq

import (
	"context"
	"errors"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrNotConfirmed is returned by Publish, with ConfigConnection.PublisherConfirms on, when the message was
// sent but the broker did not confirm it. What the caller can rely on:
//   - an error that does not match ErrNotConfirmed (e.g. amqp.ErrClosed): the message was not sent, so a
//     retry can't duplicate it;
//   - ErrNotConfirmed: the outcome is unknown. The broker nacked it, the producer channel closed before the
//     confirm, or ctx ended while waiting; the message may still have been enqueued, so a retry can
//     duplicate it and consumers must be idempotent.
//
// When the channel closed, the chain carries the broker's *amqp.Error, but that is why the CHANNEL closed:
// it may have been caused by another Publish running concurrently (e.g. 404 for its missing exchange), so
// don't treat it as a permanent failure of this message.
var ErrNotConfirmed = errors.New("rabbitmq: publish not confirmed by the broker")

// ErrUnroutable is returned by Publish, with ConfigConnection.PublisherConfirms on and ConfigPublish.Mandatory
// set, when the broker returned the message because no queue is bound to receive it (e.g. an existing
// exchange without a binding that matches the routing key). The message was not enqueued anywhere, so a
// retry can't duplicate it; the chain carries the broker's reply code and text, e.g. 312 NO_ROUTE.
var ErrUnroutable = errors.New("rabbitmq: message returned by the broker as unroutable")

// Publish publishes body to exchange with routing key. With ConfigConnection.PublisherConfirms on, it also
// waits for the broker to confirm the message and returns ErrNotConfirmed when it doesn't, and with
// ConfigPublish.Mandatory also returns ErrUnroutable when the broker returned it. ctx bounds that wait, not
// the write to the socket: when the broker stops reading (memory or disk alarm) and the socket buffer is
// full, the write blocks past ctx, as it does without confirms.
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
	if !config.Mandatory {
		confirm, err := st.chProducer.PublishWithDeferredConfirmWithContext(ctx, config.Exchange, config.RoutingKey, false, config.Immediate, msg)
		if err != nil {
			return err
		}
		return st.waitConfirm(ctx, confirm)
	}
	return st.publishMandatory(ctx, config, msg)
}

// publishMandatory publishes with confirms and Mandatory, and tells a returned message apart from a
// confirmed one.
func (st *state) publishMandatory(ctx context.Context, config ConfigPublish, msg amqp.Publishing) error {
	rt := st.producerReturns
	p := rt.newPending(config.Exchange, config.RoutingKey, msg)
	rt.sendMu.Lock()
	rt.add(p)
	confirm, err := st.chProducer.PublishWithDeferredConfirmWithContext(ctx, config.Exchange, config.RoutingKey, true, config.Immediate, msg)
	rt.sendMu.Unlock()
	if err != nil || confirm == nil { // confirm is nil only out of confirm mode, which openProducer rules out
		rt.settle(p)
		return err
	}
	err = st.waitConfirm(ctx, confirm)
	select {
	case <-confirm.Done():
	default:
		// ctx ended first. p stays pending until the confirm comes, or a return still on its way could be
		// matched to a later equal publish. The confirm comes at the latest when the channel closes.
		go func() {
			<-confirm.Done()
			rt.settle(p)
		}()
		return err
	}
	ret := rt.settle(p)
	if err != nil || ret == nil {
		return err
	}
	return fmt.Errorf("%w: %d %s", ErrUnroutable, ret.ReplyCode, ret.ReplyText)
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
	return fmt.Errorf("%w: producer channel closed (possibly by another publish) before the confirm: %w", ErrNotConfirmed, reason)
}
