package rabbitmq

import (
	"context"
	"errors"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var notifyOpenConn, notifySetupDone []chan struct{}
var muxNotifyOpenConn, muxNotifySetup sync.Mutex = sync.Mutex{}, sync.Mutex{}

// Backoff between failed connection attempts in KeepConnectionAndSetup.
var reconnectBackoffMin, reconnectBackoffMax = time.Second, 30 * time.Second

// closeTimeout bounds how long closing a connection waits for the broker's reply.
const closeTimeout = 5 * time.Second

// Connect connects to the rabbitMQ server and also creates the channels to produce and consume messages.
// It can also notify the connection is open to other goroutines if the function NotifyOpenConnection
// is called before connecting.
//
// The returned channel fires when the connection or its consumer channel closes: it receives the
// *amqp.Error and is then closed when the close came from the broker, and it is only closed (no error) when
// the close was graceful, i.e. Close was called. When the broker closes the consumer channel with the
// connection still up (e.g. an invalid ack), the connection is torn down so the caller reconnects and
// restarts the consumers. When it closes the producer channel (e.g. publishing to an exchange that does not
// exist), only that channel is reopened and nothing is reported here, so consumers are not disturbed; with
// ConfigConnection.PublisherConfirms on, the Publish that caused it gets the error instead.
//
// Calling Connect again replaces and closes the previous connection, whose channel then receives
// ErrSuperseded. Do not run Connect or KeepConnectionAndSetup concurrently on the same client.
func (r *rabbit) Connect(config ConfigConnection) (notify chan *amqp.Error, err error) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil, ErrClientClosed
	}

	st, err := dial(config)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		st.close()
		return nil, ErrClientClosed
	}
	old := r.st
	r.st = st
	r.mu.Unlock()
	if old != nil {
		old.close()
	}

	notify = make(chan *amqp.Error, 1)
	go r.watch(st, notify)
	notifyOpenConnections()
	return notify, nil
}

// dial opens the connection and its channels, closing the connection if any step after the dial fails so it
// does not leak a TCP connection per retry.
func dial(config ConfigConnection) (*state, error) {
	conn, err := amqp.Dial(config.URI)
	if err != nil {
		return nil, err
	}
	st := &state{conn: conn, consumerRPC: new(sync.Mutex), prefetchCount: config.PrefetchCount, confirms: config.PublisherConfirms}
	st.connClose = conn.NotifyClose(make(chan *amqp.Error, 1))
	if err = st.openProducer(); err != nil {
		st.close()
		return nil, err
	}
	st.chConsumer, err = conn.Channel()
	if err != nil {
		st.close()
		return nil, err
	}
	st.consumerClose = st.chConsumer.NotifyClose(make(chan *amqp.Error, 1))
	if config.PrefetchCount > 0 {
		err = st.chConsumer.Qos(config.PrefetchCount, 0, false)
		if err != nil {
			st.close()
			return nil, err
		}
	}
	return st, nil
}

// openProducer opens the producer channel on st.conn, in confirm mode when st.confirms is set, and sets
// every producer field of st. On error nothing is left open and st is not changed.
func (st *state) openProducer() error {
	ch, err := st.conn.Channel()
	if err != nil {
		return err
	}
	producerClose := ch.NotifyClose(make(chan *amqp.Error, 1))
	var reason *closeReason
	if st.confirms {
		reason = watchClose(ch)
		if err = ch.Confirm(false); err != nil {
			_ = ch.Close()
			return err
		}
	}
	st.chProducer, st.producerClose, st.producerReason = ch, producerClose, reason
	return nil
}

// ErrSuperseded is sent on the channel returned by Connect when a later Connect replaced that connection.
var ErrSuperseded = &amqp.Error{Code: amqp.ConnectionForced, Reason: "rabbitmq: connection superseded by a newer Connect"}

// watch reports on notify the first close of the connection or the consumer channel; a producer channel
// closed by the broker is reopened in place.
func (r *rabbit) watch(st *state, notify chan *amqp.Error) {
	defer close(notify)
	for {
		var amqpErr *amqp.Error
		producer := false
		select {
		case amqpErr = <-st.connClose:
		case amqpErr = <-st.consumerClose:
		case amqpErr = <-st.producerClose:
			producer = true
		}

		if producer && amqpErr != nil && !st.conn.IsClosed() {
			if next, ok := r.reopenProducer(st); ok {
				log.Printf("rabbitmq producer channel closed by the broker, reopened it: [%s]\n", amqpErr)
				st = next
				continue
			}
		}

		r.mu.Lock()
		current := r.st == st
		if current {
			r.st = nil
		}
		closed := r.closed
		r.mu.Unlock()
		switch {
		case closed:
			return // graceful: Close took this state out and closes it
		case !current:
			notify <- ErrSuperseded // a newer Connect took this state out and closes it
			return
		case amqpErr == nil:
			// Closed without error but not by us, e.g. it died before NotifyClose was registered.
			amqpErr = amqp.ErrClosed
		}
		st.close()
		notify <- amqpErr
		return
	}
}

// reopenProducer opens a new producer channel on the same connection and swaps in a state that keeps the
// consumer channel and its consumers. ok is false when the channel can't be opened or the state is no
// longer current, and then the caller handles it as any other close.
func (r *rabbit) reopenProducer(st *state) (next *state, ok bool) {
	// A copy carries every field that is not the producer channel's, which openProducer then replaces.
	copied := *st
	next = &copied
	if err := next.openProducer(); err != nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.st != st {
		_ = next.chProducer.Close()
		return nil, false
	}
	r.st = next
	return next, true
}

// Close closes the rabbitMQ connection. It waits the in-flight operations (consumers and publishes) to
// finish or the context to be done, whichever comes first. Publishing is still allowed while it waits, so
// handlers being drained can publish; after that the client is closed for good.
func (r *rabbit) Close(ctx context.Context) (done chan struct{}) {
	done = make(chan struct{})
	go func() {
		defer close(done)
		r.waitOrDone(ctx)

		r.mu.Lock()
		r.closed = true
		st := r.st
		r.st = nil
		r.mu.Unlock()

		// Operations that acquired the state between the first wait and the flag above.
		r.waitOrDone(ctx)
		if st != nil {
			st.close()
		}
	}()
	return
}

func (r *rabbit) waitOrDone(ctx context.Context) {
	r.mu.Lock()
	idle := r.idle
	r.mu.Unlock()
	select { // either waits for the messages to process or timeout from context
	case <-idle:
	case <-ctx.Done():
	}
}

// KeepConnectionAndSetup starts a goroutine to keep the connection open and everytime the connection is open, it will call the setupRabbit function. It is important to pass a context with cancel so the goroutine can be closed when the context is done. Otherwise it will run until the program ends or Close is called.
// The returned channel is closed when the goroutine exits. Run only one per client and don't call Connect on
// that client by hand: either one replaces the other's connection, and this loop then stops.
func KeepConnectionAndSetup(ctx context.Context, conn Connector, config ConfigConnection, setupRabbit RabbitSetup) <-chan struct{} {
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		backoff := reconnectBackoffMin
		for {
			if ctx.Err() != nil {
				return
			}
			notifyClose, err := conn.Connect(config)
			if err != nil {
				if errors.Is(err, ErrClientClosed) {
					return
				}
				log.Printf("error connecting to rabbitmq, retrying in %s: [%s]\n", backoff, err)
				if !sleepCtx(ctx, backoff) {
					return
				}
				backoff = min(backoff*2, reconnectBackoffMax)
				continue
			}
			connectedAt := time.Now()
			setupRabbit.Setup()
			notifySetupIsDone()
			select {
			case amqpErr := <-notifyClose:
				if amqpErr == nil {
					return // closed gracefully by Close
				}
				if amqpErr == ErrSuperseded {
					log.Println("rabbitmq connection replaced by another Connect, stopping this reconnection loop")
					return
				}
				// Only a connection that held up resets the backoff: a setup or publish that keeps getting the
				// channel closed right after connecting would otherwise reconnect in a tight loop.
				if time.Since(connectedAt) >= reconnectBackoffMax {
					backoff = reconnectBackoffMin
				}
				log.Printf("rabbitmq connection lost, reconnecting in %s: [%s]\n", backoff, amqpErr)
				if !sleepCtx(ctx, backoff) {
					return
				}
				backoff = min(backoff*2, reconnectBackoffMax)
			case <-ctx.Done():
				return
			}
		}
	}()
	return exited
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
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

// close closes the connection, and with it both channels: RPCs pending on them return amqp.ErrClosed instead
// of holding the close, and closeTimeout bounds the wait for the broker.
func (st *state) close() {
	if err := st.conn.CloseDeadline(time.Now().Add(closeTimeout)); err != nil && !errors.Is(err, amqp.ErrClosed) {
		log.Printf("Error closing connection: [%s]\n", err)
	}
}
