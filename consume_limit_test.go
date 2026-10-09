package rabbitmq_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestConfigConsumeValidate(t *testing.T) {
	tests := []struct {
		name          string
		prefetch      int
		autoAck       bool
		concurrent    bool
		maxConcurrent int
		wantErr       error
	}{
		{name: "concurrent without prefetch", prefetch: 0, concurrent: true, wantErr: rabbitmq.ErrUnboundedConcurrency},
		{name: "concurrent with auto ack", prefetch: 2, autoAck: true, concurrent: true, wantErr: rabbitmq.ErrUnboundedConcurrency},
		{name: "concurrent with prefetch", prefetch: 2, concurrent: true, wantErr: nil},
		{name: "max concurrent without prefetch", prefetch: 0, concurrent: true, maxConcurrent: 2, wantErr: nil},
		{name: "max concurrent with auto ack", prefetch: 0, autoAck: true, concurrent: true, maxConcurrent: 2, wantErr: nil},
		{name: "sequential without prefetch", prefetch: 0, autoAck: true, concurrent: false, wantErr: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := rabbitmq.NewConfigConsume("validate", "")
			config.AutoAck = tt.autoAck
			config.ExecuteConcurrent = tt.concurrent
			config.MaxConcurrent = tt.maxConcurrent
			err := config.Validate(rabbitmq.ConfigConnection{PrefetchCount: tt.prefetch})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// Consume applies the same rule as Validate against the prefetch of the channel it got, and fails before
// registering the consumer.
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

// With a backlog larger than the limit, a concurrent consumer never runs more handlers than the limit: the
// prefetch, also when the handler acks first and frees the prefetch window, or MaxConcurrent with AutoAck.
func TestConsumeConcurrentHandlersCapped(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	const limit, total = 3, 10
	tests := []struct {
		name          string
		prefetch      int
		autoAck       bool
		ackFirst      bool
		maxConcurrent int
	}{
		{name: "prefetch, ack at the end", prefetch: limit},
		{name: "prefetch, ack first", prefetch: limit, ackFirst: true},
		{name: "max concurrent with auto ack", prefetch: 0, autoAck: true, maxConcurrent: limit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rabbit := rabbitmq.NewRabbitMQ()
			if _, err := rabbit.Connect(rabbitmq.ConfigConnection{URI: uri, PrefetchCount: tt.prefetch}); err != nil {
				t.Fatalf("failed to connect to rabbitmq: %s", err)
			}
			defer closeConnection(t, rabbit)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()

			queue := "capped"
			createQueueTest(t, rabbit, queue)
			for i := 0; i < total; i++ {
				publishTest(t, ctx, rabbit, "", queue, "backlog")
			}

			consumeCtx, stopConsume := context.WithCancel(ctx)
			release, full, allHandled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var fullOnce sync.Once
			var running, maxRunning, handled atomic.Int32
			ack := func(d *amqp.Delivery) {
				if err := d.Ack(false); err != nil {
					t.Errorf("error acking message: %s", err)
				}
			}
			config := rabbitmq.NewConfigConsume(queue, "")
			config.AutoAck = tt.autoAck
			config.MaxConcurrent = tt.maxConcurrent
			consumeDone := make(chan error, 1)
			go func() {
				consumeDone <- rabbit.Consume(consumeCtx, config, func(d *amqp.Delivery) {
					if tt.ackFirst {
						ack(d)
					}
					n := running.Add(1)
					for {
						m := maxRunning.Load()
						if n <= m || maxRunning.CompareAndSwap(m, n) {
							break
						}
					}
					if n == limit {
						fullOnce.Do(func() { close(full) })
					}
					<-release
					running.Add(-1)
					if !tt.ackFirst && !tt.autoAck {
						ack(d)
					}
					if handled.Add(1) == total {
						close(allHandled)
					}
				})
			}()

			waitSignal(t, full, 10*time.Second, "the limit to fill up")
			time.Sleep(500 * time.Millisecond) // time for an extra handler to start if the cap did not hold
			if got := maxRunning.Load(); got != limit {
				t.Fatalf("expected at most %d handlers running, got %d", limit, got)
			}
			close(release)
			waitSignal(t, allHandled, 10*time.Second, "every delivery to be handled")
			if got := maxRunning.Load(); got != limit {
				t.Fatalf("expected at most %d handlers running, got %d", limit, got)
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
		})
	}
}
