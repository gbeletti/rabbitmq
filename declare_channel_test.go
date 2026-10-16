package rabbitmq_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

// A declaration the broker refuses closes the channel it ran on. It must fail only that call: the consumers
// keep receiving, the connection is not torn down, and the next declaration works.
func TestFailedDeclarationKeepsConsumers(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "declarefailed"
	setups, exited := keepConnection(t, ctx, rabbit, uri, queue)
	waitSignal(t, setups, 20*time.Second, "first setup")

	received := make(chan string, 10)
	consumeCtx, stopConsume := context.WithCancel(ctx)
	consumeDone := make(chan error, 1)
	go func() {
		consumeDone <- rabbit.Consume(consumeCtx, rabbitmq.NewConfigConsume(queue, "untouched"), func(d *amqp.Delivery) {
			received <- string(d.Body)
			_ = d.Ack(false)
		})
	}()
	publishUntilReceived(t, ctx, rabbit, queue, received, "before the failed declaration")

	// The queue is durable; redeclaring it as transient is refused with 406 PRECONDITION_FAILED.
	_, err := rabbit.CreateQueue(rabbitmq.ConfigQueue{Name: queue, Durable: false})
	var amqpErr *amqp.Error
	if !errors.As(err, &amqpErr) || amqpErr.Code != amqp.PreconditionFailed {
		t.Fatalf("expected 406 PRECONDITION_FAILED redeclaring the queue, got: %v", err)
	}
	createQueueTest(t, rabbit, queue+"-next")
	publishUntilReceived(t, ctx, rabbit, queue, received, "after the failed declaration")

	select {
	case <-setups:
		t.Error("the connection was torn down: setup ran again after a failed declaration")
	case err := <-consumeDone:
		t.Errorf("consumer stopped after a failed declaration: %v", err)
	default:
	}
	stopConsume()
	if err := <-consumeDone; err != nil {
		t.Errorf("error consuming from queue: %s", err)
	}
	cancel()
	waitSignal(t, exited, 5*time.Second, "KeepConnectionAndSetup to exit after cancel")
	closeConnection(t, rabbit)
}
