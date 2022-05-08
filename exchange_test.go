package rabbitmq_test

import (
	"testing"

	"github.com/gbeletti/rabbitmq"
)

func createExchangeTest(t *testing.T, rabbit rabbitmq.ExchangeCreator, exchange, typeExc string) {
	config := rabbitmq.ConfigExchange{
		Name:       exchange,
		Type:       typeExc,
		Durable:    true,
		AutoDelete: false,
		Internal:   false,
		NoWait:     false,
		Args:       nil,
	}
	err := rabbit.CreateExchange(config)
	if err != nil {
		t.Errorf("error creating exchange: %s\n", err)
	}
}
