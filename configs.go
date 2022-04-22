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
	Exchange      string
	RoutingKey    string
	Mandatory     bool
	Immediate     bool
	Headers       amqp.Table
	ContentType   string
	Priority      uint8
	CorrelationID string
}
