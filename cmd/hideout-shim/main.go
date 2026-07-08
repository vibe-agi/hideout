package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	normalized, err := normalizeInvocation(command, args, mustGetwd())
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
	if resp.Status == "rewrite-guest" {
		return runRewrite(resp)
	}
	if resp.Stdout != "" {
		fmt.Fprint(os.Stdout, resp.Stdout)
	}
	if resp.Stderr != "" {
		fmt.Fprintln(os.Stderr, resp.Stderr)
	}
	return resp.ExitCode
}

func runRewrite(resp broker.Response) int {
	argv, ok := rewriteArgv(resp.Data["rewriteArgv"])
	if !ok || len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "hideout-shim: invalid rewrite response")
		return 69
	}
	path, err := rewriteCommandPath(argv[0], os.Getenv("HIDEOUT_COMMAND_PROXY_SHIM_DIR"), os.Getenv("PATH"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-shim: rewrite failed: %v\n", err)
		return 127
	}
	cmd := exec.Command(path, argv[1:]...)
	if cwd, ok := resp.Data["rewriteCwd"].(string); ok && cwd != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(), "HIDEOUT_COMMAND_PROXY_ACTIVE=1")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "hideout-shim: rewrite failed: %v\n", err)
		return 127
	}
	return 0
}

func rewriteCommandPath(name, shimDir, pathEnv string) (string, error) {
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return "", errors.New("rewrite command must be a simple command name")
	}
	shimDir = filepath.Clean(shimDir)
	for _, dir := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		cleanDir := filepath.Clean(dir)
		if shimDir != "." && cleanDir == shimDir {
			continue
		}
		candidate := filepath.Join(dir, name)
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
			continue
		}
		return candidate, nil
	}
	return "", fmt.Errorf("real guest command %q not found outside shim directory", name)
}

func rewriteArgv(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}

func normalizeInvocation(command string, args []string, cwd string) (cmdproxy.Request, error) {
	switch os.Getenv("HIDEOUT_COMMAND_PROXY_ACTION") {
	case cmdproxy.ActionCommandAdapter:
		return cmdproxy.NormalizeAdapterCommand(command, args, cwd, os.Getenv("HIDEOUT_COMMAND_PROXY_ADAPTER_ID"))
	default:
		return cmdproxy.NormalizeHostOpenCommand(command, args, cwd)
	}
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
