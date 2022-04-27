# RabbitMQ - a client for microservices

## Overview

This library aims to simplify the creation of a rabbitMQ package on a Go service. Hopefully you can create your queue package in your service in an easier way.

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

The function `KeepConnectionAndSetup` will create a goroutine to keep the connection open until the context is canceled. It is important that the context is canceled on the shutdown of the service so it stops trying to keep the connection opened.

### Shutting down gracefully

When the service is going down you must call the `Close` function to close all the connections gracefully.

```go
ctx, cancelTimeout := context.WithTimeout(context.Background(), time.Second*30)
defer cancel()
var done chan struct{}
done = rabbit.Close(ctx)
<-done
```

It will stop receiving new messages and wait processing all the messages received from queue and publishing message to exchange or it will timeout after a given time.

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

The option `ExecuteConcurrent` defines if the message received should run in a goroutine or not.

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

It is important that the context has cancel, so when it is canceled it will stop consuming messages from queue. You can share the same context used on the connection.

## Reference

This library uses [rabbitmq/amqp091-go](https://github.com/rabbitmq/amqp091-go). To better understand the options for the queues and exchanges I suggest their documentation.
