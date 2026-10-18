//go:build integration

package rabbitmq_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

var waitFlag = flag.Bool("wait", false, "wait after test is done")

// shared is the broker of the tests that leave it as they found it, started by the first one that asks for it.
var shared struct {
	once      sync.Once
	container testcontainers.Container
	uri       string
	uiURL     string
	err       error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if shared.container != nil {
		if err := shared.container.Terminate(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "failed to terminate the shared rabbitmq container: %s\n", err)
		}
	}
	os.Exit(code)
}

// sharedRabbit returns the broker shared by the tests that do not stop it, close its connections or change its
// settings. Queues and exchanges on it outlive the test, so name them with uniqueName.
func sharedRabbit(t *testing.T) (uri, uiURL string) {
	t.Helper()
	shared.once.Do(func() {
		shared.container, shared.uri, shared.uiURL, shared.err = runRabbitContainer(context.Background())
	})
	if shared.err != nil {
		t.Fatalf("failed to start the shared rabbitmq container: %s", shared.err)
	}
	return shared.uri, shared.uiURL
}

// startRabbitContainer starts a broker for this test alone, for the tests that stop it, close its connections
// or change its settings.
func startRabbitContainer(t *testing.T) (container testcontainers.Container, uri, uiURL string) {
	t.Helper()
	container, uri, uiURL, err := runRabbitContainer(context.Background())
	t.Cleanup(func() {
		if container == nil {
			return
		}
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("failed to terminate rabbitmq container: %s", err)
		}
	})
	if err != nil {
		t.Fatalf("failed to start rabbitmq container: %s", err)
	}
	t.Logf("rabbitmq uri: %s", uri)
	t.Logf("rabbitmq ui url: %s", uiURL)
	return
}

func runRabbitContainer(ctx context.Context) (container testcontainers.Container, uri, uiURL string, err error) {
	req := testcontainers.ContainerRequest{
		Image:        "rabbitmq:3-management-alpine",
		ExposedPorts: []string{"5672/tcp", "15672/tcp"},
		// The management UI answers before the AMQP listener accepts connections, which shows when several
		// containers start at once: wait for the broker to say it is up too.
		WaitingFor: wait.ForAll(
			wait.ForHTTP("/").WithPort("15672"),
			wait.ForLog("Server startup complete"),
			wait.ForListeningPort("5672/tcp"),
		),
		Env: map[string]string{
			"RABBITMQ_DEFAULT_USER": "guest",
			"RABBITMQ_DEFAULT_PASS": "guest",
		},
	}
	container, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("creating container: %w", err)
	}
	ip, err := container.Host(ctx)
	if err != nil {
		return container, "", "", fmt.Errorf("getting container host: %w", err)
	}
	mappedPort, err := container.MappedPort(ctx, "5672")
	if err != nil {
		return container, "", "", fmt.Errorf("getting mapped amqp port: %w", err)
	}
	mappedPortUI, err := container.MappedPort(ctx, "15672")
	if err != nil {
		return container, "", "", fmt.Errorf("getting mapped management port: %w", err)
	}
	uri = fmt.Sprintf("amqp://guest:guest@%s:%s?heartbeat=30&connection_timeout=120", ip, mappedPort.Port())
	uiURL = fmt.Sprintf("http://%s:%s", ip, mappedPortUI.Port())
	return container, uri, uiURL, nil
}

var nameSeq atomic.Int64

// uniqueName suffixes base so a queue or exchange on the shared broker is not reused by another test, nor by
// the same test on the next run of -count.
func uniqueName(base string) string {
	return fmt.Sprintf("%s-%d", base, nameSeq.Add(1))
}
