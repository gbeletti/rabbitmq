package rabbitmq_test

import (
	"testing"

	"github.com/gbeletti/rabbitmq"
	"github.com/google/go-cmp/cmp"
)

func TestNewConfigConsume(t *testing.T) {
	queueName, consumer := "queue", "consumer"
	expectedConfig := rabbitmq.ConfigConsume{
		QueueName:         queueName,
		Consumer:          consumer,
		AutoAck:           false,
		Exclusive:         false,
		NoLocal:           false,
		NoWait:            false,
		Args:              nil,
		ExecuteConcurrent: true,
	}
	gotConfig := rabbitmq.NewConfigConsume(queueName, consumer)
	if diff := cmp.Diff(expectedConfig, gotConfig); len(diff) > 0 {
		t.Errorf("expected config and got config differ: %s\n", diff)
	}
}

func TestNewConfigPublish(t *testing.T) {
	exchangeName, routingKey := "exchange", "routingKey"
	expectedConfig := rabbitmq.ConfigPublish{
		Exchange:      exchangeName,
		RoutingKey:    routingKey,
		Mandatory:     false,
		Immediate:     false,
		Headers:       nil,
		ContentType:   "",
		Priority:      0,
		CorrelationID: "",
	}
	gotConfig := rabbitmq.NewConfigPublish(exchangeName, routingKey)
	if diff := cmp.Diff(expectedConfig, gotConfig); len(diff) > 0 {
		t.Errorf("expected config and got config differ: %s\n", diff)
	}
}
