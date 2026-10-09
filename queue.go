package rabbitmq

import amqp "github.com/rabbitmq/amqp091-go"

// CreateQueue creates a queue
func (r *rabbit) CreateQueue(config ConfigQueue) (queue amqp.Queue, err error) {
	st, err := r.current()
	if err != nil {
		return
	}
	st.consumerRPC.Lock()
	defer st.consumerRPC.Unlock()
	queue, err = st.chConsumer.QueueDeclare(
		config.Name,
		config.Durable,
		config.AutoDelete,
		config.Exclusive,
		config.NoWait,
		config.Args,
	)
	return
}

// BindQueueExchange binds a queue to an exchange
func (r *rabbit) BindQueueExchange(config ConfigBindQueue) (err error) {
	st, err := r.current()
	if err != nil {
		return
	}
	st.consumerRPC.Lock()
	defer st.consumerRPC.Unlock()
	err = st.chConsumer.QueueBind(
		config.QueueName,
		config.RoutingKey,
		config.Exchange,
		config.NoWait,
		config.Args,
	)
	return
}

// UnbindQueueExchange unbinds a queue from an exchange
func (r *rabbit) UnbindQueueExchange(config ConfigBindQueue) (err error) {
	st, err := r.current()
	if err != nil {
		return
	}
	st.consumerRPC.Lock()
	defer st.consumerRPC.Unlock()
	err = st.chConsumer.QueueUnbind(
		config.QueueName,
		config.RoutingKey,
		config.Exchange,
		config.Args,
	)
	return
}
