package rabbitmq

import (
	"context"
	"log"

	amqp "github.com/rabbitmq/amqp091-go"
)

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

// Close closes the rabbitMQ connection
func (r *rabbit) Close(ctx context.Context) (done chan struct{}) {
	done = make(chan struct{})

	doneWaiting := make(chan struct{})
	go func() {
		r.wgChannel.Wait()
		close(doneWaiting)
	}()

	go func() {
		defer close(done)
		select { // either waits for the messages to process or timeout from context
		case <-doneWaiting:
		case <-ctx.Done():
		}
		closeConnections(r)
	}()
	return
}

func closeConnections(r *rabbit) {
	var err error
	if r.chConsumer != nil {
		err = r.chConsumer.Close()
		if err != nil {
			log.Printf("Error closing consumer channel: [%s]\n", err)
		}
	}
	if r.chProducer != nil {
		err = r.chProducer.Close()
		if err != nil {
			log.Printf("Error closing producer channel: [%s]\n", err)
		}
	}
	if r.conn != nil {
		err = r.conn.Close()
		if err != nil {
			log.Printf("Error closing connection: [%s]\n", err)
		}
	}
}
