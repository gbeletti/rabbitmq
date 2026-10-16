package rabbitmq

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"strconv"
	"sync"
	"sync/atomic"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ReturnIDHeader is the header Publish adds to a message with ConfigConnection.PublisherConfirms on and
// ConfigPublish.Mandatory set. Its value is an opaque string, unique per message, that matches a returned
// message to its publish; consumers can ignore it.
const ReturnIDHeader = "x-rabbitmq-return-id"

// returnTracker tells a mandatory Publish whether the broker returned its message (basic.return), on a
// producer channel in confirm mode.
//
// A basic.return carries no delivery tag, and the message's own fields don't identify it: the CC and BCC
// headers add routing keys in any exchange type, so two messages equal in exchange, routing key, ids and
// body may route differently, and the broker strips BCC before returning. So each mandatory publish carries
// ReturnIDHeader, which the broker hands back in the return.
type returnTracker struct {
	idPrefix string
	lastID   atomic.Uint64

	mu      sync.Mutex
	pending map[string]*pendingReturn // by ReturnIDHeader; mandatory publishes not settled

	flush chan struct{} // see barrier
	done  chan struct{} // closed when the listener exits, i.e. the channel closed
}

type pendingReturn struct {
	id       string
	returned *amqp.Return
}

// watchReturns registers the returns listener of ch. It runs until ch closes, which closes the listener
// channel, so a reopened producer channel does not leave the previous listener behind.
func watchReturns(ch *amqp.Channel) *returnTracker {
	rt := &returnTracker{
		idPrefix: randomPrefix(),
		pending:  make(map[string]*pendingReturn),
		flush:    make(chan struct{}),
		done:     make(chan struct{}),
	}
	// Unbuffered so that a return the reader handed over has been matched once barrier gets through; it is
	// always drained, as amqp091 drops a return its listener does not take within 5 seconds.
	returns := ch.NotifyReturn(make(chan amqp.Return))
	go rt.listen(returns)
	return rt
}

// randomPrefix makes the ids unique across channels and processes, so a consumer that keys on
// ReturnIDHeader does not see the same value twice.
func randomPrefix() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // never fails on the supported platforms (crypto/rand doc)
	return hex.EncodeToString(b) + "-"
}

func (rt *returnTracker) listen(returns <-chan amqp.Return) {
	defer close(rt.done)
	for {
		select {
		case ret, ok := <-returns:
			if !ok {
				return
			}
			rt.match(ret)
		case <-rt.flush:
		}
	}
}

// barrier returns once every return handed to the listener so far is matched: the listener only takes the
// flush between two returns. amqp091 hands a message's return to the listener before it resolves the
// message's confirm, from the same reader goroutine.
func (rt *returnTracker) barrier() {
	select {
	case rt.flush <- struct{}{}:
	case <-rt.done:
	}
}

// add registers a mandatory publish and returns msg with ReturnIDHeader set, leaving the caller's headers
// untouched.
func (rt *returnTracker) add(msg amqp.Publishing) (*pendingReturn, amqp.Publishing) {
	p := &pendingReturn{id: rt.idPrefix + strconv.FormatUint(rt.lastID.Add(1), 10)}
	headers := make(amqp.Table, len(msg.Headers)+1)
	for k, v := range msg.Headers {
		headers[k] = v
	}
	headers[ReturnIDHeader] = p.id
	msg.Headers = headers

	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.pending[p.id] = p
	return p, msg
}

func (rt *returnTracker) match(ret amqp.Return) {
	id, ok := ret.Headers[ReturnIDHeader].(string)
	if !ok {
		log.Printf("rabbitmq message returned by the broker without the %s header, exchange %q routing key %q: [%d %s]\n",
			ReturnIDHeader, ret.Exchange, ret.RoutingKey, ret.ReplyCode, ret.ReplyText)
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if p, ok := rt.pending[id]; ok { // absent when that Publish already gave up: its ctx ended
		p.returned = &ret
	}
}

// settle removes p and reports its return, if any. After the confirm of p is done no return of p can come,
// so the report is final then; before it (ctx ended), a return still on its way is dropped.
func (rt *returnTracker) settle(p *pendingReturn) *amqp.Return {
	rt.barrier()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.pending, p.id)
	return p.returned
}
