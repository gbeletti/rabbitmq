package rabbitmq

import (
	"context"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var notifyOpenConn, notifySetupDone []chan struct{}
var muxNotifyOpenConn, muxNotifySetup sync.Mutex = sync.Mutex{}, sync.Mutex{}

// Connect connects to the rabbitMQ server and also creates the channels to produce and consume messages.
// It can also notify the connection is open to other goroutines if the function NotifyOpenConnection
// is called before connecting.
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
		if err != nil {
			return
		}
	}
	notifyOpenConnections()
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

// KeepConnectionAndSetup starts a goroutine to keep the connection open and everytime the connection is open, it will call the setupRabbit function. It is important to pass a context with cancel so the goroutine can be closed when the context is done. Otherwise it will run until the program ends.
func KeepConnectionAndSetup(ctx context.Context, conn Connector, config ConfigConnection, setupRabbit RabbitSetup) {
	go func() {
		for {
			notifyClose, err := conn.Connect(config)
			if err != nil {
				log.Printf("error connecting to rabbitmq: [%s]\n", err)
				time.Sleep(time.Second * 5)
				continue
			}
			setupRabbit.Setup()
			notifySetupIsDone()
			select {
			case <-notifyClose:
				continue
			case <-ctx.Done():
				return
			}
		}
	}()
}

// NotifyOpenConnection registers a channel to be notified when the connection is open
func NotifyOpenConnection(notify chan struct{}) {
	muxNotifyOpenConn.Lock()
	defer muxNotifyOpenConn.Unlock()
	notifyOpenConn = append(notifyOpenConn, notify)
}

// NotifySetupDone registers a channel to be notified when the setup is done by the KeepConnectionAndSetup function
func NotifySetupDone(notify chan struct{}) {
	muxNotifySetup.Lock()
	defer muxNotifySetup.Unlock()
	notifySetupDone = append(notifySetupDone, notify)
}

func notifyOpenConnections() {
	muxNotifyOpenConn.Lock()
	defer muxNotifyOpenConn.Unlock()
	for _, notify := range notifyOpenConn {
		close(notify)
	}
	notifyOpenConn = make([]chan struct{}, 0)
}

func notifySetupIsDone() {
	muxNotifySetup.Lock()
	defer muxNotifySetup.Unlock()
	for _, notify := range notifySetupDone {
		close(notify)
	}
	notifySetupDone = make([]chan struct{}, 0)
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
