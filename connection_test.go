package rabbitmq_test

import (
	"context"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRabbit(t *testing.T) {
	uri, uiURL := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*20)
	defer cancel()
	notifOpen, notifClose := openConnection(t, ctx, rabbit, uri)
	defer func() {
		closeConnection(t, rabbit)
	}()
	select {
	case <-notifOpen:
		t.Log("rabbitmq connection open")
	case <-ctx.Done():
		t.Error("timeout, failed to open rabbitmq connection")
	case <-notifClose:
		t.Error("rabbitmq connection closed")
	}
	msg := "Hello World!"
	createQueueTest(t, ctx, rabbit)
	publishTest(t, ctx, rabbit, msg)
	msgConsumed := consumeTest(t, ctx, rabbit)
	if msgConsumed != msg {
		t.Errorf("expected message '%s', got '%s'", msg, msgConsumed)
	}
	if *waitFlag {
		t.Logf("waiting 60 seconds. Go to %s for rabbit UI", uiURL)
		time.Sleep(time.Second * 60)
	}
}

func openConnection(t *testing.T, ctx context.Context, rabbit rabbitmq.Connector, uri string) (notifOpen chan struct{}, notifClose chan *amqp.Error) {
	configConn := rabbitmq.ConfigConnection{
		URI:           uri,
		PrefetchCount: 1,
	}
	notifOpen = make(chan struct{})
	rabbitmq.NotifyOpenConnection(notifOpen)
	notifClose, err := rabbit.Connect(configConn)
	if err != nil {
		t.Fatalf("failed to connect to rabbitmq: %s", err)
		return
	}
	return
}

func closeConnection(t *testing.T, rabbit rabbitmq.Closer) {
	ctxDone, cancelDone := context.WithTimeout(context.Background(), time.Second*10)
	notifDone := rabbit.Close(ctxDone)
	select {
	case <-notifDone:
	case <-ctxDone.Done():
		t.Error("failed to close rabbitmq connection")
	}
	cancelDone()
}
