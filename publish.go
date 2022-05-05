package rabbitmq

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publish publishes body to exchange with routing key
func (r *rabbit) Publish(ctx context.Context, body []byte, config ConfigPublish) (err error) {
	r.wg.Add(1)
	defer r.wg.Done()
	err = r.chProducer.Publish(
		config.Exchange,
		config.RoutingKey,
		config.Mandatory,
		config.Immediate,
		amqp.Publishing{
			Headers:       config.Headers,
			ContentType:   config.ContentType,
			Priority:      config.Priority,
			CorrelationId: config.CorrelationID,
			Body:          body,
		},
	)
	return
}
