package rabbitmq

import (
	"context"
	"errors"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Consume starts consuming messages from a queue until the context is canceled. It returns nil when the
// context is canceled or when the channel is closed (e.g. the connection dropped); in the latter case
// KeepConnectionAndSetup reconnects and runs the setup again, which is where consumers are restarted.
func (r *rabbit) Consume(ctx context.Context, config ConfigConsume, f func(*amqp.Delivery)) (err error) {
	st, release, err := r.acquire()
	if err != nil {
		return
	}
	defer release()
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
			r.wg.Add(1)
			if config.ExecuteConcurrent {
				go func() {
					defer r.wg.Done()
					f(&msg)
				}()
			} else {
				f(&msg)
				r.wg.Done()
			}
		case <-ctx.Done():
			// Deliveries already prefetched but not handed to f stay unacked and the broker requeues them
			// when the channel closes; handing them to f now would run them with a canceled context.
			err = st.chConsumer.Cancel(config.Consumer, false)
			if errors.Is(err, amqp.ErrClosed) {
				err = nil
			}
			return
		}
	}
}
