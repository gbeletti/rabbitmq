package rabbitmq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Consume starts consuming messages from a queue until the context is canceled. It returns nil when the
// context is canceled or when the channel is closed (e.g. the connection dropped); in the latter case
// KeepConnectionAndSetup reconnects and runs the setup again, which is where consumers are restarted.
// With ExecuteConcurrent it returns ErrUnboundedConcurrency when nothing bounds the handlers running; see
// ConfigConsume.
func (r *rabbit) Consume(ctx context.Context, config ConfigConsume, f func(*amqp.Delivery)) (err error) {
	st, release, err := r.acquire()
	if err != nil {
		return
	}
	defer release()
	limit, err := config.concurrencyLimit(st.prefetchCount)
	if err != nil {
		return
	}
	if config.Consumer == "" {
		// The tag amqp would generate is not returned, and Cancel needs it to stop this consumer.
		config.Consumer = uniqueConsumerTag()
	}
	var msgs <-chan amqp.Delivery
	msgs, err = st.chConsumer.Consume(
		config.QueueName,
		config.Consumer,
		config.AutoAck,
		config.Exclusive,
		config.NoLocal,
		config.NoWait,
		config.Args,
	)
	if err != nil {
		return
	}
	// Holding a slot per running handler caps them even when f acks early, which frees the prefetch window.
	slots := make(chan struct{}, limit)
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			if !config.ExecuteConcurrent {
				done := r.trackHandler()
				f(&msg)
				done()
				continue
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				handBack(&msg, config.AutoAck, f)
				return cancelConsume(st, config, msgs, f)
			}
			done := r.trackHandler()
			go func() {
				defer func() {
					<-slots
					done()
				}()
				f(&msg)
			}()
		case <-ctx.Done():
			return cancelConsume(st, config, msgs, f)
		}
	}
}

// cancelConsume stops the consumer and hands back the deliveries amqp buffered for it.
func cancelConsume(st *state, config ConfigConsume, msgs <-chan amqp.Delivery, f func(*amqp.Delivery)) error {
	err := st.chConsumer.Cancel(config.Consumer, false)
	if err != nil {
		if errors.Is(err, amqp.ErrClosed) {
			return nil // the channel is gone, and so are the deliveries it buffered
		}
		return err
	}
	drainCanceled(msgs, config.AutoAck, f)
	return nil
}

// drainCanceled empties the deliveries amqp buffered before the cancel; it closes msgs once they are handed
// out. They are requeued right away instead of staying unacked until the channel closes. With AutoAck the
// broker already considers them delivered, so they still go to f or they would be lost.
func drainCanceled(msgs <-chan amqp.Delivery, autoAck bool, f func(*amqp.Delivery)) {
	for msg := range msgs {
		handBack(&msg, autoAck, f)
	}
}

// handBack returns a delivery no handler took: requeued, or with AutoAck given to f since it can't go back.
// On a closed channel the broker already requeued it, so the failed Nack is not logged.
func handBack(msg *amqp.Delivery, autoAck bool, f func(*amqp.Delivery)) {
	if autoAck {
		f(msg)
		return
	}
	if err := msg.Nack(false, true); err != nil && !errors.Is(err, amqp.ErrClosed) {
		log.Printf("error requeueing delivery after cancel: [%s]\n", err)
	}
}

func uniqueConsumerTag() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand does not fail on the supported platforms (and never does since Go 1.24)
	return "ctag-" + hex.EncodeToString(b)
}
