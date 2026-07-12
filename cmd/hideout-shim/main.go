package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/cmdgrammar"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
)

func main() {
	os.Exit(run())
}

func run() int {
	action, adapterID, invocationArgs, err := parseInvocationMetadata(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-shim: %v\n", err)
		return 2
	}
	command, args, err := cmdproxy.ResolveHostOpenInvocation(filepath.Base(os.Args[0]), invocationArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hideout-shim: %v\n", err)
		return 2
	}
	normalized, err := normalizeInvocationForAction(command, args, mustGetwd(), action, adapterID)
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
	ctx, cancel := context.WithTimeout(context.Background(), brokerRequestTimeout(normalized.Action))
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

func brokerRequestTimeout(action string) time.Duration {
	if action == cmdproxy.ActionHostAppOpenResource {
		return 20 * time.Second
	}
	return 5 * time.Second
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
	return normalizeInvocationForAction(command, args, cwd, os.Getenv("HIDEOUT_COMMAND_PROXY_ACTION"), os.Getenv("HIDEOUT_COMMAND_PROXY_ADAPTER_ID"))
}

func normalizeInvocationForAction(command string, args []string, cwd, action, adapterID string) (cmdproxy.Request, error) {
	switch action {
	case cmdproxy.ActionCommandAdapter:
		return cmdproxy.NormalizeAdapterCommand(command, args, cwd, adapterID)
	case cmdproxy.ActionHostAppOpenResource:
		grammar, digest, ok, err := projectionBindingFromEnv()
		if err != nil {
			return cmdproxy.Request{}, err
		}
		if !ok {
			return cmdproxy.Request{}, errors.New("host-app binding metadata is required")
		}
		return cmdproxy.NormalizeOpenResourceProjectionCommand(command, args, cwd, grammar, digest)
	case "", cmdproxy.ActionHostOpen:
		return cmdproxy.NormalizeHostOpenCommand(command, args, cwd)
	default:
		return cmdproxy.Request{}, fmt.Errorf("unsupported command proxy action %q", action)
	}
}

func projectionBindingFromEnv() (cmdgrammar.OpenResourceGrammar, string, bool, error) {
	digest := os.Getenv("HIDEOUT_COMMAND_PROXY_BINDING_DIGEST")
	rawB64 := os.Getenv("HIDEOUT_COMMAND_PROXY_GRAMMAR_B64")
	if digest == "" && rawB64 == "" {
		return cmdgrammar.OpenResourceGrammar{}, "", false, nil
	}
	if len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") || rawB64 == "" {
		return cmdgrammar.OpenResourceGrammar{}, "", false, errors.New("host-app binding metadata is incomplete")
	}
	raw, err := base64.StdEncoding.DecodeString(rawB64)
	if err != nil {
		return cmdgrammar.OpenResourceGrammar{}, "", false, errors.New("host-app grammar metadata is invalid")
	}
	var grammar cmdgrammar.OpenResourceGrammar
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&grammar); err != nil || cmdgrammar.ValidateOpenResourceGrammar(grammar) != nil {
		return cmdgrammar.OpenResourceGrammar{}, "", false, errors.New("host-app grammar metadata is invalid")
	}
	return grammar, digest, true, nil
}

// parseInvocationMetadata accepts only the fixed prefix emitted by Hideout's
// generated wrapper. It is routing metadata, not authority: Core still validates
// the resulting action and intent. Unknown or incomplete metadata fails closed.
func parseInvocationMetadata(args []string) (string, string, []string, error) {
	action := os.Getenv("HIDEOUT_COMMAND_PROXY_ACTION")
	adapterID := os.Getenv("HIDEOUT_COMMAND_PROXY_ADAPTER_ID")
	args = append([]string(nil), args...)
	for len(args) > 0 {
		switch args[0] {
		case "--action":
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return "", "", nil, errors.New("--action requires a value")
			}
			action = args[1]
			args = args[2:]
		case "--adapter-id":
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return "", "", nil, errors.New("--adapter-id requires a value")
			}
			adapterID = args[1]
			args = args[2:]
		default:
			return action, adapterID, args, nil
		}
	}
	return "", "", nil, errors.New("command proxy name is required")
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
