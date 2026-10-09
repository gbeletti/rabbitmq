# RabbitMQ - a client for microservices

## Overview

This library aims to simplify the creation of a rabbitMQ package on a Go service. Hopefully it will make your life easier.

## Installation

To use this library you can simply import the package `github.com/gbeletti/rabbitmq` and run the command `go mod tidy` to download it or you can explicit run

`go get github.com/gbeletti/rabbitmq`

## Usage

You can see a package example [here](https://github.com/gbeletti/service-golang). The `main.go` starts the service and the package `queuerabbit` uses this lib.

### Connecting to rabbit

To get started import the package `github.com/gbeletti/rabbitmq`, call the function `rabbitmq.NewRabbitMQ()` and connect it to the rabbitMQ server.

```go
import (
    "context"
    "log"
    "os"

    "github.com/gbeletti/rabbitmq"
)

var rabbit rabbitmq.RabbitMQ
rabbit = rabbitmq.NewRabbitMQ()

ctx, cancel = context.WithCancel(context.Background())

configConn := rabbitmq.ConfigConnection{
    URI:           "amqp://guest:guest@localhost:5672?heartbeat=30&connection_timeout=120",
    PrefetchCount: 1,
}

var setup rabbitmq.Setup = func() {
    // creates and consumes from queues
}

rabbitmq.KeepConnectionAndSetup(ctx, rabbit, configConn, setup)

```

The function `KeepConnectionAndSetup` will create a goroutine to keep the connection open until the context is canceled or `Close` is called. It is important that the context is canceled on the shutdown of the service so it stops trying to keep the connection opened. It returns a channel that is closed when that goroutine exits.

It reconnects, and runs the setup again, when the connection drops. Failed attempts are retried with exponential backoff from 1s up to 30s.

Each `Consume` call has a channel of its own, the declarations (`CreateQueue`, `CreateExchange`, `BindQueueExchange`, `UnbindQueueExchange`) share another one and `Publish` a third, so a channel the broker closes with the connection still up does not take the others down:

- a consumer's channel (e.g. an invalid ack, or the consumer ack timeout): that `Consume` reopens it, with the same backoff, and goes on; the other consumers are not touched. Only when it can't reopen it (e.g. the queue is gone) the connection is torn down, so the setup runs again;
- the declarations' channel (e.g. `406 PRECONDITION_FAILED` for a queue redeclared with other arguments): the declaration returns the error and the next one opens a new channel;
- the producer channel (e.g. publishing to an exchange that does not exist): it is reopened in place.

A connection holds one channel per running `Consume` plus two, within the broker's `channel_max` (2047 by default).

### Shutting down gracefully

When the service is going down you must call the `Close` function to close all the connections gracefully.

```go
ctx, cancelTimeout := context.WithTimeout(context.Background(), time.Second*30)
defer cancel()
var done chan struct{}
done = rabbit.Close(ctx)
<-done
```

It will stop receiving new messages and wait processing all the messages received from queue and publishing message to exchange or it will timeout after a given time. Publishing is still allowed while it waits; after that the client is closed for good: `Connect` returns `ErrClientClosed`, the other operations return an error that matches both `ErrClientClosed` and `amqp.ErrClosed` (`errors.Is`), and a new client must be created with `NewRabbitMQ` to connect again.

### Creating queues

Just create the configuration struct and call the `CreateQueue` function.

```go
func createQueues(rabbit rabbitmq.QueueCreator) {
    config := rabbitmq.ConfigQueue{
        Name:       "test",
        Durable:    true,
        AutoDelete: false,
        Exclusive:  false,
        NoWait:     false,
        Args:       nil,
    }
    _, err := rabbit.CreateQueue(config)
    if err != nil {
        log.Printf("error creating queue: %s\n", err)
    }
}
```

### Consuming from exchange

First create the configuration

```go
config := rabbitmq.ConfigConsume{
    QueueName:         "test",
    Consumer:          "test",
    AutoAck:           false,
    Exclusive:         false,
    NoLocal:           false,
    NoWait:            false,
    Args:              nil,
    ExecuteConcurrent: true,
}
```

The option `ExecuteConcurrent` defines if the message received should run in a goroutine or not. At most
`MaxConcurrent` handlers run at once; when it is not set, the limit is the connection `PrefetchCount`, which needs
`AutoAck` off since the broker ignores the prefetch for it. Without either bound `Consume` returns
`ErrUnboundedConcurrency` instead of spawning one goroutine per message. `config.Validate(configConn)` returns
the same error, to fail at boot. The limit is per `Consume` call: it counts the handlers still running from a
channel that call reopened, but not those from before a reconnection, which a new call does not see. With `AutoAck`, `MaxConcurrent` bounds the handlers
but not the deliveries waiting for one, which amqp buffers in memory.

Then create the function to be executed upon getting a new message.

```go
func receiveMessage(d *amqp.Delivery) {
    defer func() {
        if err := d.Ack(false); err != nil {
            log.Printf("error acking message: %s\n", err)
        }
    }()
    log.Printf("received message: %s\n", d.Body)
}
```

Every message sent to `test` queue will execute the `receiveMessage` function.

Finally run the `Consume` function in a goroutine

```go
go func() {
    if err := rabbit.Consume(ctx, config, receiveMessage); err != nil {
        log.Printf("error consuming from queue: %s\n", err)
    }
}()
```

It is important that the context has cancel, so when it is canceled it will stop consuming messages from queue. You can share the same context used in the connection.

### Publishing with confirms

By default `Publish` returns as soon as the message is written to the socket: a message the broker rejects, e.g. sent to an exchange that does not exist, is lost and `Publish` still returns `nil`. Set `PublisherConfirms` in `ConfigConnection` to put the producer channel in [confirm mode](https://www.rabbitmq.com/docs/confirms#publisher-confirms):

```go
configConn := rabbitmq.ConfigConnection{
    URI:               "amqp://guest:guest@localhost:5672",
    PublisherConfirms: true,
}
```

Then `Publish` waits for the broker to confirm each message. Each `Publish` takes one more round trip to the broker; concurrent publishes still share the channel and wait in parallel. The errors tell the caller what happened:

- an error that does not match `rabbitmq.ErrNotConfirmed` (e.g. `amqp.ErrClosed`): the message was not sent, so retrying can't duplicate it;
- `rabbitmq.ErrNotConfirmed`: the message was sent and the outcome is unknown. The broker nacked it, the producer channel closed before the confirm, or the context ended while waiting. It may still have been enqueued, so a retry can duplicate it and consumers must be idempotent.

When the channel closed, the broker's `*amqp.Error` is in the chain, e.g. `404 NOT_FOUND`. It says why the **channel** closed, which may have been another publish running concurrently, so don't treat it as a permanent failure of this message.

Two limits:

- A confirm means the broker took the message, not that a queue got it. An existing exchange with no binding matching the routing key acks and drops the message, and `Mandatory` doesn't turn that into an error, because the returned message is not listened to.
- The context bounds the wait for the confirm, not the write to the socket. When the broker stops reading from publishers (memory or disk alarm) and the socket buffer fills up, `Publish` blocks past the context, as it does without confirms.

## Reference

This library uses [rabbitmq/amqp091-go](https://github.com/rabbitmq/amqp091-go). To better understand the options for the queues and exchanges I suggest their documentation.
