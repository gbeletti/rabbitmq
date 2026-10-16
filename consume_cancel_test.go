package rabbitmq_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

// Deliveries prefetched but not handed to the handler when the context is canceled must go back to the
// queue right away, with the connection still up. The empty consumer tag also checks that Cancel reaches
// the consumer whose tag was generated.
func TestConsumeRequeuesBufferedDeliveriesOnCancel(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	if _, err := rabbit.Connect(rabbitmq.ConfigConnection{URI: uri, PrefetchCount: 10}); err != nil {
		t.Fatalf("failed to connect to rabbitmq: %s", err)
	}
	defer closeConnection(t, rabbit)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	queue := "requeueoncancel"
	createQueueTest(t, rabbit, queue)
	const total = 5
	for i := 0; i < total; i++ {
		publishTest(t, ctx, rabbit, "", queue, "buffered")
	}

	consumeCtx, stopConsume := context.WithCancel(ctx)
	started, release := make(chan struct{}), make(chan struct{})
	var handled atomic.Int32
	config := rabbitmq.NewConfigConsume(queue, "")
	config.ExecuteConcurrent = false
	consumeDone := make(chan error, 1)
	go func() {
		consumeDone <- rabbit.Consume(consumeCtx, config, func(d *amqp.Delivery) {
			if handled.Add(1) == 1 {
				close(started)
				<-release
			}
			_ = d.Ack(false)
		})
	}()

	waitSignal(t, started, 10*time.Second, "first delivery")
	time.Sleep(500 * time.Millisecond) // the other deliveries reach the prefetch buffer
	stopConsume()
	close(release)
	select {
	case err := <-consumeDone:
		if err != nil {
			t.Fatalf("error consuming from queue: %s", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Consume did not return after the context was canceled")
	}

	want := total - int(handled.Load())
	if want == 0 {
		t.Skip("every delivery reached the handler before the cancel, nothing was buffered")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		q, err := rabbit.CreateQueue(rabbitmq.ConfigQueue{Name: queue, Durable: true})
		if err != nil {
			t.Fatalf("error declaring queue: %s", err)
		}
		if q.Messages == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d deliveries back in the queue, got %d ready", want, q.Messages)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
