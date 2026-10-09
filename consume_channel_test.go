package rabbitmq_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gbeletti/rabbitmq"
	amqp "github.com/rabbitmq/amqp091-go"
)

// consumeInBackground runs Consume on queue, sending the body of each delivery to received.
func consumeInBackground(ctx context.Context, rabbit rabbitmq.Consumer, queue string, handle func(d *amqp.Delivery)) (received chan string, done chan error) {
	received, done = make(chan string, 10), make(chan error, 1)
	go func() {
		done <- rabbit.Consume(ctx, rabbitmq.NewConfigConsume(queue, ""), func(d *amqp.Delivery) {
			handle(d)
			received <- string(d.Body)
		})
	}()
	return
}

func ackOnce(d *amqp.Delivery) { _ = d.Ack(false) }

// doubleAckFirst acks the first delivery twice, which makes the broker close the channel with 406
// PRECONDITION_FAILED, and the others once.
func doubleAckFirst() func(d *amqp.Delivery) {
	var calls atomic.Int32
	return func(d *amqp.Delivery) {
		_ = d.Ack(false)
		if calls.Add(1) == 1 {
			_ = d.Ack(false)
		}
	}
}

// The broker closing one consumer's channel must not reach the connection nor the other consumers: that
// Consume reopens its channel and keeps going.
func TestConsumerChannelReopenedWithoutReconnect(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue, other := "consumerclosed", "consumerclosed-other"
	setups, exited := keepConnection(t, ctx, rabbit, uri, queue)
	waitSignal(t, setups, 20*time.Second, "first setup")
	createQueueTest(t, rabbit, other)

	consumeCtx, stopConsume := context.WithCancel(ctx)
	failing, failingDone := consumeInBackground(consumeCtx, rabbit, queue, doubleAckFirst())
	untouched, untouchedDone := consumeInBackground(consumeCtx, rabbit, other, ackOnce)
	publishUntilReceived(t, ctx, rabbit, other, untouched, "before the double ack")

	publishTest(t, ctx, rabbit, "", queue, "double ack")
	waitReceived(t, failing, "double ack")
	publishUntilReceived(t, ctx, rabbit, queue, failing, "after the channel was reopened")
	publishUntilReceived(t, ctx, rabbit, other, untouched, "after the double ack")

	select {
	case <-setups:
		t.Error("the connection was torn down: setup ran again after a consumer channel close")
	case err := <-failingDone:
		t.Errorf("Consume returned after its channel was closed by the broker: %v", err)
	case err := <-untouchedDone:
		t.Errorf("another consumer stopped after a consumer channel close: %v", err)
	default:
	}
	stopConsume()
	for _, done := range []chan error{failingDone, untouchedDone} {
		if err := <-done; err != nil {
			t.Errorf("error consuming from queue: %s", err)
		}
	}
	cancel()
	waitSignal(t, exited, 5*time.Second, "KeepConnectionAndSetup to exit after cancel")
	closeConnection(t, rabbit)
}

// When the channel can't be reopened, here because the queue is gone, Consume falls back to what a consumer
// channel close did before it had a channel of its own: it tears the connection down, so the setup runs again
// and restarts the consumers, and it returns nil.
func TestConsumerChannelNotReopenedReconnects(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "consumernotreopened"
	setups, exited := keepConnection(t, ctx, rabbit, uri, queue)
	waitSignal(t, setups, 20*time.Second, "first setup")

	received, consumeDone := consumeInBackground(ctx, rabbit, queue, doubleAckFirst())
	publishTest(t, ctx, rabbit, "", queue, "double ack")
	waitReceived(t, received, "double ack")
	// Deleting the queue only after the consumer left it keeps the broker from canceling the consumer, which
	// would end Consume without the reopen.
	deleteQueueWhenUnconsumed(t, uri, queue)

	select {
	case err := <-consumeDone:
		if err != nil {
			t.Errorf("Consume should return nil when it can't reopen its channel, got: %s", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Consume did not return after failing to reopen its channel")
	}
	waitSignal(t, setups, 20*time.Second, "setup after the consumer failed to reopen its channel")
	publishAndConsume(t, ctx, rabbit, "", queue, "after the reconnection")

	cancel()
	waitSignal(t, exited, 5*time.Second, "KeepConnectionAndSetup to exit after cancel")
	closeConnection(t, rabbit)
}

// A consumer's channel stays open until its handlers are done, so one still running when Consume returns
// can ack, and the delivery is not redelivered.
func TestHandlerAcksAfterConsumeReturned(t *testing.T) {
	uri, _ := setupRabbitContainer(t)
	rabbit := rabbitmq.NewRabbitMQ()
	if _, err := rabbit.Connect(rabbitmq.ConfigConnection{URI: uri, PrefetchCount: 1}); err != nil {
		t.Fatalf("failed to connect to rabbitmq: %s", err)
	}
	defer closeConnection(t, rabbit)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	queue := "lateack"
	createQueueTest(t, rabbit, queue)
	publishTest(t, ctx, rabbit, "", queue, "late ack")

	consumeCtx, stopConsume := context.WithCancel(ctx)
	started, release, acked := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	consumeDone := make(chan error, 1)
	go func() {
		consumeDone <- rabbit.Consume(consumeCtx, rabbitmq.NewConfigConsume(queue, ""), func(d *amqp.Delivery) {
			close(started)
			<-release
			acked <- d.Ack(false)
		})
	}()
	waitSignal(t, started, 10*time.Second, "the delivery")
	stopConsume()
	select {
	case err := <-consumeDone:
		if err != nil {
			t.Fatalf("error consuming from queue: %s", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Consume did not return after the context was canceled")
	}
	time.Sleep(300 * time.Millisecond) // a close that does not wait for the handler would land meanwhile
	close(release)
	if err := <-acked; err != nil {
		t.Fatalf("the handler could not ack after Consume returned: %s", err)
	}

	time.Sleep(500 * time.Millisecond) // let a close of the channel requeue it, if it was not acked
	q, err := rabbit.CreateQueue(rabbitmq.ConfigQueue{Name: queue, Durable: true})
	if err != nil {
		t.Fatalf("error declaring queue: %s", err)
	}
	if q.Messages != 0 {
		t.Errorf("expected the acked delivery to leave the queue, %d are back", q.Messages)
	}
}

func waitReceived(t *testing.T, received <-chan string, msg string) {
	t.Helper()
	select {
	case got := <-received:
		if got != msg {
			t.Fatalf("expected message %q, got %q", msg, got)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("message %q never reached the consumer", msg)
	}
}

// deleteQueueWhenUnconsumed waits until queue has no consumer and deletes it, on a connection of its own.
func deleteQueueWhenUnconsumed(t *testing.T, uri, queue string) {
	t.Helper()
	conn, err := amqp.Dial(uri)
	if err != nil {
		t.Fatalf("failed to connect to rabbitmq: %s", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("failed to open a channel: %s", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		q, err := ch.QueueDeclarePassive(queue, true, false, false, false, nil)
		if err != nil {
			t.Fatalf("failed to inspect queue %s: %s", queue, err)
		}
		if q.Consumers == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue %s still has %d consumers", queue, q.Consumers)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err = ch.QueueDelete(queue, false, false, false); err != nil {
		t.Fatalf("failed to delete queue %s: %s", queue, err)
	}
}
