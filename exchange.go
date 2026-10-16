package rabbitmq

// CreateExchange creates an exchange
func (r *rabbit) CreateExchange(config ConfigExchange) (err error) {
	st, err := r.current()
	if err != nil {
		return
	}
	err = st.chConsumer.ExchangeDeclare(
		config.Name,
		config.Type,
		config.Durable,
		config.AutoDelete,
		config.Internal,
		config.NoWait,
		config.Args,
	)
	return
}
