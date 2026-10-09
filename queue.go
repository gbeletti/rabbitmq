package rabbitmq

import amqp "github.com/rabbitmq/amqp091-go"

// CreateQueue creates a queue
func (r *rabbit) CreateQueue(config ConfigQueue) (queue amqp.Queue, err error) {
	st, err := r.current()
	if err != nil {
		return
	}
	err = st.declarer.call(func(ch *amqp.Channel) (err error) {
		queue, err = ch.QueueDeclare(
			config.Name,
			config.Durable,
			config.AutoDelete,
			config.Exclusive,
			config.NoWait,
			config.Args,
		)
		return
	})
	return
}

// BindQueueExchange binds a queue to an exchange
func (r *rabbit) BindQueueExchange(config ConfigBindQueue) (err error) {
	st, err := r.current()
	if err != nil {
		return
	}
	return st.declarer.call(func(ch *amqp.Channel) error {
		return ch.QueueBind(
			config.QueueName,
			config.RoutingKey,
			config.Exchange,
			config.NoWait,
			config.Args,
		)
	})
}

// UnbindQueueExchange unbinds a queue from an exchange
func (r *rabbit) UnbindQueueExchange(config ConfigBindQueue) (err error) {
	st, err := r.current()
	if err != nil {
		return
	}
	return st.declarer.call(func(ch *amqp.Channel) error {
		return ch.QueueUnbind(
			config.QueueName,
			config.RoutingKey,
			config.Exchange,
			config.Args,
		)
	})
}
