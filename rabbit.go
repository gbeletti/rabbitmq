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
	// mu guards st and closed. st is replaced as a whole on every (re)connection, so an operation that
	// grabbed it keeps using a consistent connection/channels set even if a reconnection happens meanwhile.
	mu     sync.RWMutex
	st     *state
	closed bool
	wg     *sync.WaitGroup
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
	return &rabbit{
		wg: &sync.WaitGroup{},
	}
}

// acquire returns the current state and registers one operation on the wait group, so Close waits for it.
// The Add happens under the read lock and Close flips closed under the write lock before its last Wait, so
// no Add can race with that Wait.
func (r *rabbit) acquire() (st *state, release func(), err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || r.st == nil {
		return nil, nil, amqp.ErrClosed
	}
	r.wg.Add(1)
	return r.st, r.wg.Done, nil
}

// current returns the current state without registering an operation. Used by the declarations, which are
// quick RPCs that Close does not need to wait for.
func (r *rabbit) current() (*state, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed || r.st == nil {
		return nil, amqp.ErrClosed
	}
	return r.st, nil
}
