package rabbitmq_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

// connectWithConfirms connects with PublisherConfirms on and declares queue.
func connectWithConfirms(t *testing.T, rabbit rabbitmq.RabbitMQ, uri, queue string) {
	t.Helper()
	if _, err := rabbit.Connect(rabbitmq.ConfigConnection{URI: uri, PrefetchCount: 1, PublisherConfirms: true}); err != nil {
		t.Fatalf("error connecting: %s", err)
	}
	createQueueTest(t, rabbit, queue)
}

func TestPublishConfirmed(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "confirmed"
	connectWithConfirms(t, rabbit, uri, queue)
	defer closeConnection(t, rabbit)

	// Concurrent publishes share the channel, each waiting for its own confirm.
	const total = 50
	var wg sync.WaitGroup
	errs := make(chan error, total)
	for i := range total {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- rabbit.Publish(ctx, fmt.Appendf(nil, "msg-%d", i), rabbitmq.NewConfigPublish("", queue))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("confirmed publish returned error: %s", err)
		}
	}

	// A confirm means the message is in the queue, so the count is exact right away.
	q, err := rabbit.CreateQueue(rabbitmq.ConfigQueue{Name: queue, Durable: true})
	if err != nil {
		t.Fatalf("error declaring queue: %s", err)
	}
	if q.Messages != total {
		t.Errorf("expected %d messages in the queue, got %d", total, q.Messages)
	}
}

func TestPublishToMissingExchangeNotConfirmed(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "notconfirmed"
	connectWithConfirms(t, rabbit, uri, queue)
	defer closeConnection(t, rabbit)

	assertMissingExchange := func(what string) {
		t.Helper()
		err := rabbit.Publish(ctx, []byte("lost"), rabbitmq.NewConfigPublish("does-not-exist", queue))
		if !errors.Is(err, rabbitmq.ErrNotConfirmed) {
			t.Fatalf("%s: expected ErrNotConfirmed, got: %v", what, err)
		}
		var amqpErr *amqp.Error
		if !errors.As(err, &amqpErr) || amqpErr.Code != amqp.NotFound {
			t.Fatalf("%s: expected the broker's 404 in the error chain, got: %v", what, err)
		}
	}
	assertMissingExchange("first publish")

	// The producer channel is reopened in place, still in confirm mode.
	deadline := time.After(15 * time.Second)
	for {
		err := rabbit.Publish(ctx, []byte("after"), rabbitmq.NewConfigPublish("", queue))
		if err == nil {
			break
		}
		if !errors.Is(err, amqp.ErrClosed) {
			t.Fatalf("unexpected error while the producer channel reopens: %s", err)
		}
		select {
		case <-deadline:
			t.Fatal("producer channel was not reopened")
		case <-time.After(100 * time.Millisecond):
		}
	}
	assertMissingExchange("publish on the reopened channel")

	if got := consumeTest(t, ctx, rabbit, queue); got != "after" {
		t.Errorf("expected message %q, got %q", "after", got)
	}
}

func TestPublishConfirmHonorsContext(t *testing.T) {
	container, uri, _ := startRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	queue := "confirmctx"
	connectWithConfirms(t, rabbit, uri, queue)
	defer closeConnection(t, rabbit)

	// Canceled before the send: nothing went out, so it must not look like an unknown outcome.
	canceled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	err := rabbit.Publish(canceled, []byte("never sent"), rabbitmq.NewConfigPublish("", queue))
	if !errors.Is(err, context.Canceled) || errors.Is(err, rabbitmq.ErrNotConfirmed) {
		t.Fatalf("expected context.Canceled without ErrNotConfirmed, got: %v", err)
	}

	// A memory alarm makes the broker stop reading from publishers, so the confirm never comes.
	setWatermark := func(value string) {
		t.Helper()
		code, _, err := container.Exec(context.Background(), []string{"rabbitmqctl", "set_vm_memory_high_watermark", value})
		if err != nil || code != 0 {
			t.Fatalf("set_vm_memory_high_watermark %s: code %d, err %v", value, code, err)
		}
	}
	setWatermark("0")
	defer setWatermark("0.4")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = rabbit.Publish(ctx, []byte("blocked"), rabbitmq.NewConfigPublish("", queue))
	if !errors.Is(err, rabbitmq.ErrNotConfirmed) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected ErrNotConfirmed and context.DeadlineExceeded, got: %v", err)
	}
}
