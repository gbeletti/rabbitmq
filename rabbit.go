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
	chConsumer *amqp.Channel
	chProducer *amqp.Channel

	connClose, producerClose, consumerClose chan *amqp.Error
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
