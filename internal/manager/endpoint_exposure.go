package manager

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/policy"
	"github.com/vibe-agi/hideout/internal/profile"
)

const (
	EndpointSourceProfile = "profile"
	EndpointSourceManual  = "manual"

	OpenTargetPreviewOpen = "preview.open"
)

type RunOpenTargetOwner struct {
	ID   string
	Kind string
}

type RunEndpointCandidate struct {
	ID            string
	Source        string
	Owner         string
	Proto         string
	TargetAddress string
}

type RunEndpointExposureRequest struct {
	CandidateID string
	Owner       string
	Kind        string
	ClosePolicy string
}

// BuildPreviewOpenOptions resolves user-facing --preview values inside Manager.
// The CLI sends only raw intent; it never reads profile policy or manufactures
// an authority-bearing endpoint binding itself.
func BuildPreviewOpenOptions(p profile.Profile, targets []string) ([]RunOpenTargetOwner, []RunEndpointCandidate, []RunEndpointExposureRequest, error) {
	if len(targets) == 0 {
		return nil, nil, nil, nil
	}
	owners := []RunOpenTargetOwner{{ID: OpenTargetPreviewOpen, Kind: OpenTargetPreviewOpen}}
	profileCandidates := map[string]profile.EndpointCandidate{}
	for _, candidate := range p.EndpointExposure.HostToGuest {
		profileCandidates[strings.TrimSpace(candidate.ID)] = candidate
	}
	runCandidates := make([]RunEndpointCandidate, 0, len(targets))
	exposures := make([]RunEndpointExposureRequest, 0, len(targets))
	for i, raw := range targets {
		value := strings.TrimSpace(raw)
		if value == "" {
			return nil, nil, nil, errors.New("--preview cannot be empty")
		}
		candidateID := value
		if candidate, ok := profileCandidates[value]; ok {
			owner := strings.TrimSpace(candidate.Owner)
			if owner != OpenTargetPreviewOpen {
				return nil, nil, nil, fmt.Errorf("--preview candidate %q belongs to owner %q, not preview.open", value, owner)
			}
		} else {
			targetAddress, err := normalizePreviewEndpoint(value)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("--preview %q: %w", value, err)
			}
			candidateID = fmt.Sprintf("manual_preview_%d", i+1)
			runCandidates = append(runCandidates, RunEndpointCandidate{
				ID: candidateID, Source: EndpointSourceManual, Owner: OpenTargetPreviewOpen,
				Proto: "tcp", TargetAddress: targetAddress,
			})
		}
		exposures = append(exposures, RunEndpointExposureRequest{
			CandidateID: candidateID, Owner: OpenTargetPreviewOpen,
			Kind: OpenTargetPreviewOpen, ClosePolicy: "session-end",
		})
	}
	return owners, runCandidates, exposures, nil
}

func normalizePreviewEndpoint(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("endpoint is required")
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil {
			return "", err
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return "", fmt.Errorf("preview URL scheme %q is unsupported", u.Scheme)
		}
		value = u.Host
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		return "", fmt.Errorf("must be host:port or http(s) loopback URL: %w", err)
	}
	if host == "localhost" {
		return net.JoinHostPort("127.0.0.1", port), nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", errors.New("preview endpoint must use localhost or a loopback IP")
	}
	return net.JoinHostPort(host, port), nil
}

func resolveRunEndpointExposures(runSession RunSession, owners []RunOpenTargetOwner, runCandidates []RunEndpointCandidate, requests []RunEndpointExposureRequest) ([]RunPortBridgeRequest, error) {
	if len(requests) == 0 {
		return nil, nil
	}
	ownerRegistry, err := activeOpenTargetRegistry(owners)
	if err != nil {
		return nil, err
	}
	candidates, err := runEndpointCandidateRegistry(runSession.Plan.RuntimeProfile.EndpointExposure.HostToGuest, runCandidates)
	if err != nil {
		return nil, err
	}
	out := make([]RunPortBridgeRequest, 0, len(requests))
	for i, request := range requests {
		resolved, err := resolveHostToGuestExposureRequest(i, ownerRegistry, candidates, request)
		if err != nil {
			return nil, err
		}
		out = append(out, resolved)
	}
	return out, nil
}

func activeOpenTargetRegistry(owners []RunOpenTargetOwner) (map[string]RunOpenTargetOwner, error) {
	registry := map[string]RunOpenTargetOwner{}
	for i, owner := range owners {
		id := strings.TrimSpace(owner.ID)
		kind := strings.TrimSpace(owner.Kind)
		if id == "" || kind == "" {
			return nil, fmt.Errorf("openTargets[%d] requires id and kind", i)
		}
		if !validManagerLabel(id) {
			return nil, fmt.Errorf("openTargets[%d].id contains unsupported characters", i)
		}
		if !validManagerLabel(kind) {
			return nil, fmt.Errorf("openTargets[%d].kind contains unsupported characters", i)
		}
		if _, exists := registry[id]; exists {
			return nil, fmt.Errorf("openTargets[%d].id duplicates %q", i, id)
		}
		registry[id] = RunOpenTargetOwner{ID: id, Kind: kind}
	}
	return registry, nil
}

func runEndpointCandidateRegistry(profileCandidates []profile.EndpointCandidate, runCandidates []RunEndpointCandidate) (map[string]RunEndpointCandidate, error) {
	registry := map[string]RunEndpointCandidate{}
	for i, candidate := range profileCandidates {
		next := RunEndpointCandidate{
			ID:            strings.TrimSpace(candidate.ID),
			Source:        EndpointSourceProfile,
			Owner:         strings.TrimSpace(candidate.Owner),
			Proto:         strings.TrimSpace(candidate.Proto),
			TargetAddress: strings.TrimSpace(candidate.TargetAddress),
		}
		if err := addRunEndpointCandidate(registry, fmt.Sprintf("profile endpointExposure.hostToGuest[%d]", i), next); err != nil {
			return nil, err
		}
	}
	for i, candidate := range runCandidates {
		next := RunEndpointCandidate{
			ID:            strings.TrimSpace(candidate.ID),
			Source:        strings.TrimSpace(candidate.Source),
			Owner:         strings.TrimSpace(candidate.Owner),
			Proto:         strings.TrimSpace(candidate.Proto),
			TargetAddress: strings.TrimSpace(candidate.TargetAddress),
		}
		if err := addRunEndpointCandidate(registry, fmt.Sprintf("run endpoint candidate[%d]", i), next); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func addRunEndpointCandidate(registry map[string]RunEndpointCandidate, label string, candidate RunEndpointCandidate) error {
	if candidate.ID == "" {
		return fmt.Errorf("%s id is required", label)
	}
	if !validManagerLabel(candidate.ID) {
		return fmt.Errorf("%s id contains unsupported characters", label)
	}
	if _, exists := registry[candidate.ID]; exists {
		return fmt.Errorf("%s duplicates candidate %q", label, candidate.ID)
	}
	if candidate.Source == "" {
		return fmt.Errorf("%s source is required", label)
	}
	switch candidate.Source {
	case EndpointSourceProfile, EndpointSourceManual:
	default:
		return fmt.Errorf("%s source %q is unsupported for product host-to-guest exposure", label, candidate.Source)
	}
	if candidate.Owner == "" {
		return fmt.Errorf("%s owner is required", label)
	}
	if !validManagerLabel(candidate.Owner) {
		return fmt.Errorf("%s owner contains unsupported characters", label)
	}
	if candidate.Proto == "" {
		candidate.Proto = "tcp"
	}
	if candidate.Proto != "tcp" {
		return fmt.Errorf("%s proto %q is unsupported", label, candidate.Proto)
	}
	if candidate.TargetAddress == "" {
		return fmt.Errorf("%s target address is required", label)
	}
	registry[candidate.ID] = candidate
	return nil
}

func resolveHostToGuestExposureRequest(index int, owners map[string]RunOpenTargetOwner, candidates map[string]RunEndpointCandidate, request RunEndpointExposureRequest) (RunPortBridgeRequest, error) {
	candidateID := strings.TrimSpace(request.CandidateID)
	ownerID := strings.TrimSpace(request.Owner)
	kind := strings.TrimSpace(request.Kind)
	if candidateID == "" {
		return RunPortBridgeRequest{}, fmt.Errorf("endpoint exposure requests[%d].candidateId is required", index)
	}
	if ownerID == "" {
		return RunPortBridgeRequest{}, fmt.Errorf("endpoint exposure requests[%d].owner is required", index)
	}
	if kind == "" {
		kind = OpenTargetPreviewOpen
	}
	owner, ok := owners[ownerID]
	if !ok {
		return RunPortBridgeRequest{}, fmt.Errorf("endpoint exposure owner %q is not active", ownerID)
	}
	if owner.Kind != kind {
		return RunPortBridgeRequest{}, fmt.Errorf("endpoint exposure owner %q kind %q does not match %q", ownerID, owner.Kind, kind)
	}
	candidate, ok := candidates[candidateID]
	if !ok {
		return RunPortBridgeRequest{}, fmt.Errorf("endpoint exposure candidate %q is unknown", candidateID)
	}
	if candidate.Owner != ownerID {
		return RunPortBridgeRequest{}, fmt.Errorf("endpoint exposure candidate %q belongs to owner %q, not %q", candidateID, candidate.Owner, ownerID)
	}
	closePolicy := strings.TrimSpace(request.ClosePolicy)
	if closePolicy == "" {
		closePolicy = "session-end"
	}
	if closePolicy != "session-end" && closePolicy != "process-exit" {
		return RunPortBridgeRequest{}, fmt.Errorf("endpoint exposure close policy %q is unsupported for preview.open", closePolicy)
	}
	return RunPortBridgeRequest{
		ID:            "ep_" + candidate.ID,
		Owner:         owner.ID,
		Action:        policy.ActionEndpointExposeHostToGuest,
		Source:        candidate.Source,
		ClosePolicy:   closePolicy,
		TargetAddress: candidate.TargetAddress,
	}, nil
}

func emitEndpointExposureDeny(aw *audit.Writer, runSession RunSession, requests []RunEndpointExposureRequest, cause error) error {
	if aw == nil {
		aw = audit.NewDiscard()
	}
	owner := "missing"
	candidate := "missing"
	closePolicy := "session-end"
	if len(requests) > 0 {
		owner = portBridgeAuditLabel(requests[0].Owner)
		candidate = portBridgeAuditLabel(requests[0].CandidateID)
		if value := strings.TrimSpace(requests[0].ClosePolicy); value != "" {
			closePolicy = value
		}
	}
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	return aw.Emit(audit.Event{
		Session:  runSession.Layout.ID,
		Profile:  runSession.Plan.ProfileName,
		Backend:  runSession.Plan.Backend,
		Action:   policy.ActionEndpointExposeHostToGuest,
		Decision: string(policy.Deny),
		Details: map[string]any{
			"status":           "denied",
			"owner":            owner,
			"candidate":        candidate,
			"lifetime":         "run",
			"direction":        "host-to-guest",
			"listenScope":      "loopback",
			"targetScope":      "guest",
			"endpointCategory": "host-loopback",
			"source":           "missing",
			"closePolicy":      closePolicy,
			"endpoint":         "not-created",
			"reason":           reason,
		},
	})
}

func validManagerLabel(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == ':':
		default:
			return false
		}
	}
	return true
}
