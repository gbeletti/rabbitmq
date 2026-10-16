package rabbitmq_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

// The declarations and the consumers' basic.consume/basic.cancel share the consumer channel, and amqp091-go
// does not serialize RPCs on a channel: concurrent ones took each other's reply and failed with
// "unexpected command received".
func TestConcurrentRPCsOnConsumerChannel(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	if _, err := rabbit.Connect(rabbitmq.ConfigConnection{URI: uri, PrefetchCount: 1}); err != nil {
		t.Fatalf("failed to connect to rabbitmq: %s", err)
	}
	defer closeConnection(t, rabbit)

	exchange := "concurrentrpc"
	createExchangeTest(t, rabbit, exchange, "direct")

	// An already canceled context makes Consume run basic.consume and then basic.cancel right away.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	const workers, rounds = 8, 25
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range rounds {
				if err := consumerChannelRPCs(canceled, rabbit, exchange, fmt.Sprintf("concurrentrpc-%d-%d", w, i)); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Minute):
		t.Fatal("concurrent RPCs did not finish")
	}
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func consumerChannelRPCs(ctx context.Context, rabbit rabbitmq.RabbitMQ, exchange, queue string) error {
	err := rabbit.CreateExchange(rabbitmq.ConfigExchange{Name: exchange, Type: "direct", Durable: true})
	if err != nil {
		return fmt.Errorf("declaring exchange: %w", err)
	}
	q, err := rabbit.CreateQueue(rabbitmq.ConfigQueue{Name: queue, Exclusive: true})
	if err != nil {
		return fmt.Errorf("declaring queue %s: %w", queue, err)
	}
	if q.Name != queue {
		return fmt.Errorf("declaring queue %s: got the reply for %s", queue, q.Name)
	}
	bind := rabbitmq.ConfigBindQueue{QueueName: queue, Exchange: exchange, RoutingKey: queue}
	if err = rabbit.BindQueueExchange(bind); err != nil {
		return fmt.Errorf("binding queue %s: %w", queue, err)
	}
	if err = rabbit.UnbindQueueExchange(bind); err != nil {
		return fmt.Errorf("unbinding queue %s: %w", queue, err)
	}
	err = rabbit.Consume(ctx, rabbitmq.NewConfigConsume(queue, ""), func(*amqp.Delivery) {})
	if err != nil {
		return fmt.Errorf("consuming queue %s: %w", queue, err)
	}
	return nil
}
