package rabbitmq_test

import (
	"context"
	"testing"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func consumeTest(t *testing.T, ctx context.Context, rabbit rabbitmq.Consumer, queue string) (msg string) {
	ctxCancel, cancel := context.WithCancel(ctx)
	consumeDone := make(chan struct{})
	// Wait for Consume to return: its Cancel is an RPC on the channel the next declarations use, and
	// concurrent RPCs on one channel get their replies mixed up.
	defer func() {
		cancel()
		<-consumeDone
	}()
	gotMessage := make(chan string)
	var receiveMessage = func(d *amqp.Delivery) {
		gotMessage <- string(d.Body)
		err := d.Ack(false)
		if err != nil {
			t.Errorf("error acking message: %s\n", err)
		}
	}
	go func() {
		defer close(consumeDone)
		config := rabbitmq.ConfigConsume{
			QueueName:         queue,
			Consumer:          "test",
			AutoAck:           false,
			Exclusive:         false,
			NoLocal:           false,
			NoWait:            false,
			Args:              nil,
			ExecuteConcurrent: true,
		}
		if err := rabbit.Consume(ctxCancel, config, receiveMessage); err != nil {
			t.Errorf("error consuming from queue: %s\n", err)
		}
	}()
	select {
	case <-ctx.Done():
		t.Errorf("context canceled before receiving message")
	case msg = <-gotMessage:
		t.Logf("got message: %s\n", msg)
	}
	return
}
