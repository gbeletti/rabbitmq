package rabbitmq

import (
	"fmt"
	"time"

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

// ConfigConsume is the configuration for the consumer.
//
// ExecuteConcurrent runs each delivery in its own goroutine, at most MaxConcurrent at once. When MaxConcurrent
// is not set (0 or less) the limit is ConfigConnection.PrefetchCount, which requires AutoAck off since the broker
// ignores the prefetch for it; without either bound Consume returns ErrUnboundedConcurrency. The limit is per
// Consume call: after a reconnection, handlers still running from the previous channel are not counted.
type ConfigConsume struct {
	QueueName         string
	Consumer          string
	AutoAck           bool
	Exclusive         bool
	NoLocal           bool
	NoWait            bool
	Args              amqp.Table
	ExecuteConcurrent bool
	MaxConcurrent     int
}

// Validate returns the error Consume would return for this configuration on a connection made with conn, so a
// consumer can fail at boot instead of in the setup that runs again after every reconnection.
func (c ConfigConsume) Validate(conn ConfigConnection) error {
	_, err := c.concurrencyLimit(conn.PrefetchCount)
	return err
}

// concurrencyLimit is how many handlers run at once for a consumer on a channel with this prefetch.
func (c ConfigConsume) concurrencyLimit(prefetch int) (int, error) {
	switch {
	case !c.ExecuteConcurrent:
		return 1, nil
	case c.MaxConcurrent > 0:
		return c.MaxConcurrent, nil
	case c.AutoAck:
		return 0, fmt.Errorf("%w: AutoAck is on and MaxConcurrent is not set (queue %q)", ErrUnboundedConcurrency, c.QueueName)
	case prefetch <= 0:
		return 0, fmt.Errorf("%w: connection PrefetchCount is %d and MaxConcurrent is not set (queue %q)", ErrUnboundedConcurrency, prefetch, c.QueueName)
	}
	return prefetch, nil
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
	DeliveryMode    uint8
	ReplyTo         string
	Expiration      string
	Timestamp       time.Time
	Type            string
	UserId          string
	AppId           string
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
