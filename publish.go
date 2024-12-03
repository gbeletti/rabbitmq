package rabbitmq

import (
	"context"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Publish publishes body to exchange with routing key
func (r *rabbit) Publish(ctx context.Context, body []byte, config ConfigPublish) (err error) {
	if r.chConsumer == nil {
		return amqp.ErrClosed
	}
	r.wg.Add(1)
	defer r.wg.Done()
	err = r.chProducer.PublishWithContext(
		ctx,
		config.Exchange,
		config.RoutingKey,
		config.Mandatory,
		config.Immediate,
		amqp.Publishing{
			Headers:         config.Headers,
			ContentType:     config.ContentType,
			ContentEncoding: config.ContentEncoding,
			Priority:        config.Priority,
			CorrelationId:   config.CorrelationID,
			MessageId:       config.MessageID,
			Body:            body,
			DeliveryMode:    config.DeliveryMode,
			ReplyTo:         config.ReplyTo,
			Expiration:      config.Expiration,
			Timestamp:       config.Timestamp,
			Type:            config.Type,
			UserId:          config.UserId,
			AppId:           config.AppId,
		},
	)
	return
}
