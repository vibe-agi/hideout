package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
)

func main() {
	os.Exit(run())
}

func run() int {
	command, args, err := cmdproxy.ResolveHostOpenInvocation(filepath.Base(os.Args[0]), os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-shim: %v\n", err)
		return 2
	}
	normalized, err := cmdproxy.NormalizeHostOpenCommand(command, args, mustGetwd())
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-shim: %v\n", err)
		return 2
	}
	endpoint, err := brokerEndpointFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hideout-shim: broker environment is missing")
		return 69
	}
	sessionID := os.Getenv(broker.EnvSession)
	token := os.Getenv(broker.EnvToken)
	if sessionID == "" || token == "" {
		fmt.Fprintln(os.Stderr, "hideout-shim: broker environment is missing")
		return 69
	}
	requestID, err := broker.NewRequestID()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hideout-shim: request id generation failed")
		return 69
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp := broker.ClientOpenEndpoint(ctx, endpoint, broker.Request{
		ID:              requestID,
		SessionID:       sessionID,
		CapabilityToken: token,
		Subject:         normalized.Subject,
		Command:         normalized.Command,
		Argv:            normalized.Argv,
		Route:           normalized.Route,
		Action:          normalized.Action,
		Args:            normalized.Payload,
	})
	if resp.Stdout != "" {
		fmt.Fprint(os.Stdout, resp.Stdout)
	}
	if resp.Stderr != "" {
		fmt.Fprintln(os.Stderr, resp.Stderr)
	}
	return resp.ExitCode
}

func brokerEndpointFromEnv() (broker.Endpoint, error) {
	if raw := os.Getenv(broker.EnvEndpoint); raw != "" {
		return broker.ParseEndpoint(raw)
	}
	if sock := os.Getenv(broker.EnvSock); sock != "" {
		return broker.UnixEndpoint(sock), nil
	}
	return broker.Endpoint{}, fmt.Errorf("broker endpoint is missing")
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}
