package rabbitmq_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
)

// keepConnection starts KeepConnectionAndSetup with a setup that declares queue and signals every run.
func keepConnection(t *testing.T, ctx context.Context, rabbit rabbitmq.RabbitMQ, uri, queue string) (setups chan struct{}, exited <-chan struct{}) {
	setups = make(chan struct{}, 10)
	setup := rabbitmq.Setup(func() {
		createQueueTest(t, rabbit, queue)
		setups <- struct{}{}
	})
	config := rabbitmq.ConfigConnection{URI: uri, PrefetchCount: 1}
	exited = rabbitmq.KeepConnectionAndSetup(ctx, rabbit, config, setup)
	return
}

func waitSignal(t *testing.T, ch <-chan struct{}, timeout time.Duration, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for %s", what)
	}
}

func TestReconnectAfterChannelClosedByBroker(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "channelclosed"
	setups, exited := keepConnection(t, ctx, rabbit, uri, queue)
	waitSignal(t, setups, 20*time.Second, "first setup")

	// The broker closes the producer channel with 404 NOT_FOUND, but the connection stays up.
	if err := rabbit.Publish(ctx, []byte("lost"), rabbitmq.NewConfigPublish("does-not-exist", queue)); err != nil {
		t.Fatalf("publish to missing exchange should only fail asynchronously, got: %s", err)
	}
	waitSignal(t, setups, 20*time.Second, "setup after the channel was closed")
	publishAndConsume(t, ctx, rabbit, "", queue, "after channel close")

	// Close without canceling the context: a graceful close must stop the reconnection loop.
	closeConnection(t, rabbit)
	waitSignal(t, exited, 5*time.Second, "KeepConnectionAndSetup to exit after Close")
	select {
	case <-setups:
		t.Error("reconnected after Close")
	default:
	}
}

func TestReconnectAfterConnectionDropped(t *testing.T) {
	container, uri, _ := startRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "connectiondropped"
	setups, exited := keepConnection(t, ctx, rabbit, uri, queue)
	waitSignal(t, setups, 20*time.Second, "first setup")

	code, out, err := container.Exec(ctx, []string{"rabbitmqctl", "close_all_connections", "test"})
	if err != nil || code != 0 {
		output, _ := io.ReadAll(out)
		t.Fatalf("failed to close connections on the broker: code %d err %v: %s", code, err, output)
	}
	waitSignal(t, setups, 20*time.Second, "setup after the connection dropped")
	publishAndConsume(t, ctx, rabbit, "", queue, "after connection drop")

	cancel()
	waitSignal(t, exited, 5*time.Second, "KeepConnectionAndSetup to exit after cancel")
	closeConnection(t, rabbit)
}

func TestKeepConnectionStopsOnCancelWithBrokerDown(t *testing.T) {
	container, uri, _ := startRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	setups, exited := keepConnection(t, ctx, rabbit, uri, "brokerdown")
	waitSignal(t, setups, 20*time.Second, "first setup")

	stopTimeout := 10 * time.Second
	if err := container.Stop(ctx, &stopTimeout); err != nil {
		t.Fatalf("failed to stop rabbitmq container: %s", err)
	}
	// Let the loop notice the drop and fail a few dials, so it is sleeping in the backoff when canceled.
	time.Sleep(4 * time.Second)
	select {
	case <-exited:
		t.Fatal("KeepConnectionAndSetup exited before the context was canceled")
	default:
	}

	cancel()
	waitSignal(t, exited, 2*time.Second, "KeepConnectionAndSetup to exit after cancel")
	closeConnection(t, rabbit)
}
