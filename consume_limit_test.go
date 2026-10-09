package rabbitmq_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

// A concurrent consumer runs one goroutine per delivery, so it is only accepted when the prefetch bounds the
// deliveries in flight. The broker ignores the prefetch with AutoAck, so that is rejected too.
func TestConsumeRejectsUnboundedConcurrency(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	tests := []struct {
		name       string
		prefetch   int
		autoAck    bool
		concurrent bool
		wantErr    error
	}{
		{name: "concurrent without prefetch", prefetch: 0, concurrent: true, wantErr: rabbitmq.ErrUnboundedConcurrency},
		{name: "concurrent with auto ack", prefetch: 2, autoAck: true, concurrent: true, wantErr: rabbitmq.ErrUnboundedConcurrency},
		{name: "sequential without prefetch", prefetch: 0, concurrent: false, wantErr: nil},
		{name: "concurrent with prefetch", prefetch: 2, concurrent: true, wantErr: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rabbit := rabbitmq.NewRabbitMQ()
			if _, err := rabbit.Connect(rabbitmq.ConfigConnection{URI: uri, PrefetchCount: tt.prefetch}); err != nil {
				t.Fatalf("failed to connect to rabbitmq: %s", err)
			}
			defer closeConnection(t, rabbit)
			queue := "unbounded"
			createQueueTest(t, rabbit, queue)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			config := rabbitmq.NewConfigConsume(queue, "")
			config.AutoAck = tt.autoAck
			config.ExecuteConcurrent = tt.concurrent
			err := rabbit.Consume(ctx, config, func(d *amqp.Delivery) {})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			if tt.wantErr != nil && ctx.Err() != nil {
				t.Fatal("Consume should fail right away instead of consuming until the context is done")
			}
		})
	}
}

// With a backlog larger than the prefetch, a concurrent consumer never runs more handlers than the prefetch.
func TestConsumeConcurrentHandlersCappedByPrefetch(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	const prefetch, total = 3, 10
	rabbit := rabbitmq.NewRabbitMQ()
	if _, err := rabbit.Connect(rabbitmq.ConfigConnection{URI: uri, PrefetchCount: prefetch}); err != nil {
		t.Fatalf("failed to connect to rabbitmq: %s", err)
	}
	defer closeConnection(t, rabbit)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	queue := "cappedbyprefetch"
	createQueueTest(t, rabbit, queue)
	for i := 0; i < total; i++ {
		publishTest(t, ctx, rabbit, "", queue, "backlog")
	}

	consumeCtx, stopConsume := context.WithCancel(ctx)
	release, full, allHandled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var running, maxRunning, handled atomic.Int32
	config := rabbitmq.NewConfigConsume(queue, "")
	consumeDone := make(chan error, 1)
	go func() {
		consumeDone <- rabbit.Consume(consumeCtx, config, func(d *amqp.Delivery) {
			n := running.Add(1)
			for {
				m := maxRunning.Load()
				if n <= m || maxRunning.CompareAndSwap(m, n) {
					break
				}
			}
			if n == prefetch {
				close(full)
			}
			<-release
			running.Add(-1)
			if err := d.Ack(false); err != nil {
				t.Errorf("error acking message: %s", err)
			}
			if handled.Add(1) == total {
				close(allHandled)
			}
		})
	}()

	waitSignal(t, full, 10*time.Second, "the prefetch to fill up")
	time.Sleep(500 * time.Millisecond) // time for an extra delivery to show up if the cap did not hold
	if got := maxRunning.Load(); got != prefetch {
		t.Fatalf("expected at most %d handlers running, got %d", prefetch, got)
	}
	close(release)
	waitSignal(t, allHandled, 10*time.Second, "every delivery to be handled")
	if got := maxRunning.Load(); got != prefetch {
		t.Fatalf("expected at most %d handlers running, got %d", prefetch, got)
	}

	stopConsume()
	select {
	case err := <-consumeDone:
		if err != nil {
			t.Fatalf("error consuming from queue: %s", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Consume did not return after the context was canceled")
	}
}
