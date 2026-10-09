package rabbitmq

import (
	"errors"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrClientClosed is returned after Close was called. Close is terminal: KeepConnectionAndSetup stops
// reconnecting when it gets this error, and a new client must be created with NewRabbitMQ.
var ErrClientClosed = errors.New("rabbitmq: client is closed")

// errOpClosed is what operations return once the client is closed. It matches both ErrClientClosed and
// amqp.ErrClosed, so callers that only knew amqp.ErrClosed keep working and new ones can tell "closed for
// good" from "reconnecting", which returns amqp.ErrClosed alone.
var errOpClosed = fmt.Errorf("%w: %w", ErrClientClosed, amqp.ErrClosed)

// ErrUnboundedConcurrency is returned by Consume and ConfigConsume.Validate when ExecuteConcurrent is set but
// nothing limits the handlers running: no MaxConcurrent, and either the connection has no PrefetchCount or
// AutoAck is on (the broker ignores the prefetch for it). The returned error wraps it with the reason.
var ErrUnboundedConcurrency = errors.New("rabbitmq: ExecuteConcurrent needs MaxConcurrent, or PrefetchCount > 0 with AutoAck off")

type rabbit struct {
	// mu guards st, closed and the in-flight counter. st is replaced as a whole on every (re)connection, so
	// an operation that grabbed it keeps using a consistent connection/channels set.
	mu     sync.Mutex
	st     *state
	closed bool

	// inflight counts consumers, handlers and publishes running; idle is closed whenever it is zero. Unlike a
	// sync.WaitGroup it may go from 0 to 1 while Close waits, which happens when handlers being drained publish.
	inflight int
	idle     chan struct{}
}

// state is everything that belongs to one connection. It is never mutated after Connect publishes it.
type state struct {
	conn       *amqp.Connection
	chProducer *amqp.Channel

	// declarer runs the declarations on a channel of their own; a pointer, shared by the copies
	// reopenProducer makes.
	declarer *declarer

	// prefetchCount is the Qos set on the channel of each consumer; it caps the goroutines of a concurrent one.
	prefetchCount int

	connClose, producerClose chan *amqp.Error

	// failed takes the error of a consumer that lost its channel and could not open another, for watch to tear
	// the connection down; see consumerFailed. The copies reopenProducer makes share it.
	failed chan *amqp.Error

	// confirms is ConfigConnection.PublisherConfirms. producerReason is only set when it is on.
	confirms       bool
	producerReason *closeReason
}

// closeReason records why a channel closed, so Publish can tell the caller what the broker said.
type closeReason struct {
	done chan struct{}
	err  *amqp.Error // nil on a graceful close; read only after done is closed
}

func watchClose(ch *amqp.Channel) *closeReason {
	reason := &closeReason{done: make(chan struct{})}
	notify := ch.NotifyClose(make(chan *amqp.Error, 1))
	go func() {
		reason.err = <-notify
		close(reason.done)
	}()
	return reason
}

// declarer owns the channel the declarations run on, apart from the consumers' and the producer's: the broker
// answers an invalid declaration (e.g. 406 for a queue redeclared with other arguments) by closing the channel
// it came on, and here that channel only holds declarations. It is reopened by the next declaration, so the
// failure reaches only the call that caused it.
type declarer struct {
	// mu serializes the declarations and guards ch. amqp091-go sends an RPC and then waits for the reply
	// without a lock, so two concurrent RPCs on a channel take each other's reply and fail with
	// ErrCommandInvalid, or silently get the wrong one when both are alike.
	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
}

// call runs fn on the declarations channel, opening it first when it is not open yet or the broker closed it.
func (d *declarer) call(fn func(ch *amqp.Channel) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ch == nil || d.ch.IsClosed() {
		ch, err := d.conn.Channel()
		if err != nil {
			return err
		}
		d.ch = ch
	}
	return fn(d.ch)
}

// consumerFailed hands watch the error of a consumer that lost its channel and could not open another, so the
// connection is torn down and the setup runs again, which restarts the consumer. One is enough to do that, so
// it does not wait when another is already pending.
func (st *state) consumerFailed(err error) {
	var amqpErr *amqp.Error
	if !errors.As(err, &amqpErr) {
		amqpErr = &amqp.Error{Code: amqp.ChannelError, Reason: "rabbitmq: reopening a consumer channel: " + err.Error()}
	}
	select {
	case st.failed <- amqpErr:
	default:
	}
}

// NewRabbitMQ creates the object to manage the operations to rabbitMQ. After Close it can't be connected
// again: create another one.
func NewRabbitMQ() RabbitMQ {
	idle := make(chan struct{})
	close(idle)
	return &rabbit{idle: idle}
}

// acquire returns the current state and registers one in-flight operation, so Close waits for it.
func (r *rabbit) acquire() (st *state, release func(), err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err = r.unavailable(); err != nil {
		return nil, nil, err
	}
	r.track()
	return r.st, r.untrack, nil
}

// current returns the current state without registering an operation. Used by the declarations, which are
// quick RPCs that Close does not need to wait for.
func (r *rabbit) current() (*state, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.unavailable(); err != nil {
		return nil, err
	}
	return r.st, nil
}

// holds reports whether st's connection is still the client's and open, so a consumer may open a channel on
// it again.
func (r *rabbit) holds(st *state) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.closed && r.st != nil && r.st.conn == st.conn && !st.conn.IsClosed()
}

// unavailable must be called with r.mu held.
func (r *rabbit) unavailable() error {
	if r.closed {
		return errOpClosed
	}
	if r.st == nil {
		return amqp.ErrClosed
	}
	return nil
}

// trackHandler registers the handler of a delivery. Its consumer is already in flight, so it skips the
// closed check that acquire does.
func (r *rabbit) trackHandler() (release func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.track()
	return r.untrack
}

// track must be called with r.mu held.
func (r *rabbit) track() {
	if r.inflight == 0 {
		r.idle = make(chan struct{})
	}
	r.inflight++
}

func (r *rabbit) untrack() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inflight--
	if r.inflight == 0 {
		close(r.idle)
	}
}
