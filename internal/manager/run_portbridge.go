package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/portbridge"
)

type RunPortBridgeRequest struct {
	ID            string
	Owner         string
	Action        string
	Source        string
	ClosePolicy   string
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
		if runSession.Plan.Backend != "native" && runSession.Plan.Backend != "lima" {
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
		var bridge *portbridge.Bridge
		var listenAddress string
		switch runSession.Plan.Backend {
		case "native":
			var err error
			bridge, err = portbridge.Start(ctx, spec)
			if err != nil {
				_ = emitPortBridgeAudit(aw, runSession, spec, "error", "error", err.Error())
				return leases, endpoints, err
			}
			listenAddress = bridge.ListenAddress()
		case "lima":
			address, err := allocateLoopbackListenAddress(spec.ListenAddress)
			if err != nil {
				_ = emitPortBridgeAudit(aw, runSession, spec, "error", "error", err.Error())
				return leases, endpoints, err
			}
			listenAddress = address
		default:
			err := fmt.Errorf("host-to-guest PortBridge requires a backend provider for %s backend", runSession.Plan.Backend)
			_ = emitPortBridgeAudit(aw, runSession, spec, string(policy.Deny), "denied", err.Error())
			return leases, endpoints, err
		}
		endpoint := portBridgeEndpoint(spec, listenAddress)
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
		if lease.bridge != nil {
			if err := lease.bridge.Close(); err != nil {
				errs = append(errs, err)
			}
		}
		action := lease.endpoint.Action
		if action == "" {
			action = policy.ActionPortbridgeHostToGuest
		}
		closePolicy := lease.endpoint.ClosePolicy
		if closePolicy == "" {
			closePolicy = "session-end"
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
			"source":           portBridgeAuditLabel(lease.endpoint.Source),
			"closePolicy":      closePolicy,
			"closeReason":      "session-end",
			"endpoint":         "present",
		}
		if err := aw.Emit(audit.Event{
			Session:  sessionID,
			Profile:  profileName,
			Backend:  backendName,
			Action:   action,
			Decision: string(policy.AuditOnly),
			Details:  details,
		}); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func validateRunPortBridgePolicy(runSession RunSession, spec portbridge.Spec) error {
	action := spec.Action
	if action == "" {
		action = policy.ActionPortbridgeHostToGuest
	}
	proposal := policy.Proposal{
		Decision:  policy.Allow,
		Route:     policy.PortBridge,
		Action:    action,
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
		Action:           strings.TrimSpace(request.Action),
		Source:           strings.TrimSpace(request.Source),
		ClosePolicy:      strings.TrimSpace(request.ClosePolicy),
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
		Action:           spec.Action,
		Source:           spec.Source,
		ClosePolicy:      spec.ClosePolicy,
		Lifetime:         string(spec.Lifetime),
		Direction:        string(spec.Direction),
		ListenScope:      string(spec.ListenScope),
		ListenAddress:    listenAddress,
		TargetScope:      string(spec.TargetScope),
		TargetAddress:    spec.TargetAddress,
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
	action := spec.Action
	if action == "" {
		action = policy.ActionPortbridgeHostToGuest
	}
	source := spec.Source
	if source == "" {
		source = "internal"
	}
	closePolicy := spec.ClosePolicy
	if closePolicy == "" {
		closePolicy = "session-end"
	}
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
		"source":           portBridgeAuditLabel(source),
		"closePolicy":      closePolicy,
		"endpoint":         endpointState,
	}
	if status == "cleanup" {
		details["closeReason"] = "session-end"
	}
	if reason != "" {
		details["reason"] = reason
	}
	return aw.Emit(audit.Event{
		Session:  runSession.Layout.ID,
		Profile:  runSession.Plan.ProfileName,
		Backend:  runSession.Plan.Backend,
		Action:   action,
		Decision: decision,
		Details:  details,
	})
}

func allocateLoopbackListenAddress(preferred string) (string, error) {
	preferred = strings.TrimSpace(preferred)
	if preferred == "" {
		preferred = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", preferred)
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		return "", err
	}
	return addr, nil
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
