package rabbitmq_test

import (
	"context"
	"testing"

	"github.com/gbeletti/rabbitmq"
)

func publishTest(t *testing.T, ctx context.Context, pub rabbitmq.Publisher, msg string) {
	config := rabbitmq.ConfigPublish{
		Exchange:   "",
		RoutingKey: "test",
	}
	body := []byte(msg)
	err := pub.Publish(ctx, body, config)
	if err != nil {
		t.Errorf("error publishing message: %s\n", err)
	}
}
