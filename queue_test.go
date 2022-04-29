package rabbitmq_test

import (
	"context"
	"testing"

	"github.com/gbeletti/rabbitmq"
)

func createQueueTest(t *testing.T, ctx context.Context, rabbit rabbitmq.QueueCreator) {
	config := rabbitmq.ConfigQueue{
		Name:       "test",
		Durable:    true,
		AutoDelete: false,
		Exclusive:  false,
		NoWait:     false,
		Args:       nil,
	}
	_, err := rabbit.CreateQueue(config)
	if err != nil {
		t.Errorf("error creating queue: %s\n", err)
	}
}
