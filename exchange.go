package rabbitmq

import amqp "github.com/rabbitmq/amqp091-go"

// CreateExchange creates an exchange
func (r *rabbit) CreateExchange(config ConfigExchange) (err error) {
	st, err := r.current()
	if err != nil {
		return
	}
	return st.consumerCall(func(ch *amqp.Channel) error {
		return ch.ExchangeDeclare(
			config.Name,
			config.Type,
			config.Durable,
			config.AutoDelete,
			config.Internal,
			config.NoWait,
			config.Args,
		)
	})
}
