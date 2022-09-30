package rabbitmq

import (
	amqp "github.com/rabbitmq/amqp091-go"
)

// ConfigConnection is the configuration for the connection
type ConfigConnection struct {
	URI           string
	PrefetchCount int
}

// ConfigQueue is the configuration for the queue
type ConfigQueue struct {
	Name       string
	Durable    bool
	AutoDelete bool
	Exclusive  bool
	NoWait     bool
	Args       amqp.Table
}

// ConfigBindQueue is the configuration for the bind to queue
type ConfigBindQueue struct {
	QueueName  string
	Exchange   string
	RoutingKey string
	NoWait     bool
	Args       amqp.Table
}

// ConfigExchange is the configuration for the exchange
type ConfigExchange struct {
	Name       string
	Type       string
	Durable    bool
	AutoDelete bool
	Internal   bool
	NoWait     bool
	Args       amqp.Table
}

// ConfigConsume is the configuration for the consumer
type ConfigConsume struct {
	QueueName         string
	Consumer          string
	AutoAck           bool
	Exclusive         bool
	NoLocal           bool
	NoWait            bool
	Args              amqp.Table
	ExecuteConcurrent bool
}

// ConfigPublish is the configuration for the publisher
type ConfigPublish struct {
	Exchange        string
	RoutingKey      string
	Mandatory       bool
	Immediate       bool
	Headers         amqp.Table
	ContentType     string
	ContentEncoding string
	Priority        uint8
	CorrelationID   string
	MessageID       string
}

// NewConfigConsume helper function to create a new ConfigConsume with some default values
func NewConfigConsume(queueName, consumer string) ConfigConsume {
	return ConfigConsume{
		QueueName:         queueName,
		Consumer:          consumer,
		AutoAck:           false,
		Exclusive:         false,
		NoLocal:           false,
		NoWait:            false,
		Args:              nil,
		ExecuteConcurrent: true,
	}
}

// NewConfigPublish helper function to create a new ConfigPublish with some default values
func NewConfigPublish(exchange, routingKey string) ConfigPublish {
	return ConfigPublish{
		Exchange:        exchange,
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
}
