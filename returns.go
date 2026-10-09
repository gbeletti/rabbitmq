package rabbitmq

import (
	"hash/maphash"
	"log"
	"slices"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

// returnTracker tells a mandatory Publish whether the broker returned its message (basic.return), on a
// producer channel in confirm mode.
//
// A basic.return carries no delivery tag, so it is matched to its publish by two facts of the protocol:
//   - the broker routes a channel's messages in the order they were sent, so the returns come in that
//     order too, and the return of a message comes before its ack;
//   - amqp091 dispatches both from its single reader goroutine, so the return of a message is handed to the
//     listener before the DeferredConfirmation of that message is resolved.
//
// Order alone is not enough: a routable message whose ack is slow (persisted to disk, quorum queue) is still
// pending when the return of a later message arrives, so "the oldest pending" would blame the wrong publish
// and let the unroutable one through. MessageId alone neither: it is optional and empty by default. So a
// return goes to the OLDEST pending publish whose exchange, routing key, MessageId, CorrelationId and body
// match it. Two in-flight publishes equal in all of these route the same way, unless the bindings change
// between them or a headers exchange routes them by headers, which are not compared (their Go types change
// on the wire round trip, e.g. int to int32); only then may they swap results.
type returnTracker struct {
	// sendMu is held while a mandatory publish is registered and sent, so pending is in send order.
	sendMu sync.Mutex

	mu      sync.Mutex
	pending []*pendingReturn // mandatory publishes sent and not settled, in send order

	seed  maphash.Seed
	flush chan struct{} // see barrier
	done  chan struct{} // closed when the listener exits, i.e. the channel closed
}

type pendingReturn struct {
	exchange, routingKey     string
	messageID, correlationID string
	bodyLen                  int
	bodyHash                 uint64 // the body is hashed, not kept, so the caller may reuse it once Publish returns
	returned                 *amqp.Return
}

// watchReturns registers the returns listener of ch. It runs until ch closes, which closes the listener
// channel, so a reopened producer channel does not leave the previous listener behind.
func watchReturns(ch *amqp.Channel) *returnTracker {
	rt := &returnTracker{seed: maphash.MakeSeed(), flush: make(chan struct{}), done: make(chan struct{})}
	// Unbuffered so that a return the reader handed over has been matched once barrier gets through; it is
	// always drained, as amqp091 drops a return its listener does not take within 5 seconds.
	returns := ch.NotifyReturn(make(chan amqp.Return))
	go rt.listen(returns)
	return rt
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
// flush between two returns.
func (rt *returnTracker) barrier() {
	select {
	case rt.flush <- struct{}{}:
	case <-rt.done:
	}
}

func (rt *returnTracker) newPending(exchange, routingKey string, msg amqp.Publishing) *pendingReturn {
	return &pendingReturn{
		exchange:      exchange,
		routingKey:    routingKey,
		messageID:     msg.MessageId,
		correlationID: msg.CorrelationId,
		bodyLen:       len(msg.Body),
		bodyHash:      maphash.Bytes(rt.seed, msg.Body),
	}
}

// add must be called with sendMu held, right before the send.
func (rt *returnTracker) add(p *pendingReturn) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.pending = append(rt.pending, p)
}

func (rt *returnTracker) match(ret amqp.Return) {
	bodyHash := maphash.Bytes(rt.seed, ret.Body)
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for _, p := range rt.pending {
		if p.returned == nil && p.exchange == ret.Exchange && p.routingKey == ret.RoutingKey &&
			p.messageID == ret.MessageId && p.correlationID == ret.CorrelationId &&
			p.bodyLen == len(ret.Body) && p.bodyHash == bodyHash {
			p.returned = &ret
			return
		}
	}
	log.Printf("rabbitmq message returned by the broker matches no pending publish, exchange %q routing key %q: [%d %s]\n",
		ret.Exchange, ret.RoutingKey, ret.ReplyCode, ret.ReplyText)
}

// settle removes p and reports its return, if any. Call it only once the confirm of p is done (or p was not
// sent): no return of p can come after that, and the barrier waits for the one already handed over.
func (rt *returnTracker) settle(p *pendingReturn) *amqp.Return {
	rt.barrier()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.pending = slices.DeleteFunc(rt.pending, func(q *pendingReturn) bool { return q == p })
	return p.returned
}
