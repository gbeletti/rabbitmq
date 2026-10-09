package rabbitmq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Consume starts consuming messages from a queue until the context is canceled, on a channel of its own with
// the connection's PrefetchCount. It returns nil when the context is canceled, when the connection is lost
// (KeepConnectionAndSetup then reconnects and runs the setup again, which is where consumers are restarted)
// and when the broker cancels the consumer (e.g. the queue was deleted).
//
// When the broker closes only this consumer's channel (e.g. an invalid ack), Consume reopens it and goes on,
// waiting the same backoff as KeepConnectionAndSetup between attempts; the other consumers are not touched.
// When it can't reopen it, it tears the connection down so the setup runs again, and returns nil.
//
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
	// Holding a slot per running handler caps them even when f acks early, which frees the prefetch window.
	// The slots outlive a reopened channel, so handlers still running on the old one count.
	c := &consumer{r: r, st: st, config: config, f: f, slots: make(chan struct{}, limit)}
	sub, err := c.subscribe()
	if err != nil {
		return
	}
	backoff := reconnectBackoffMin
	for {
		openedAt := time.Now()
		closeErr, err := c.run(ctx, sub)
		if err != nil || closeErr == nil {
			return err
		}
		// As in KeepConnectionAndSetup, only a channel that held up resets the backoff, so a handler that keeps
		// getting it closed does not reopen it in a tight loop.
		if time.Since(openedAt) >= reconnectBackoffMax {
			backoff = reconnectBackoffMin
		}
		log.Printf("rabbitmq consumer channel of queue %s closed by the broker, reopening it in %s: [%s]\n", config.QueueName, backoff, closeErr)
		if !sleepCtx(ctx, backoff) || !r.holds(st) {
			return nil
		}
		backoff = min(backoff*2, reconnectBackoffMax)
		if sub, err = c.subscribe(); err != nil {
			if !st.conn.IsClosed() {
				log.Printf("error reopening the rabbitmq consumer channel of queue %s, reconnecting: [%s]\n", config.QueueName, err)
				st.consumerFailed(err)
			}
			return nil
		}
	}
}

// consumer is one Consume call, which may go through several channels.
type consumer struct {
	r      *rabbit
	st     *state
	config ConfigConsume
	f      func(*amqp.Delivery)
	slots  chan struct{}
}

// subscription is a channel of a consumer and the deliveries it gets on it.
type subscription struct {
	ch     *amqp.Channel
	msgs   <-chan amqp.Delivery
	closed chan *amqp.Error
	// handlers counts the handlers that may still ack on ch, which is only closed after them.
	handlers sync.WaitGroup
}

// subscribe opens a channel and starts the consumer on it. Only this goroutine runs RPCs on that channel, so
// unlike the declarations they need no lock.
func (c *consumer) subscribe() (*subscription, error) {
	ch, err := c.st.conn.Channel()
	if err != nil {
		return nil, err
	}
	sub := &subscription{ch: ch, closed: ch.NotifyClose(make(chan *amqp.Error, 1))}
	if c.st.prefetchCount > 0 {
		if err = ch.Qos(c.st.prefetchCount, 0, false); err != nil {
			_ = ch.Close()
			return nil, err
		}
	}
	sub.msgs, err = ch.Consume(
		c.config.QueueName,
		c.config.Consumer,
		c.config.AutoAck,
		c.config.Exclusive,
		c.config.NoLocal,
		c.config.NoWait,
		c.config.Args,
	)
	if err != nil {
		_ = ch.Close()
		return nil, err
	}
	return sub, nil
}

// run hands the deliveries of sub to the handlers until ctx is done or the deliveries stop. closeErr is set
// only when the broker closed the channel with the connection still up, which is when it is worth reopening.
func (c *consumer) run(ctx context.Context, sub *subscription) (closeErr *amqp.Error, err error) {
	defer c.release(sub)
	for {
		select {
		case msg, ok := <-sub.msgs:
			if !ok {
				return c.closedBy(sub), nil
			}
			if !c.config.ExecuteConcurrent {
				done := c.r.trackHandler()
				c.f(&msg)
				done()
				continue
			}
			select {
			case c.slots <- struct{}{}:
			case <-ctx.Done():
				handBack(&msg, c.config.AutoAck, c.f)
				return nil, c.cancel(sub)
			}
			done := c.r.trackHandler()
			sub.handlers.Add(1)
			go func() {
				defer func() {
					<-c.slots
					sub.handlers.Done()
					done()
				}()
				c.f(&msg)
			}()
		case <-ctx.Done():
			return nil, c.cancel(sub)
		}
	}
}

// closedBy tells why the deliveries of sub stopped. amqp091-go hands the close reason to NotifyClose before it
// closes the deliveries, and marks the connection closed before its channels, so neither check races.
func (c *consumer) closedBy(sub *subscription) *amqp.Error {
	select {
	case closeErr := <-sub.closed:
		if c.st.conn.IsClosed() {
			return nil // the connection is gone: the setup restarts the consumers
		}
		return closeErr // nil when the channel was closed by us
	default:
		return nil // the channel is open: the broker canceled the consumer
	}
}

// cancel stops the consumer and hands back the deliveries amqp buffered for it.
func (c *consumer) cancel(sub *subscription) error {
	if err := sub.ch.Cancel(c.config.Consumer, false); err != nil {
		if errors.Is(err, amqp.ErrClosed) {
			return nil // the channel is gone, and so are the deliveries it buffered
		}
		return err
	}
	drainCanceled(sub.msgs, c.config.AutoAck, c.f)
	return nil
}

// release closes the channel of sub once its handlers are done, since closing it earlier would fail their
// acks and redeliver what they handled. Consume does not wait for it, as it never waited for the handlers.
func (c *consumer) release(sub *subscription) {
	go func() {
		sub.handlers.Wait()
		if err := sub.ch.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			log.Printf("error closing the rabbitmq consumer channel of queue %s: [%s]\n", c.config.QueueName, err)
		}
	}()
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
