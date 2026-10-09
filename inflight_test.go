package rabbitmq

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// Operations going from 0 to 1 while Close waits was a sync.WaitGroup misuse that panics.
func TestInflightAcquireConcurrentWithWait(t *testing.T) {
	r := NewRabbitMQ().(*rabbit)
	r.st = &state{}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, release, err := r.acquire()
				if err != nil {
					t.Errorf("acquire: %s", err)
					return
				}
				release()
			}
		}()
	}
	// The acquirers overlap, so the counter rarely reaches zero: bound each wait, as Close does with its ctx.
	for i := 0; i < 20_000; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Microsecond)
		r.waitOrDone(ctx)
		cancel()
	}
	close(stop)
	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.waitOrDone(ctx)
	if ctx.Err() != nil || r.inflight != 0 {
		t.Fatalf("expected idle with no operation in flight, got %d in flight", r.inflight)
	}
}

func TestAcquireAfterClose(t *testing.T) {
	r := NewRabbitMQ().(*rabbit)
	if _, _, err := r.acquire(); !errors.Is(err, amqp.ErrClosed) || errors.Is(err, ErrClientClosed) {
		t.Fatalf("not connected yet: expected only amqp.ErrClosed, got %v", err)
	}
	r.closed = true
	if _, _, err := r.acquire(); !errors.Is(err, ErrClientClosed) || !errors.Is(err, amqp.ErrClosed) {
		t.Fatalf("closed: expected ErrClientClosed and amqp.ErrClosed, got %v", err)
	}
	if _, err := r.Connect(ConfigConnection{}); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("Connect after Close: expected ErrClientClosed, got %v", err)
	}
}
