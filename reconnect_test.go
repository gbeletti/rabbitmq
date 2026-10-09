package rabbitmq_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
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

func TestProducerChannelReopenedWithoutReconnect(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "producerclosed"
	setups, exited := keepConnection(t, ctx, rabbit, uri, queue)
	waitSignal(t, setups, 20*time.Second, "first setup")

	// A consumer started before the producer channel dies must keep receiving.
	received := make(chan string, 10)
	consumeCtx, stopConsume := context.WithCancel(ctx)
	consumeDone := make(chan error, 1)
	go func() {
		consumeDone <- rabbit.Consume(consumeCtx, rabbitmq.NewConfigConsume(queue, "longlived"), func(d *amqp.Delivery) {
			received <- string(d.Body)
			_ = d.Ack(false)
		})
	}()

	// The broker closes the producer channel with 404 NOT_FOUND, but the connection stays up.
	if err := rabbit.Publish(ctx, []byte("lost"), rabbitmq.NewConfigPublish("does-not-exist", queue)); err != nil {
		t.Fatalf("publish to missing exchange should only fail asynchronously, got: %s", err)
	}
	publishUntilReceived(t, ctx, rabbit, queue, received, "after producer close")

	// The state swapped in with the new producer channel keeps the prefetch, so a concurrent consumer started
	// now is still bounded by it and accepted.
	lateCtx, stopLate := context.WithTimeout(ctx, time.Second)
	defer stopLate()
	if err := rabbit.Consume(lateCtx, rabbitmq.NewConfigConsume(queue, "afterreopen"), func(d *amqp.Delivery) {
		_ = d.Ack(false)
	}); err != nil {
		t.Errorf("concurrent consumer started after the producer channel reopened failed: %s", err)
	}

	select {
	case <-setups:
		t.Error("the connection was torn down: setup ran again after a producer channel close")
	case err := <-consumeDone:
		t.Errorf("consumer stopped after a producer channel close: %v", err)
	default:
	}
	stopConsume()
	if err := <-consumeDone; err != nil {
		t.Errorf("error consuming from queue: %s", err)
	}

	// Close without canceling the context: a graceful close must stop the reconnection loop.
	closeConnection(t, rabbit)
	waitSignal(t, exited, 5*time.Second, "KeepConnectionAndSetup to exit after Close")
	select {
	case <-setups:
		t.Error("reconnected after Close")
	default:
	}
}

// publishUntilReceived publishes msg until it shows up in received, since the reopened producer channel is
// swapped in asynchronously and a publish racing the broker's channel.close is lost.
func publishUntilReceived(t *testing.T, ctx context.Context, rabbit rabbitmq.Publisher, queue string, received <-chan string, msg string) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		_ = rabbit.Publish(ctx, []byte(msg), rabbitmq.NewConfigPublish("", queue))
		select {
		case got := <-received:
			if got != msg {
				t.Fatalf("expected message %q, got %q", msg, got)
			}
			return
		case <-time.After(500 * time.Millisecond):
		case <-deadline:
			t.Fatalf("message %q never reached the consumer", msg)
		}
	}
}

func TestReconnectAfterConsumerChannelClosedByBroker(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "consumerclosed"
	setups, exited := keepConnection(t, ctx, rabbit, uri, queue)
	waitSignal(t, setups, 20*time.Second, "first setup")

	// Acking the same delivery twice makes the broker close the consumer channel with 406 PRECONDITION_FAILED.
	consumeDone := make(chan error, 1)
	go func() {
		consumeDone <- rabbit.Consume(ctx, rabbitmq.NewConfigConsume(queue, "doubleack"), func(d *amqp.Delivery) {
			_ = d.Ack(false)
			_ = d.Ack(false)
		})
	}()
	publishTest(t, ctx, rabbit, "", queue, "double ack")
	select {
	case err := <-consumeDone:
		if err != nil {
			t.Errorf("Consume should return nil when its channel closes, got: %s", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Consume did not return after its channel was closed")
	}
	waitSignal(t, setups, 20*time.Second, "setup after the consumer channel was closed")
	publishAndConsume(t, ctx, rabbit, "", queue, "after consumer channel close")

	cancel()
	waitSignal(t, exited, 5*time.Second, "KeepConnectionAndSetup to exit after cancel")
	closeConnection(t, rabbit)
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

func TestConnectSupersedesPreviousConnection(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	config := rabbitmq.ConfigConnection{URI: uri}
	first, err := rabbit.Connect(config)
	if err != nil {
		t.Fatalf("failed to connect to rabbitmq: %s", err)
	}
	if _, err = rabbit.Connect(config); err != nil {
		t.Fatalf("failed to connect to rabbitmq again: %s", err)
	}
	defer closeConnection(t, rabbit)
	select {
	case amqpErr := <-first:
		if amqpErr != rabbitmq.ErrSuperseded {
			t.Errorf("expected ErrSuperseded on the replaced connection, got %v", amqpErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the replaced connection was not reported")
	}
}
