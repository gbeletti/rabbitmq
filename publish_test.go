package rabbitmq_test

import (
	"context"
	"testing"

	"github.com/gbeletti/rabbitmq"
)

func publishAndConsume(t *testing.T, ctx context.Context, rabbit rabbitmq.RabbitMQ, exchange, queue, msg string) {
	publishTest(t, ctx, rabbit, exchange, queue, msg)
	msgConsumed := consumeTest(t, ctx, rabbit, queue)
	if msgConsumed != msg {
		t.Errorf("exchange [%s] queue [%s] expected message '%s', got '%s'", exchange, queue, msg, msgConsumed)
	}
}

func publishTest(t *testing.T, ctx context.Context, pub rabbitmq.Publisher, exchange, queue, msg string) {
	config := rabbitmq.ConfigPublish{
		Exchange:   exchange,
		RoutingKey: queue,
	}
	body := []byte(msg)
	err := pub.Publish(ctx, body, config)
	if err != nil {
		t.Errorf("exchange [%s] routing key [%s] error publishing message: %s\n", exchange, queue, err)
	}
}
