//go:build integration

package rabbitmq_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestPublishMandatoryUnroutable(t *testing.T) {
	t.Parallel()
	uri, _ := sharedRabbit(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	exchange, queue := uniqueName("unroutable"), uniqueName("routed")
	connectWithConfirms(t, rabbit, uri, queue)
	defer closeConnection(t, rabbit)
	createExchangeTest(t, rabbit, exchange, "direct")
	bindQueueTest(t, rabbit, exchange, queue)

	mandatory := func(routingKey string) rabbitmq.ConfigPublish {
		config := rabbitmq.NewConfigPublish(exchange, routingKey)
		config.Mandatory = true
		return config
	}
	queued := func() int {
		t.Helper()
		q, err := rabbit.CreateQueue(rabbitmq.ConfigQueue{Name: queue, Durable: true})
		if err != nil {
			t.Fatalf("error declaring queue: %s", err)
		}
		return q.Messages
	}

	t.Run("no binding matches", func(t *testing.T) {
		err := rabbit.Publish(ctx, []byte("dropped"), mandatory("no-binding"))
		if !errors.Is(err, rabbitmq.ErrUnroutable) {
			t.Fatalf("expected ErrUnroutable, got: %v", err)
		}
		if errors.Is(err, rabbitmq.ErrNotConfirmed) {
			t.Errorf("an unroutable message has a known outcome, expected no ErrNotConfirmed, got: %v", err)
		}
		var amqpErr *amqp.Error
		if !errors.As(err, &amqpErr) || amqpErr.Code != amqp.NoRoute {
			t.Errorf("expected the broker's 312 NO_ROUTE in the error chain, got: %v", err)
		}
	})

	t.Run("binding matches", func(t *testing.T) {
		config := mandatory(queue)
		config.Headers = amqp.Table{"app": "test"}
		if err := rabbit.Publish(ctx, []byte("routed"), config); err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
		if _, ok := config.Headers[rabbitmq.ReturnIDHeader]; ok || len(config.Headers) != 1 {
			t.Errorf("the caller's headers must not change, got: %v", config.Headers)
		}
		if got := consumeTest(t, ctx, rabbit, queue); got != "routed" {
			t.Errorf("expected message %q, got %q", "routed", got)
		}
	})

	t.Run("not mandatory", func(t *testing.T) {
		// Without Mandatory the broker drops the message and acks it, as before.
		if err := rabbit.Publish(ctx, []byte("dropped"), rabbitmq.NewConfigPublish(exchange, "no-binding")); err != nil {
			t.Fatalf("expected nil, got: %v", err)
		}
		if got := queued(); got != 0 {
			t.Errorf("expected an empty queue, got %d messages", got)
		}
	})

	t.Run("concurrent", func(t *testing.T) {
		// Persistent messages to a durable queue are acked after the disk write, so the returns of the
		// unroutable ones arrive while routable ones sent before them are still unconfirmed. Same body for all,
		// so only the routing key tells them apart.
		const total = 200
		var wg sync.WaitGroup
		errs := make([]error, total)
		for i := range total {
			wg.Add(1)
			go func() {
				defer wg.Done()
				config := mandatory("no-binding")
				if i%2 == 0 {
					config = mandatory(queue)
					config.DeliveryMode = amqp.Persistent
				}
				errs[i] = rabbit.Publish(ctx, []byte("payload"), config)
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if i%2 == 0 && err != nil {
				t.Errorf("publish %d is routable, expected nil, got: %v", i, err)
			}
			if i%2 == 1 && !errors.Is(err, rabbitmq.ErrUnroutable) {
				t.Errorf("publish %d is unroutable, expected ErrUnroutable, got: %v", i, err)
			}
		}
		if got := queued(); got != total/2 {
			t.Errorf("expected %d messages in the queue, got %d", total/2, got)
		}
	})

	t.Run("routed only by CC or BCC", func(t *testing.T) {
		// CC and BCC are extra routing keys in any exchange type (sender-selected distribution), so two
		// messages equal in exchange, routing key, ids and body route differently. The broker strips BCC, so
		// the return can't be told apart by that header either.
		const pairs = 100
		ccQueue := uniqueName("cc")
		createQueueTest(t, rabbit, ccQueue)
		bindQueueTest(t, rabbit, exchange, ccQueue)
		var wg sync.WaitGroup
		routedErrs, droppedErrs := make([]error, pairs), make([]error, pairs)
		for i := range pairs {
			routed := mandatory("no-binding")
			routed.DeliveryMode = amqp.Persistent // its ack waits for the disk, so it is pending when the other returns
			header := "CC"
			if i%2 == 1 {
				header = "BCC"
			}
			routed.Headers = amqp.Table{header: []interface{}{ccQueue}}
			wg.Add(2)
			go func() {
				defer wg.Done()
				routedErrs[i] = rabbit.Publish(ctx, []byte("same"), routed)
			}()
			go func() {
				defer wg.Done()
				time.Sleep(time.Millisecond)
				droppedErrs[i] = rabbit.Publish(ctx, []byte("same"), mandatory("no-binding"))
			}()
		}
		wg.Wait()
		for i := range pairs {
			if routedErrs[i] != nil {
				t.Errorf("pair %d: routed by CC/BCC, expected nil, got: %v", i, routedErrs[i])
			}
			if !errors.Is(droppedErrs[i], rabbitmq.ErrUnroutable) {
				t.Errorf("pair %d: unroutable, expected ErrUnroutable, got: %v", i, droppedErrs[i])
			}
		}
		q, err := rabbit.CreateQueue(rabbitmq.ConfigQueue{Name: ccQueue, Durable: true})
		if err != nil {
			t.Fatalf("error declaring queue: %s", err)
		}
		if q.Messages != pairs {
			t.Errorf("expected %d messages in the queue, got %d", pairs, q.Messages)
		}
	})
}

// Not parallel: it counts the goroutines of the whole process.
func TestReopenedProducerChannelLeavesNoListener(t *testing.T) {
	uri, _ := sharedRabbit(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := uniqueName("reopened")
	connectWithConfirms(t, rabbit, uri, queue)
	defer closeConnection(t, rabbit)

	// A missing exchange closes the producer channel instead of returning the message, so a mandatory
	// publish gets the close, and the channel is reopened with new listeners.
	reopen := func() {
		t.Helper()
		config := rabbitmq.NewConfigPublish("does-not-exist", queue)
		config.Mandatory = true
		err := rabbit.Publish(ctx, []byte("lost"), config)
		if !errors.Is(err, rabbitmq.ErrNotConfirmed) || errors.Is(err, rabbitmq.ErrUnroutable) {
			t.Fatalf("expected ErrNotConfirmed without ErrUnroutable, got: %v", err)
		}
		deadline := time.After(15 * time.Second)
		for {
			err := rabbit.Publish(ctx, []byte("after"), rabbitmq.NewConfigPublish("", queue))
			if err == nil {
				return
			}
			if !errors.Is(err, amqp.ErrClosed) {
				t.Fatalf("unexpected error while the producer channel reopens: %s", err)
			}
			select {
			case <-deadline:
				t.Fatal("producer channel was not reopened")
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	reopen()
	baseline := runtime.NumGoroutine()
	for range 10 {
		reopen()
	}

	// The listeners of each closed channel exit once amqp091 closes their notification channels.
	deadline := time.After(5 * time.Second)
	for runtime.NumGoroutine() > baseline {
		select {
		case <-deadline:
			t.Fatalf("goroutines grew from %d to %d over 10 reopens", baseline, runtime.NumGoroutine())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
