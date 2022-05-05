package rabbitmq

import (
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

type rabbit struct {
	conn       *amqp.Connection
	chConsumer *amqp.Channel
	chProducer *amqp.Channel
	wg         *sync.WaitGroup
}

// NewRabbitMQ creates the object to manage the operations to rabbitMQ
func NewRabbitMQ() RabbitMQ {
	return &rabbit{
		wg: &sync.WaitGroup{},
	}
}
