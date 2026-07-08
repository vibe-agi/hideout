package broker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/cmdadapter"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/privilege"
	"github.com/vibe-agi/hideout/internal/profile"
)

type brokerPackResolver struct{}

func (brokerPackResolver) ResolveCommandAdapter(profileDir, id string, adapter profile.CommandAdapter) (cmdadapter.ResolvedSource, error) {
	return cmdadapter.ResolvedSource{
		Source: `function decideCommandAdapter() {
			return {outcome: "deny", reason: "pack blocked", exitCode: 73, stderr: "pack blocked"};
		}`,
		Digest: "sha256:e1e08121184db26dbaae5fde71fd2a97b561ef87151f7f50d72c5b37a586f7cd",
		Path:   filepath.Join(profileDir, "adapter-pack", id+".js"),
	}, nil
}

func TestPackBackedCommandAdapterRoutesThroughBroker(t *testing.T) {
	p := profile.Default("pack-test")
	p.CommandAdapters.Adapters = map[string]profile.CommandAdapter{
		"pack-tool": {
			Enabled:        true,
			PackID:         "example.pack",
			PackRevisionID: "rev_abc",
			PackAdapterID:  "tool",
			PackLockDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Entrypoint:     cmdadapter.DefaultEntrypoint,
			Commands:       []string{"tool-x"},
		},
	}
	rt, err := cmdadapter.CompileWithResolver(p, t.TempDir(), brokerPackResolver{})
	if err != nil {
		t.Fatalf("compile pack-backed runtime: %v", err)
	}
	server := &Server{
		SessionID:       "session",
		Token:           "token",
		Profile:         "pack-test",
		GuestRoot:       "/workspace",
		HostRoot:        "/tmp/workspace",
		Commands:        []string{"tool-x"},
		CommandAdapters: rt,
		Evaluator:       policy.NewEvaluator(p),
	}
	req := testAdapterRequest()
	req.Args["adapterId"] = "pack-tool"
	resp := server.Handle(context.Background(), req)
	if resp.Status != "denied" || resp.ExitCode != 73 || !strings.Contains(resp.Stderr, "pack blocked") {
		t.Fatalf("pack-backed adapter did not route through broker: %+v", resp)
	}
}

func TestCommandAdapterBrokerOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		allowed    []string
		wantStatus string
		wantCode   int
	}{
		{
			name: "deny",
			source: `function decideCommandAdapter() {
				return {outcome: "deny", reason: "no", exitCode: 77, stderr: "blocked"};
			}`,
			wantStatus: "denied",
			wantCode:   77,
		},
		{
			name: "simulate",
			source: `function decideCommandAdapter() {
				return {outcome: "simulate", reason: "fake", exitCode: 0, stdout: "tool-x 1.0\n"};
			}`,
			wantStatus: "simulated",
			wantCode:   0,
		},
		{
			name: "rewrite",
			source: `function decideCommandAdapter() {
				return {outcome: "rewriteGuest", reason: "rewrite", argv: ["tool-real", "--version"]};
			}`,
			wantStatus: "rewrite-guest",
			wantCode:   0,
		},
		{
			name: "proposal",
			source: `function decideCommandAdapter() {
				return {outcome: "proposeCapability", reason: "install", capability: "guest.privilege.plan", intent: {category: "package-manager"}};
			}`,
			allowed:    []string{cmdadapter.CapabilityGuestPrivPlan},
			wantStatus: "proposal-unavailable",
			wantCode:   126,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := testAdapterServer(tt.source, tt.allowed)
			resp := server.Handle(context.Background(), testAdapterRequest())
			if resp.Status != tt.wantStatus || resp.ExitCode != tt.wantCode {
				t.Fatalf("status/code = %s/%d, want %s/%d (stderr=%q)", resp.Status, resp.ExitCode, tt.wantStatus, tt.wantCode, resp.Stderr)
			}
		})
	}
}

func TestCommandAdapterBrokerFailsClosedOnInvalidOutput(t *testing.T) {
	server := testAdapterServer(`function decideCommandAdapter() {
		return {outcome: "launchMissiles", reason: "bad"};
	}`, nil)
	resp := server.Handle(context.Background(), testAdapterRequest())
	if resp.Status != "denied" || resp.ExitCode != 126 {
		t.Fatalf("invalid adapter output should deny, got %+v", resp)
	}
	if !strings.Contains(resp.Stderr, "unsupported") {
		t.Fatalf("expected strict validation error, got %q", resp.Stderr)
	}
}

func TestRootSensitiveAdapterEmitsTargetRootAttempt(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	aw, err := audit.NewFile(auditPath)
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	t.Cleanup(func() { _ = aw.Close() })
	p := profile.Default("test")
	adapter := cmdadapter.RuntimeAdapter{
		ID:            "root-sensitive",
		Digest:        "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Entrypoint:    cmdadapter.DefaultEntrypoint,
		Commands:      []string{"sudo"},
		RootSensitive: true,
		Source: `function decideCommandAdapter(ctx) {
			return {
				outcome: "deny",
				reason: "blocked",
				exitCode: 126,
				intent: {category: "escalation", separationStatus: "wrong"}
			};
		}`,
	}
	server := &Server{
		SessionID: "session",
		Token:     "token",
		Profile:   "test",
		Backend:   "lima",
		GuestRoot: "/workspace",
		HostRoot:  "/tmp/workspace",
		Commands:  []string{"sudo"},
		CommandAdapters: cmdadapter.Runtime{
			Adapters:  map[string]cmdadapter.RuntimeAdapter{"root-sensitive": adapter},
			ByCommand: map[string]string{"sudo": "root-sensitive"},
		},
		Evaluator: policy.NewEvaluator(p),
		Audit:     aw,
	}
	server.SetGuestPrivilegeStatus(privilege.Status{
		Status: privilege.StatusDegraded,
		Reason: "target user can run passwordless sudo",
	})
	req := testAdapterRequest()
	req.Subject = "command:sudo"
	req.Command = "sudo"
	req.Argv = []string{"sudo", "whoami"}
	req.Args["adapterId"] = "root-sensitive"
	resp := server.Handle(context.Background(), req)
	if resp.Status != "denied" || resp.ExitCode != 126 {
		t.Fatalf("root-sensitive request should deny, got %+v", resp)
	}
	if err := aw.Close(); err != nil {
		t.Fatalf("close audit: %v", err)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`"action":"broker.request"`,
		`"action":"target.root_attempt"`,
		`"separationStatus":"degraded"`,
		`"nonClaim":"Hideout does not claim guest-root containment for this run because privilege separation is degraded."`,
		`"command":"sudo"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("root-sensitive audit missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, `"separationStatus":"wrong"`) {
		t.Fatalf("Go-owned status must override adapter-provided status:\n%s", text)
	}
}

func testAdapterServer(source string, allowed []string) *Server {
	p := profile.Default("test")
	adapter := cmdadapter.RuntimeAdapter{
		ID:                          "adapter",
		Digest:                      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Entrypoint:                  cmdadapter.DefaultEntrypoint,
		Commands:                    []string{"tool-x"},
		AllowedProposalCapabilities: allowed,
		Source:                      source,
	}
	return &Server{
		SessionID: "session",
		Token:     "token",
		Profile:   "test",
		GuestRoot: "/workspace",
		HostRoot:  "/tmp/workspace",
		Commands:  []string{"tool-x"},
		CommandAdapters: cmdadapter.Runtime{
			Adapters:  map[string]cmdadapter.RuntimeAdapter{"adapter": adapter},
			ByCommand: map[string]string{"tool-x": "adapter"},
		},
		Evaluator: policy.NewEvaluator(p),
	}
}

func testAdapterRequest() Request {
	return Request{
		ID:              "req",
		SessionID:       "session",
		CapabilityToken: "token",
		Subject:         "command:tool-x",
		Command:         "tool-x",
		Argv:            []string{"tool-x", "--version"},
		Route:           cmdproxy.RouteCommandAdapter,
		Action:          cmdproxy.ActionCommandAdapter,
		Args:            map[string]any{"cwd": "/workspace", "adapterId": "adapter"},
	}
}
