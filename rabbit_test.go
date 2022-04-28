package rabbitmq_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupRabbitContainer(t *testing.T) (uri string) {
	ctx := context.Background()
	req := testcontainers.ContainerRequest{
		Image:        "rabbitmq:3-management-alpine",
		ExposedPorts: []string{"5672/tcp", "15672/tcp"},
		WaitingFor:   wait.ForHTTP("/").WithPort("15672"),
		Env: map[string]string{
			"RABBITMQ_DEFAULT_USER": "guest",
			"RABBITMQ_DEFAULT_PASS": "guest",
		},
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to create rabbitmq container: %s", err)
		return
	}

	ip, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get rabbitmq container host: %s", err)
		return
	}

	mappedPort, err := container.MappedPort(ctx, "5672")
	if err != nil {
		t.Fatalf("failed to get rabbitmq container mapped port: %s", err)
		return
	}
	t.Cleanup(func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("failed to terminate rabbitmq container: %s", err)
		}
	})

	uri = fmt.Sprintf("amqp://guest:guest@%s:%s?heartbeat=30&connection_timeout=120", ip, mappedPort.Port())
	os.Setenv("RABBITMQ_URI", uri)
	t.Logf("rabbitmq uri: %s", uri)
	return
}

// func TestContainer(t *testing.T) {
// 	t.Log("setup rabbitmq container")
// 	setupRabbitContainer(t)
// 	t.Log("setup rabbitmq container done")
// 	time.Sleep(time.Second * 60)
// }
