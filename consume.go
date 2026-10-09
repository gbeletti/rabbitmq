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
// With ExecuteConcurrent it returns ErrUnboundedConcurrency unless the prefetch bounds the handlers running.
func (r *rabbit) Consume(ctx context.Context, config ConfigConsume, f func(*amqp.Delivery)) (err error) {
	st, release, err := r.acquire()
	if err != nil {
		return
	}
	defer release()
	if config.ExecuteConcurrent && (config.AutoAck || st.prefetchCount <= 0) {
		return ErrUnboundedConcurrency
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
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			done := r.trackHandler()
			if config.ExecuteConcurrent {
				go func() {
					defer done()
					f(&msg)
				}()
			} else {
				f(&msg)
				done()
			}
		case <-ctx.Done():
			err = st.chConsumer.Cancel(config.Consumer, false)
			if err != nil {
				if errors.Is(err, amqp.ErrClosed) {
					return nil // the channel is gone, and so are the deliveries it buffered
				}
				return
			}
			drainCanceled(msgs, config.AutoAck, f)
			return nil
		}
	}
}

// drainCanceled empties the deliveries amqp buffered before the cancel; it closes msgs once they are handed
// out. They are requeued right away instead of staying unacked until the channel closes. With AutoAck the
// broker already considers them delivered, so they still go to f or they would be lost.
func drainCanceled(msgs <-chan amqp.Delivery, autoAck bool, f func(*amqp.Delivery)) {
	for msg := range msgs {
		if autoAck {
			f(&msg)
			continue
		}
		if err := msg.Nack(false, true); err != nil {
			log.Printf("error requeueing delivery after cancel: [%s]\n", err)
		}
	}
}

func uniqueConsumerTag() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand does not fail on the supported platforms (and never does since Go 1.24)
	return "ctag-" + hex.EncodeToString(b)
}
