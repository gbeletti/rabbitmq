package rabbitmq_test

import (
	"testing"

	"github.com/gbeletti/rabbitmq"
)

func createQueueTest(t *testing.T, rabbit rabbitmq.QueueCreator, queue string) {
	config := rabbitmq.ConfigQueue{
		Name:       queue,
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

func bindQueueTest(t *testing.T, rabbit rabbitmq.QueueBinder, exchange, queue string) {
	config := rabbitmq.ConfigBindQueue{
		QueueName:  queue,
		RoutingKey: queue,
		Exchange:   exchange,
		NoWait:     false,
		Args:       nil,
	}
	err := rabbit.BindQueueExchange(config)
	if err != nil {
		t.Errorf("exchange [%s] queue [%s] error binding queue: %s\n", exchange, queue, err)
	}
}

func unbindQueueTest(t *testing.T, rabbit rabbitmq.QueueBinder, exchange, queue string) {
	config := rabbitmq.ConfigBindQueue{
		QueueName:  queue,
		RoutingKey: queue,
		Exchange:   exchange,
	}
	err := rabbit.UnbindQueueExchange(config)
	if err != nil {
		t.Errorf("exchange [%s] queue [%s] error unbinding queue: %s\n", exchange, queue, err)
	}
}
