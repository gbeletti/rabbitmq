package rabbitmq_test

import (
	"errors"
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
		Exchange:        exchangeName,
		RoutingKey:      routingKey,
		Mandatory:       false,
		Immediate:       false,
		Headers:         nil,
		ContentType:     "",
		ContentEncoding: "utf-8",
		Priority:        0,
		CorrelationID:   "",
		MessageID:       "",
	}
	gotConfig := rabbitmq.NewConfigPublish(exchangeName, routingKey)
	if diff := cmp.Diff(expectedConfig, gotConfig); len(diff) > 0 {
		t.Errorf("expected config and got config differ: %s\n", diff)
	}
}

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
