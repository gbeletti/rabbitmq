package rabbitmq

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
)

// RabbitMQ combines all the interfaces of the package
type RabbitMQ interface {
	Connector
	Closer
	QueueCreator
	QueueBinder
	Consumer
	Publisher
}

// Connector is an interface for connecting to a RabbitMQ server
type Connector interface {
	Connect(config ConfigConnection) (notify chan *amqp.Error, err error)
}

// Closer is an interface for closing a RabbitMQ connection
type Closer interface {
	Close(ctx context.Context) (done chan struct{})
}

// QueueCreator is the interface for creating
type QueueCreator interface {
	CreateQueue(config ConfigQueue) (queue amqp.Queue, err error)
}

// QueueBinder is the interface for binding and unbinding queues
type QueueBinder interface {
	BindQueueExchange(config ConfigBindQueue) (err error)
	UnbindQueueExchange(config ConfigBindQueue) (err error)
}

// Consumer is the interface for consuming messages from a queue
type Consumer interface {
	Consume(ctx context.Context, config ConfigConsume, f func(*amqp.Delivery)) (err error)
}

// Publisher is the interface for publishing messages to an exchange
type Publisher interface {
	Publish(ctx context.Context, body []byte, config ConfigPublish) (err error)
}

// RabbitSetup is the interface for setting up the RabbitMQ queues and exchanges after the connection is made
type RabbitSetup interface {
	Setup()
}

// Setup is a type that implements the RabbitSetup interface
type Setup func()

// Setup executes the setup function
func (s Setup) Setup() {
	s()
}
