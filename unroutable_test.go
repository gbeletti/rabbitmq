package rabbitmq_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestPublishMandatoryUnroutable(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const exchange, queue = "unroutable", "routed"
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
		if !strings.Contains(err.Error(), "312 NO_ROUTE") {
			t.Errorf("expected the broker's reply in the error, got: %v", err)
		}
	})

	t.Run("binding matches", func(t *testing.T) {
		if err := rabbit.Publish(ctx, []byte("routed"), mandatory(queue)); err != nil {
			t.Fatalf("expected nil, got: %v", err)
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
}

func TestReopenedProducerChannelLeavesNoListener(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "reopened"
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
