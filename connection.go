package rabbitmq

import amqp "github.com/rabbitmq/amqp091-go"

// Connect connects to the rabbitMQ server and also creates the channels to produce and consume messages
func (r *rabbit) Connect(config ConfigConnection) (notify chan *amqp.Error, err error) {
	notify = make(chan *amqp.Error)
	r.conn, err = amqp.Dial(config.URI)
	if err != nil {
		return
	}
	r.conn.NotifyClose(notify)
	r.chProducer, err = r.conn.Channel()
	if err != nil {
		return
	}
	r.chConsumer, err = r.conn.Channel()
	if err != nil {
		return
	}
	if config.PrefetchCount > 0 {
		err = r.chConsumer.Qos(config.PrefetchCount, 0, false)
	}
	return
}

func (r *rabbit) Close() (err error) {
	defer func() {
		err = r.conn.Close()
	}()
	r.wgChannel.Wait()
	err = r.chConsumer.Close()
	if err != nil {
		return
	}
	err = r.chProducer.Close()
	if err != nil {
		return
	}
	return
}
