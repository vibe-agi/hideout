package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestValidateRejectsGenericHostExec(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	_, err := e.Validate(Proposal{
		Decision:  Allow,
		Route:     HostBroker,
		Action:    "host.exec",
		Resources: []string{"guest-command:shell"},
		Reason:    "bad",
	})
	if err == nil {
		t.Fatal("expected host.exec to be rejected")
	}
}

func TestEvaluateOpenKeepsLocalhostDeniedForPreviewBoundary(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	proposal, err := e.EvaluateOpen("http://127.0.0.1:5173")
	if err != nil {
		t.Fatalf("EvaluateOpen: %v", err)
	}
	if proposal.Decision != Deny || proposal.Route != DenyRoute || proposal.Action != ActionHostOpen {
		t.Fatalf("localhost host.open should stay denied, got %+v", proposal)
	}
}

func TestValidateRejectsProbeActionsOnProductPath(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	_, err := e.Validate(Proposal{
		Decision:  Allow,
		Route:     LabProbe,
		Action:    ActionPortbridgeProbe,
		Resources: []string{"portbridge:loopback"},
		Reason:    "probe action must stay in lab validator",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported action") {
		t.Fatalf("expected product validator to reject probe action, got %v", err)
	}
}

func TestValidateLabProposalAllowsRegisteredSubjectAction(t *testing.T) {
	proposal, err := ValidateLabProposal(LabProposal{
		Subject:  "lab:portbridge",
		Decision: Allow,
		Route:    LabProbe,
		Action:   ActionPortbridgeProbe,
		Resources: []string{
			"portbridge:loopback",
			"listen:127.0.0.1:0",
			"target:127.0.0.1:3000",
		},
		Reason: "loopback port bridge capability probe",
	})
	if err != nil {
		t.Fatalf("ValidateLabProposal: %v", err)
	}
	if proposal.Subject != "lab:portbridge" || proposal.Route != LabProbe || proposal.Action != ActionPortbridgeProbe {
		t.Fatalf("unexpected lab proposal: %+v", proposal)
	}
}

func TestValidateLabProposalRejectsUnsafeShapes(t *testing.T) {
	tests := []struct {
		name     string
		proposal LabProposal
		want     string
	}{
		{
			name: "subject mismatch",
			proposal: LabProposal{
				Subject:   "lab:browser",
				Decision:  Allow,
				Route:     LabProbe,
				Action:    ActionPortbridgeProbe,
				Resources: []string{"portbridge:loopback"},
				Reason:    "bad subject",
			},
			want: "not allowed",
		},
		{
			name: "allow without lab route",
			proposal: LabProposal{
				Subject:   "lab:portbridge",
				Decision:  Allow,
				Route:     HostBroker,
				Action:    ActionPortbridgeProbe,
				Resources: []string{"portbridge:loopback"},
				Reason:    "bad route",
			},
			want: "lab-probe",
		},
		{
			name: "secret resource",
			proposal: LabProposal{
				Subject:   "lab:portbridge",
				Decision:  Allow,
				Route:     LabProbe,
				Action:    ActionPortbridgeProbe,
				Resources: []string{"target:token=abc"},
				Reason:    "bad resource",
			},
			want: "secret",
		},
		{
			name: "unsupported action",
			proposal: LabProposal{
				Subject:   "lab:portbridge",
				Decision:  Allow,
				Route:     LabProbe,
				Action:    "host.open",
				Resources: []string{"url:https"},
				Reason:    "bad action",
			},
			want: "unsupported lab action",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ValidateLabProposal(tt.proposal); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestProposalMatchesJSONSchemaAndValidator(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	schema := compilePolicySchema(t)
	proposals := []Proposal{
		{
			Decision:  Allow,
			Route:     HostBroker,
			Action:    "host.open",
			Resources: []string{"url:https"},
			Reason:    "web URL open is allowed through host broker",
			Audit:     map[string]any{"source": "test"},
		},
		{
			Decision:  AuditOnly,
			Route:     GuestDirect,
			Action:    "guest.exec",
			Resources: []string{"guest-command:node"},
			Reason:    "top-level command execution",
		},
		{
			Decision:  AuditOnly,
			Route:     GuestDirect,
			Action:    "network.connect",
			Resources: []string{"network:direct"},
			Reason:    "session network setup",
		},
		{
			Decision:  Allow,
			Route:     PortBridge,
			Action:    ActionPortbridgeHostToGuest,
			Resources: []string{"direction:host-to-guest", "listen:host-loopback", "target:guest", "owner:preview.open", "lifetime:run"},
			Reason:    "host-to-guest PortBridge requested by preview.open",
		},
	}
	for _, proposal := range proposals {
		if _, err := e.Validate(proposal); err != nil {
			t.Fatalf("Validate(%+v): %v", proposal, err)
		}
		if err := validatePolicyProposal(schema, proposal); err != nil {
			t.Fatalf("schema validation(%+v): %v", proposal, err)
		}
	}
}

func TestProposalSchemaAndValidatorRejectInvalidShapes(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	schema := compilePolicySchema(t)
	tests := []struct {
		name     string
		proposal Proposal
		want     string
	}{
		{
			name: "missing resources",
			proposal: Proposal{
				Decision: Allow,
				Route:    HostBroker,
				Action:   "host.open",
				Reason:   "missing resource",
			},
			want: "resources",
		},
		{
			name: "missing reason",
			proposal: Proposal{
				Decision:  Allow,
				Route:     HostBroker,
				Action:    "host.open",
				Resources: []string{"url:https"},
			},
			want: "reason",
		},
		{
			name: "deny with non-deny route",
			proposal: Proposal{
				Decision:  Deny,
				Route:     HostBroker,
				Action:    "host.open",
				Resources: []string{"url:https"},
				Reason:    "denied",
			},
			want: "deny route",
		},
		{
			name: "host open allow without broker",
			proposal: Proposal{
				Decision:  Allow,
				Route:     GuestDirect,
				Action:    "host.open",
				Resources: []string{"url:https"},
				Reason:    "bad route",
			},
			want: "host-broker",
		},
		{
			name: "host open audit-only fake route",
			proposal: Proposal{
				Decision:  AuditOnly,
				Route:     Fake,
				Action:    "host.open",
				Resources: []string{"url:https"},
				Reason:    "bad route",
			},
			want: "host-broker",
		},
		{
			name: "host open audit-only deny route",
			proposal: Proposal{
				Decision:  AuditOnly,
				Route:     DenyRoute,
				Action:    "host.open",
				Resources: []string{"url:https"},
				Reason:    "bad route",
			},
			want: "host-broker",
		},
		{
			name: "guest exec through host broker",
			proposal: Proposal{
				Decision:  AuditOnly,
				Route:     HostBroker,
				Action:    "guest.exec",
				Resources: []string{"guest-command:node"},
				Reason:    "bad route",
			},
			want: "guest-direct or guest-exec",
		},
		{
			name: "network connect through host broker",
			proposal: Proposal{
				Decision:  AuditOnly,
				Route:     HostBroker,
				Action:    "network.connect",
				Resources: []string{"network:direct"},
				Reason:    "bad route",
			},
			want: "guest-direct",
		},
		{
			name: "portbridge through host broker",
			proposal: Proposal{
				Decision:  Allow,
				Route:     HostBroker,
				Action:    ActionPortbridgeHostToGuest,
				Resources: []string{"direction:host-to-guest", "listen:host-loopback", "target:guest", "owner:preview.open", "lifetime:run"},
				Reason:    "bad route",
			},
			want: "portbridge route",
		},
		{
			name: "unsupported action",
			proposal: Proposal{
				Decision:  Allow,
				Route:     HostBroker,
				Action:    "host.browser.open",
				Resources: []string{"url:https"},
				Reason:    "unsupported action",
			},
			want: "unsupported action",
		},
		{
			name: "secret value in resource",
			proposal: Proposal{
				Decision:  Allow,
				Route:     HostBroker,
				Action:    "host.open",
				Resources: []string{"secret=value"},
				Reason:    "bad resource",
			},
			want: "secret",
		},
		{
			name: "token value in resource",
			proposal: Proposal{
				Decision:  Allow,
				Route:     HostBroker,
				Action:    "host.open",
				Resources: []string{"url:https://example.com/callback?token=abc"},
				Reason:    "bad resource",
			},
			want: "secret",
		},
		{
			name: "URL credential in resource",
			proposal: Proposal{
				Decision:  Allow,
				Route:     HostBroker,
				Action:    "host.open",
				Resources: []string{"url:https://user:pass@example.com"},
				Reason:    "bad resource",
			},
			want: "secret",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.Validate(tt.proposal); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected validator error containing %q, got %v", tt.want, err)
			}
			if err := validatePolicyProposal(schema, tt.proposal); err == nil {
				t.Fatal("expected schema validation to fail")
			}
		})
	}
}

func TestValidateTreatsMaxCapabilitiesAsUpperBound(t *testing.T) {
	p := profile.Default("test")
	p.Policy.MaxCapabilities = []string{"guest.exec", "network.connect"}
	e := NewEvaluator(p)
	_, err := e.Validate(Proposal{
		Decision:  Allow,
		Route:     HostBroker,
		Action:    "host.open",
		Resources: []string{"url:https"},
		Reason:    "profile did not grant host.open upper bound",
	})
	if err == nil || !strings.Contains(err.Error(), `exceeds profile max capabilities`) {
		t.Fatalf("expected host.open to exceed max capabilities, got %v", err)
	}
}

func TestEvaluateOpenRejectsHostLocalNetworkTargets(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	for _, target := range []string{
		"http://localhost:3000",
		"http://app.localhost",
		"http://127.0.0.1:3000",
		"http://[::1]:3000",
		"http://0.0.0.0:3000",
		"http://10.0.0.10",
		"http://172.16.0.10",
		"http://192.168.1.10",
		"http://100.64.0.10",
		"http://198.18.0.1",
		"http://169.254.1.10",
		"http://printer.local",
		"http://host.docker.internal:3000",
		"http://host.lima.internal:3000",
		"http://host.containers.internal:3000",
	} {
		t.Run(target, func(t *testing.T) {
			proposal, err := e.EvaluateOpen(target)
			if err != nil {
				t.Fatalf("EvaluateOpen: %v", err)
			}
			if proposal.Decision != Deny || proposal.Route != DenyRoute {
				t.Fatalf("expected local URL denial, got %+v", proposal)
			}
			if !strings.Contains(proposal.Reason, "profile policy") {
				t.Fatalf("denial should explain profile policy: %+v", proposal)
			}
		})
	}
}

func TestEvaluateOpenAllowsHostLocalTargetsWhenProfileOptsIn(t *testing.T) {
	p := profile.Default("test")
	p.HostCapabilities.Open.AllowLocalURLs = true
	e := NewEvaluator(p)
	for _, target := range []string{
		"http://localhost:3000",
		"http://127.0.0.1:3000",
		"http://host.lima.internal:3000",
	} {
		t.Run(target, func(t *testing.T) {
			proposal, err := e.EvaluateOpen(target)
			if err != nil {
				t.Fatalf("EvaluateOpen: %v", err)
			}
			if proposal.Decision != Allow || proposal.Route != HostBroker {
				t.Fatalf("expected profile-authorized local URL allow, got %+v", proposal)
			}
		})
	}
}

func TestEvaluateOpenAllowsPrivateNetworkTargetsWhenProfileOptsIn(t *testing.T) {
	p := profile.Default("test")
	p.HostCapabilities.Open.AllowPrivateNetworkURLs = true
	e := NewEvaluator(p)
	for _, target := range []string{
		"http://10.0.0.10",
		"http://172.16.0.10",
		"http://192.168.1.10",
		"http://100.64.0.10",
	} {
		t.Run(target, func(t *testing.T) {
			proposal, err := e.EvaluateOpen(target)
			if err != nil {
				t.Fatalf("EvaluateOpen: %v", err)
			}
			if proposal.Decision != Allow || proposal.Route != HostBroker {
				t.Fatalf("expected profile-authorized private URL allow, got %+v", proposal)
			}
		})
	}
}

func TestEvaluateOpenRejectsResolvedHostLocalNetworkTargets(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	e.ResolveHost = func(host string) ([]netip.Addr, error) {
		if host != "public-looking.example" {
			t.Fatalf("unexpected resolve host %q", host)
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("127.0.0.1")}, nil
	}
	proposal, err := e.EvaluateOpen("https://public-looking.example/path")
	if err != nil {
		t.Fatalf("EvaluateOpen: %v", err)
	}
	if proposal.Decision != Deny || proposal.Route != DenyRoute {
		t.Fatalf("expected resolved local URL denial, got %+v", proposal)
	}
	if !strings.Contains(proposal.Reason, "resolves to localhost") {
		t.Fatalf("denial should explain resolved profile policy boundary: %+v", proposal)
	}
}

func TestEvaluateOpenAllowsResolvedHostLocalTargetsWhenProfileOptsIn(t *testing.T) {
	p := profile.Default("test")
	p.HostCapabilities.Open.AllowLocalURLs = true
	e := NewEvaluator(p)
	e.ResolveHost = func(host string) ([]netip.Addr, error) {
		if host != "public-looking.example" {
			t.Fatalf("unexpected resolve host %q", host)
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	proposal, err := e.EvaluateOpen("https://public-looking.example/path")
	if err != nil {
		t.Fatalf("EvaluateOpen: %v", err)
	}
	if proposal.Decision != Allow || proposal.Route != HostBroker {
		t.Fatalf("expected resolved local URL allow by profile policy, got %+v", proposal)
	}
}

func TestEvaluateOpenRejectsResolvedCGNATTargets(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	e.ResolveHost = func(host string) ([]netip.Addr, error) {
		if host != "public-looking.example" {
			t.Fatalf("unexpected resolve host %q", host)
		}
		return []netip.Addr{netip.MustParseAddr("100.64.0.10")}, nil
	}
	proposal, err := e.EvaluateOpen("https://public-looking.example/path")
	if err != nil {
		t.Fatalf("EvaluateOpen: %v", err)
	}
	if proposal.Decision != Deny || proposal.Route != DenyRoute {
		t.Fatalf("expected resolved CGNAT URL denial, got %+v", proposal)
	}
	if !strings.Contains(proposal.Reason, "private host network") {
		t.Fatalf("denial should explain host network boundary: %+v", proposal)
	}
}

func TestEvaluateOpenRejectsResolvedBenchmarkNetworkTargets(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	e.ResolveHost = func(host string) ([]netip.Addr, error) {
		if host != "public-looking.example" {
			t.Fatalf("unexpected resolve host %q", host)
		}
		return []netip.Addr{netip.MustParseAddr("198.18.0.1")}, nil
	}
	proposal, err := e.EvaluateOpen("https://public-looking.example/path")
	if err != nil {
		t.Fatalf("EvaluateOpen: %v", err)
	}
	if proposal.Decision != Deny || proposal.Route != DenyRoute {
		t.Fatalf("expected resolved benchmark-network URL denial, got %+v", proposal)
	}
	if !strings.Contains(proposal.Reason, "private host network") {
		t.Fatalf("denial should explain host network boundary: %+v", proposal)
	}
}

func TestEvaluateOpenFailsClosedWhenURLHostDoesNotResolve(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	e.ResolveHost = func(string) ([]netip.Addr, error) {
		return nil, errors.New("no such host")
	}
	proposal, err := e.EvaluateOpen("https://public-looking.example/path")
	if err != nil {
		t.Fatalf("EvaluateOpen: %v", err)
	}
	if proposal.Decision != Deny || proposal.Route != DenyRoute {
		t.Fatalf("expected DNS failure denial, got %+v", proposal)
	}
	if !strings.Contains(proposal.Reason, "DNS resolution failed") {
		t.Fatalf("denial should explain DNS boundary: %+v", proposal)
	}
}

func TestEvaluateOpenAllowsResolvedPublicHost(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	e.ResolveHost = func(host string) ([]netip.Addr, error) {
		if host != "public.example" {
			t.Fatalf("unexpected resolve host %q", host)
		}
		return []netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")}, nil
	}
	proposal, err := e.EvaluateOpen("https://public.example/path")
	if err != nil {
		t.Fatalf("EvaluateOpen: %v", err)
	}
	if proposal.Decision != Allow || proposal.Route != HostBroker {
		t.Fatalf("expected public URL allow, got %+v", proposal)
	}
}

func TestEvaluateOpenReResolvesHostEachEvaluation(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	var calls int
	e.ResolveHost = func(host string) ([]netip.Addr, error) {
		if host != "app.attacker.example" {
			t.Fatalf("unexpected resolve host %q", host)
		}
		calls++
		if calls == 1 {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}

	first, err := e.EvaluateOpen("https://app.attacker.example/path")
	if err != nil {
		t.Fatalf("first EvaluateOpen: %v", err)
	}
	if first.Decision != Allow || first.Route != HostBroker {
		t.Fatalf("expected first public resolution to allow, got %+v", first)
	}

	second, err := e.EvaluateOpen("https://app.attacker.example/path")
	if err != nil {
		t.Fatalf("second EvaluateOpen: %v", err)
	}
	if second.Decision != Deny || second.Route != DenyRoute {
		t.Fatalf("expected second loopback resolution to deny, got %+v", second)
	}
	if calls != 2 {
		t.Fatalf("host should be resolved for every evaluation, calls=%d", calls)
	}
}

func TestEvaluateOpenDoesNotResolvePublicIPLiteral(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	e.ResolveHost = func(host string) ([]netip.Addr, error) {
		t.Fatalf("IP literal should not be resolved, got %q", host)
		return nil, nil
	}
	proposal, err := e.EvaluateOpen("https://93.184.216.34/path")
	if err != nil {
		t.Fatalf("EvaluateOpen: %v", err)
	}
	if proposal.Decision != Allow || proposal.Route != HostBroker {
		t.Fatalf("expected public IP literal URL allow, got %+v", proposal)
	}
}

func TestRunCommandScriptIsClampedByValidator(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `
function decideCommand(ctx) {
  return hideout.decision.allow({
    route: "host-broker",
    action: "host.exec",
    resources: ["*"],
    reason: "try to escape"
  });
}`
	_, err := e.RunCommandScript(source, "decideCommand", CommandContext{})
	if err == nil {
		t.Fatal("expected script proposal to be rejected by final validator")
	}
}

func TestRunCommandScriptSDKReturnsProposal(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `
function decideCommand(ctx) {
  return hideout.decision.deny({
    action: "host.open",
    resources: ["url:https"],
    reason: "script denied"
  });
}`
	proposal, err := e.RunCommandScript(source, "decideCommand", CommandContext{})
	if err != nil {
		t.Fatalf("RunCommandScript: %v", err)
	}
	if proposal.Decision != Deny || proposal.Route != DenyRoute || proposal.Action != "host.open" || proposal.Reason != "script denied" {
		t.Fatalf("unexpected proposal: %+v", proposal)
	}
}

func TestRunCommandScriptRejectsUnknownOutputFields(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `
function decideCommand(ctx) {
  const proposal = hideout.decision.deny({
    action: "host.open",
    resources: ["url:https"],
    reason: "script denied"
  });
  proposal.hostExec = "please";
  return proposal;
}`
	_, err := e.RunCommandScript(source, "decideCommand", CommandContext{})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestRunAuditRedactScriptReturnsDetails(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `
function redactAudit(ctx) {
  const details = ctx.details;
  details.target = "REDACTED_BY_SCRIPT";
  details.reason = ctx.decision;
  return { details, reason: "custom audit redaction" };
}`
	redaction, err := e.RunAuditRedactScript(source, "redactAudit", AuditContext{
		Version:  "policy-audit/v1",
		Profile:  map[string]string{"name": "test"},
		Session:  map[string]any{"id": "ses_1"},
		Action:   "host.open",
		Decision: "allow",
		Details:  map[string]any{"target": "https://example.com/private"},
	})
	if err != nil {
		t.Fatalf("RunAuditRedactScript: %v", err)
	}
	if redaction.Details["target"] != "REDACTED_BY_SCRIPT" || redaction.Details["reason"] != "allow" {
		t.Fatalf("unexpected redaction: %+v", redaction)
	}
}

func TestRunAuditRedactScriptRejectsUnknownOutputFields(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `
function redactAudit(ctx) {
  return { details: ctx.details, hostExec: "please" };
}`
	_, err := e.RunAuditRedactScript(source, "redactAudit", AuditContext{
		Version:  "policy-audit/v1",
		Profile:  map[string]string{"name": "test"},
		Session:  map[string]any{"id": "ses_1"},
		Action:   "host.open",
		Decision: "allow",
		Details:  map[string]any{"target": "https://example.com/private"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestRunAuditRedactScriptRequiresDetails(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `function redactAudit(ctx) { return { reason: "bad" }; }`
	_, err := e.RunAuditRedactScript(source, "redactAudit", AuditContext{
		Version:  "policy-audit/v1",
		Profile:  map[string]string{"name": "test"},
		Session:  map[string]any{"id": "ses_1"},
		Action:   "host.open",
		Decision: "allow",
		Details:  map[string]any{"target": "https://example.com/private"},
	})
	if err == nil || !strings.Contains(err.Error(), "details") {
		t.Fatalf("expected details error, got %v", err)
	}
}

func TestRunCommandScriptRejectsOversizedSource(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	_, err := e.RunCommandScript(strings.Repeat(" ", MaxScriptSourceBytes+1), "decideCommand", CommandContext{})
	if err == nil || !strings.Contains(err.Error(), "source exceeds") {
		t.Fatalf("expected source size error, got %v", err)
	}
}

func TestRunCommandScriptRejectsOversizedInput(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `function decideCommand(ctx) {
  return hideout.decision.allow({
    route: "host-broker",
    action: "host.open",
    resources: ["url:https"],
    reason: "ok"
  });
}`
	_, err := e.RunCommandScript(source, "decideCommand", CommandContext{
		Extra: map[string]interface{}{"payload": strings.Repeat("x", MaxScriptInputBytes)},
	})
	if err == nil || !strings.Contains(err.Error(), "input exceeds") {
		t.Fatalf("expected input size error, got %v", err)
	}
}

func TestRunCommandScriptRejectsOversizedOutput(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := fmt.Sprintf(`function decideCommand(ctx) {
  return hideout.decision.allow({
    route: "host-broker",
    action: "host.open",
    resources: ["url:https"],
    reason: %q
  });
}`, strings.Repeat("x", MaxScriptOutputBytes))
	_, err := e.RunCommandScript(source, "decideCommand", CommandContext{})
	if err == nil || !strings.Contains(err.Error(), "output exceeds") {
		t.Fatalf("expected output size error, got %v", err)
	}
}

func TestRunCommandScriptDoesNotExposeHostOrRuntimeGlobals(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `
function decideCommand(ctx) {
  const forbidden = [
    ["require", typeof require],
    ["process", typeof process],
    ["setTimeout", typeof setTimeout],
    ["setInterval", typeof setInterval],
    ["fetch", typeof fetch],
    ["XMLHttpRequest", typeof XMLHttpRequest],
    ["Deno", typeof Deno],
    ["Bun", typeof Bun]
  ];
  for (const item of forbidden) {
    if (item[1] !== "undefined") {
      return hideout.decision.allow({
        route: "host-broker",
        action: "host.open",
        resources: ["url:https"],
        reason: "unexpected global " + item[0]
      });
    }
  }
  return hideout.decision.deny({
    action: "host.open",
    resources: ["url:https"],
    reason: "no host runtime globals"
  });
}`
	proposal, err := e.RunCommandScript(source, "decideCommand", CommandContext{})
	if err != nil {
		t.Fatalf("RunCommandScript: %v", err)
	}
	if proposal.Decision != Deny || proposal.Reason != "no host runtime globals" {
		t.Fatalf("script runtime exposed a forbidden global: %+v", proposal)
	}
}

func TestRunCommandScriptUsesDeterministicTimeAndRandom(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `
function decideCommand(ctx) {
  return hideout.decision.deny({
    action: "host.open",
    resources: ["url:https"],
    reason: [
      Date.now(),
      new Date().toISOString(),
      Math.random(),
      Math.random()
    ].join("|")
  });
}`
	first, err := e.RunCommandScript(source, "decideCommand", CommandContext{})
	if err != nil {
		t.Fatalf("first RunCommandScript: %v", err)
	}
	second, err := e.RunCommandScript(source, "decideCommand", CommandContext{})
	if err != nil {
		t.Fatalf("second RunCommandScript: %v", err)
	}
	if first.Reason != second.Reason {
		t.Fatalf("expected deterministic script outputs, got %q and %q", first.Reason, second.Reason)
	}
	if !strings.HasPrefix(first.Reason, "0|1970-01-01T00:00:00.000Z|") {
		t.Fatalf("expected frozen Date source, got %q", first.Reason)
	}
}

func TestRunCommandScriptTimesOut(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	source := `function decideCommand(ctx) {
  while (true) {}
}`
	_, err := e.RunCommandScript(source, "decideCommand", CommandContext{})
	if !errors.Is(err, ErrScriptTimeout) {
		t.Fatalf("expected ErrScriptTimeout, got %v", err)
	}
}

func TestEvaluateOpenAllowsHTTPS(t *testing.T) {
	e := NewEvaluator(profile.Default("test"))
	p, err := e.EvaluateOpen("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if p.Decision != Allow || p.Action != "host.open" || p.Route != HostBroker {
		t.Fatalf("unexpected proposal: %+v", p)
	}
}

func TestEvaluateOpenRespectsHostCapabilityFlags(t *testing.T) {
	p := profile.Default("test")
	p.HostCapabilities.Open.AllowURLs = false
	e := NewEvaluator(p)
	proposal, err := e.EvaluateOpen("https://example.com")
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Decision != Deny {
		t.Fatalf("expected deny, got %+v", proposal)
	}
}

func TestEvaluateWorkspaceOpenRespectsHostCapabilityFlags(t *testing.T) {
	p := profile.Default("test")
	p.HostCapabilities.Open.AllowWorkspaceFiles = false
	e := NewEvaluator(p)
	proposal, err := e.EvaluateWorkspaceOpen()
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Decision != Deny || proposal.Route != DenyRoute || proposal.Reason != "workspace file open is disabled by profile policy" {
		t.Fatalf("expected workspace file deny, got %+v", proposal)
	}
}

func compilePolicySchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join("..", "..", "schemas", "policy.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read policy schema: %v", err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode policy schema: %v", err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("policy.schema.json", doc); err != nil {
		t.Fatalf("add policy schema: %v", err)
	}
	schema, err := compiler.Compile("policy.schema.json")
	if err != nil {
		t.Fatalf("compile policy schema: %v", err)
	}
	return schema
}

func validatePolicyProposal(schema *jsonschema.Schema, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(doc)
}
