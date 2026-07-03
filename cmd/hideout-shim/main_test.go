package main

import (
	"os"
	"testing"

	"github.com/vibe-agi/hideout/internal/broker"
)

func TestWrapperModeRejectsMissingBrokerEnv(t *testing.T) {
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"hideout-shim", "open", "https://example.com"}
	t.Setenv("HIDEOUT_BROKER_SOCK", "")
	if code := run(); code != 69 {
		t.Fatalf("expected broker env failure exit 69, got %d", code)
	}
}

func TestBrokerEndpointFromEnvPrefersEndpoint(t *testing.T) {
	t.Setenv(broker.EnvEndpoint, "tcp://127.0.0.1:1234")
	t.Setenv(broker.EnvSock, "/tmp/ignored.sock")
	got, err := brokerEndpointFromEnv()
	if err != nil {
		t.Fatalf("brokerEndpointFromEnv: %v", err)
	}
	if got.Network != broker.EndpointTCP || got.Address != "127.0.0.1:1234" {
		t.Fatalf("endpoint=%+v", got)
	}
}

func TestBrokerEndpointFromEnvFallsBackToSock(t *testing.T) {
	t.Setenv(broker.EnvEndpoint, "")
	t.Setenv(broker.EnvSock, "/tmp/hideout.sock")
	got, err := brokerEndpointFromEnv()
	if err != nil {
		t.Fatalf("brokerEndpointFromEnv: %v", err)
	}
	if got.Network != broker.EndpointUnix || got.Address != "/tmp/hideout.sock" {
		t.Fatalf("endpoint=%+v", got)
	}
}
