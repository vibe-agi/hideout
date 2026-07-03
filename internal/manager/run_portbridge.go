package manager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/portbridge"
)

type RunPortBridgeRequest struct {
	ID            string
	Owner         string
	ListenAddress string
	TargetAddress string
}

type runPortBridgeLease struct {
	endpoint backend.PortBridgeEndpoint
	bridge   *portbridge.Bridge
}

func validateRunPortBridgeRequests(runSession RunSession, requests []RunPortBridgeRequest, aw *audit.Writer) error {
	if len(requests) == 0 {
		return nil
	}
	if aw == nil {
		aw = audit.NewDiscard()
	}
	for _, request := range requests {
		spec := runPortBridgeSpec(request)
		if err := validateRunPortBridgePolicy(runSession, spec); err != nil {
			_ = emitPortBridgeAudit(aw, runSession, spec, string(policy.Deny), "denied", err.Error())
			return err
		}
		if runSession.Plan.Backend != "native" {
			err := fmt.Errorf("host-to-guest PortBridge requires a backend provider for %s backend", runSession.Plan.Backend)
			_ = emitPortBridgeAudit(aw, runSession, spec, string(policy.Deny), "denied", err.Error())
			return err
		}
		if err := portbridge.ValidateHostToGuestProduct(spec); err != nil {
			_ = emitPortBridgeAudit(aw, runSession, spec, string(policy.Deny), "denied", err.Error())
			return err
		}
	}
	return nil
}

func startRunPortBridges(ctx context.Context, runSession RunSession, requests []RunPortBridgeRequest, aw *audit.Writer) ([]runPortBridgeLease, []backend.PortBridgeEndpoint, error) {
	if len(requests) == 0 {
		return nil, nil, nil
	}
	if aw == nil {
		aw = audit.NewDiscard()
	}
	var leases []runPortBridgeLease
	var endpoints []backend.PortBridgeEndpoint
	for _, request := range requests {
		spec := runPortBridgeSpec(request)
		bridge, err := portbridge.Start(ctx, spec)
		if err != nil {
			_ = emitPortBridgeAudit(aw, runSession, spec, "error", "error", err.Error())
			return leases, endpoints, err
		}
		endpoint := portBridgeEndpoint(spec, bridge.ListenAddress())
		lease := runPortBridgeLease{endpoint: endpoint, bridge: bridge}
		leases = append(leases, lease)
		endpoints = append(endpoints, endpoint)
		if err := emitPortBridgeAudit(aw, runSession, spec, string(policy.Allow), "ready", "host-to-guest PortBridge endpoint created"); err != nil {
			return leases, endpoints, err
		}
	}
	return leases, endpoints, nil
}

func closeRunPortBridgeLeases(leases []runPortBridgeLease, aw *audit.Writer, sessionID, profileName, backendName string) error {
	if len(leases) == 0 {
		return nil
	}
	if aw == nil {
		aw = audit.NewDiscard()
	}
	var errs []error
	for _, lease := range leases {
		if err := lease.bridge.Close(); err != nil {
			errs = append(errs, err)
		}
		details := map[string]any{
			"status":           "cleanup",
			"bridgeId":         portBridgeAuditLabel(lease.endpoint.ID),
			"owner":            portBridgeAuditLabel(lease.endpoint.Owner),
			"lifetime":         lease.endpoint.Lifetime,
			"direction":        lease.endpoint.Direction,
			"listenScope":      lease.endpoint.ListenScope,
			"targetScope":      lease.endpoint.TargetScope,
			"endpointCategory": lease.endpoint.EndpointCategory,
			"endpoint":         "present",
		}
		if err := aw.Emit(audit.Event{
			Session:  sessionID,
			Profile:  profileName,
			Backend:  backendName,
			Action:   policy.ActionPortbridgeHostToGuest,
			Decision: string(policy.AuditOnly),
			Details:  details,
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validateRunPortBridgePolicy(runSession RunSession, spec portbridge.Spec) error {
	proposal := policy.Proposal{
		Decision:  policy.Allow,
		Route:     policy.PortBridge,
		Action:    policy.ActionPortbridgeHostToGuest,
		Resources: portBridgePolicyResources(spec),
		Reason:    "host-to-guest PortBridge requested by " + spec.Owner,
	}
	_, err := policy.NewEvaluator(runSession.Plan.RuntimeProfile).Validate(proposal)
	return err
}

func runPortBridgeSpec(request RunPortBridgeRequest) portbridge.Spec {
	listenAddress := strings.TrimSpace(request.ListenAddress)
	if listenAddress == "" {
		listenAddress = "127.0.0.1:0"
	}
	return portbridge.Spec{
		ID:               strings.TrimSpace(request.ID),
		Owner:            strings.TrimSpace(request.Owner),
		Lifetime:         portbridge.LifetimeRun,
		Direction:        portbridge.DirectionHostToGuest,
		ListenScope:      portbridge.ListenScopeLoopback,
		ListenAddress:    listenAddress,
		TargetScope:      portbridge.TargetScopeGuest,
		TargetAddress:    strings.TrimSpace(request.TargetAddress),
		EndpointCategory: portbridge.EndpointCategoryHostLoopback,
	}
}

func portBridgeEndpoint(spec portbridge.Spec, listenAddress string) backend.PortBridgeEndpoint {
	return backend.PortBridgeEndpoint{
		ID:               spec.ID,
		Owner:            spec.Owner,
		Lifetime:         string(spec.Lifetime),
		Direction:        string(spec.Direction),
		ListenScope:      string(spec.ListenScope),
		ListenAddress:    listenAddress,
		TargetScope:      string(spec.TargetScope),
		EndpointCategory: string(spec.EndpointCategory),
	}
}

func portBridgePolicyResources(spec portbridge.Spec) []string {
	return []string{
		"direction:" + string(spec.Direction),
		"listen:" + string(spec.EndpointCategory),
		"target:" + string(spec.TargetScope),
		"owner:" + portBridgeAuditLabel(spec.Owner),
		"lifetime:" + string(spec.Lifetime),
	}
}

func emitPortBridgeAudit(aw *audit.Writer, runSession RunSession, spec portbridge.Spec, decision, status, reason string) error {
	endpointState := "present"
	if status == "denied" || status == "error" {
		endpointState = "not-created"
	}
	details := map[string]any{
		"status":           status,
		"bridgeId":         portBridgeAuditLabel(spec.ID),
		"owner":            portBridgeAuditLabel(spec.Owner),
		"lifetime":         string(spec.Lifetime),
		"direction":        string(spec.Direction),
		"listenScope":      string(spec.ListenScope),
		"targetScope":      string(spec.TargetScope),
		"endpointCategory": string(spec.EndpointCategory),
		"endpoint":         endpointState,
	}
	if reason != "" {
		details["reason"] = reason
	}
	return aw.Emit(audit.Event{
		Session:  runSession.Layout.ID,
		Profile:  runSession.Plan.ProfileName,
		Backend:  runSession.Plan.Backend,
		Action:   policy.ActionPortbridgeHostToGuest,
		Decision: decision,
		Details:  details,
	})
}

func portBridgeAuditLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "missing"
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == ':':
		default:
			return "invalid"
		}
	}
	return value
}
