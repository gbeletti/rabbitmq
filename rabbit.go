package rabbitmq

import (
	"errors"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ErrClientClosed is returned by Connect after Close was called. Close is terminal: KeepConnectionAndSetup
// stops reconnecting when it gets this error.
var ErrClientClosed = errors.New("rabbitmq: client is closed")

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

// NewRabbitMQ creates the object to manage the operations to rabbitMQ
func NewRabbitMQ() RabbitMQ {
	idle := make(chan struct{})
	close(idle)
	return &rabbit{idle: idle}
}

// acquire returns the current state and registers one in-flight operation, so Close waits for it.
func (r *rabbit) acquire() (st *state, release func(), err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.st == nil {
		return nil, nil, amqp.ErrClosed
	}
	r.track()
	return r.st, r.untrack, nil
}

// current returns the current state without registering an operation. Used by the declarations, which are
// quick RPCs that Close does not need to wait for.
func (r *rabbit) current() (*state, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.st == nil {
		return nil, amqp.ErrClosed
	}
	return r.st, nil
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
