package manager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/broker"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/hostfs"
	"github.com/vibe-agi/hideout/internal/hostfs/readgrant"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/session"
)

const (
	hostFSReadDecisionTimeout = 5 * time.Minute
	hostFSReadRateWindow      = time.Minute
	hostFSReadGrantLifetime   = 24 * time.Hour
	hostFSReadPendingLimit    = 8
	hostFSReadRateLimit       = 8
	hostFSReadReasonMaxBytes  = 512
)

type HostFSReadReopenRequest struct {
	DecisionID      string `json:"decisionId"`
	ExpectedVersion string `json:"expectedVersion"`
	Reason          string `json:"reason,omitempty"`
}

type hostFSReadProvider struct {
	core      Core
	store     *readgrant.Store
	sessionID string
	ownerPath string
	policy    hostfs.EffectivePolicy
	now       func() time.Time
}

func newHostFSReadProvider(core Core, sessionID, readDir, ownerPath string, policy hostfs.EffectivePolicy) (*hostFSReadProvider, error) {
	store, err := readgrant.NewStore(readDir, sessionID)
	if err != nil {
		return nil, err
	}
	policy = hostfs.NewService(policy).Policy
	return &hostFSReadProvider{
		core:      core,
		store:     store,
		sessionID: sessionID,
		ownerPath: ownerPath,
		policy:    policy,
		now:       time.Now,
	}, nil
}

func (p *hostFSReadProvider) ProposeHostFSRead(_ context.Context, proposal broker.HostFSReadProposal) (broker.HostFSReadProposalResult, error) {
	if p == nil || p.store == nil {
		return broker.HostFSReadProposalResult{}, errors.New("HostFS read provider is unavailable")
	}
	grant, reason, err := p.validateProposal(proposal)
	if err != nil {
		return broker.HostFSReadProposalResult{Code: broker.HostFSErrorReadDenied}, nil
	}
	now := p.now().UTC()
	key := hostFSReadKey(p.sessionID, proposal.CanonicalPath)
	decisionID := "dec_hfr_" + key[:24]
	result := broker.HostFSReadProposalResult{}
	err = p.store.WithExclusive(func() error {
		state, err := p.store.LoadState()
		if err != nil {
			return err
		}
		state.CreationTimes = compactHostFSReadCreationTimes(state.CreationTimes, now)
		if existing, ok := state.Requests[key]; ok {
			decisionStore, err := p.core.decisionStore()
			if err != nil {
				return err
			}
			d, err := decisionStore.RawDecision(existing.DecisionID)
			if err != nil {
				return fmt.Errorf("HostFS read decision state is unprovable: %w", err)
			}
			existing.State = d.State
			existing.Revision = d.Revision
			existing.TimeoutAt = d.TimeoutAt
			existing.SuppressedCount++
			existing.LastSuppressedAt = now
			state.Requests[key] = existing
			state.UpdatedAt = now
			if err := p.store.SaveState(state); err != nil {
				return err
			}
			result = hostFSReadProposalResultForDecision(d)
			return nil
		}

		pending := 0
		for _, record := range state.Requests {
			if record.State == decision.StatePending || record.State == decision.StateClaimed {
				pending++
			}
		}
		if pending >= hostFSReadPendingLimit {
			result.Code = broker.HostFSErrorReadRequestLimited
			state.UpdatedAt = now
			if err := p.store.SaveState(state); err != nil {
				return err
			}
			return p.emitLimitAudit(proposal, "pending-capacity", nil)
		}
		if len(state.CreationTimes) >= hostFSReadRateLimit {
			retry := state.CreationTimes[0].Add(hostFSReadRateWindow).Sub(now)
			ms := int64(math.Ceil(float64(retry) / float64(time.Millisecond)))
			if ms < 1 {
				ms = 1
			}
			if ms > 60_000 {
				ms = 60_000
			}
			result = broker.HostFSReadProposalResult{Code: broker.HostFSErrorReadRequestLimited, RetryAfterMS: &ms}
			state.UpdatedAt = now
			if err := p.store.SaveState(state); err != nil {
				return err
			}
			return p.emitLimitAudit(proposal, "creation-rate", &ms)
		}

		record := readgrant.RequestRecord{
			KeyDigest:        key,
			DecisionID:       decisionID,
			RequestedPath:    proposal.RequestedPath,
			CanonicalPath:    proposal.CanonicalPath,
			Operation:        readgrant.OperationRead,
			Profile:          proposal.Profile,
			Backend:          proposal.Backend,
			VisibilityRule:   grant.ID,
			VisibilitySource: string(grant.Source),
			VisibilityRoot:   grant.HostPath,
			VisibilityScope:  string(grant.Scope),
			UntrustedReason:  reason,
			State:            decision.StatePending,
			Revision:         1,
			CreatedAt:        now,
			TimeoutAt:        now.Add(hostFSReadDecisionTimeout),
		}
		state.Requests[key] = record
		state.CreationTimes = append(state.CreationTimes, now)
		state.UpdatedAt = now
		if err := p.store.SaveState(state); err != nil {
			return err
		}
		d := hostFSReadDecision(record, p.sessionID)
		created, err := p.core.CreateDecision(d)
		if err != nil {
			record.State = decision.StateFailed
			record.Revision++
			state.Requests[key] = record
			state.UpdatedAt = now
			_ = p.store.SaveState(state)
			return err
		}
		record.Revision = created.Revision
		state.Requests[key] = record
		state.UpdatedAt = now
		if err := p.store.SaveState(state); err != nil {
			return err
		}
		if p.core.LifecycleResources != nil {
			if err := p.core.LifecycleResources.RecordSessionFact(context.Background(), p.sessionID, lifecycle.FactSpec{
				Kind: lifecycle.KindDecisionRecord, ID: created.ID, Class: lifecycle.FactRetained,
			}); err != nil {
				return err
			}
		}
		result = broker.HostFSReadProposalResult{Code: broker.HostFSErrorReadApprovalRequired, DecisionRef: created.ID}
		return nil
	})
	if err != nil {
		return broker.HostFSReadProposalResult{}, err
	}
	return result, nil
}

func (p *hostFSReadProvider) emitLimitAudit(proposal broker.HostFSReadProposal, limit string, retryAfterMS *int64) error {
	details := map[string]any{
		"session":   proposal.SessionID,
		"profile":   proposal.Profile,
		"backend":   proposal.Backend,
		"operation": readgrant.OperationRead,
		"limit":     limit,
	}
	if retryAfterMS != nil {
		details["retryAfterMs"] = *retryAfterMS
	}
	return p.core.emitOperatorCenterAudit(audit.Event{
		Session:  proposal.SessionID,
		Profile:  proposal.Profile,
		Backend:  proposal.Backend,
		Action:   "hostfs.read.limit",
		Decision: "deny",
		Details:  details,
	})
}

func (p *hostFSReadProvider) AllowsRead(check hostfs.ReadGrantCheck) (bool, error) {
	if p == nil || p.store == nil {
		return false, errors.New("HostFS read grant store is unavailable")
	}
	allowed := false
	err := p.store.WithShared(func() error {
		if err := readgrant.ProbeOwner(p.ownerPath); err != nil {
			return err
		}
		manifest, err := p.store.LoadManifest()
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		now := p.now().UTC()
		grant, ok := manifest.FindActive(readgrant.OperationRead, check.RequestedPath, check.CanonicalPath, now)
		if !ok {
			return nil
		}
		if err := p.validateActiveGrant(check, grant); err != nil {
			return err
		}
		decisionStore, err := p.core.decisionStore()
		if err != nil {
			return err
		}
		d, err := decisionStore.RawDecision(grant.DecisionID)
		if err != nil {
			return err
		}
		if d.Kind != decision.KindHostFSRead || d.State != decision.StateApplied || d.Source.Session != p.sessionID || d.Revision != grant.Revision {
			return errors.New("active HostFS read grant does not match an applied decision")
		}
		allowed = true
		return nil
	})
	return allowed, err
}

func (p *hostFSReadProvider) validateProposal(proposal broker.HostFSReadProposal) (hostfs.Grant, string, error) {
	if proposal.SessionID != p.sessionID || strings.TrimSpace(proposal.Profile) == "" || strings.TrimSpace(proposal.Backend) == "" {
		return hostfs.Grant{}, "", errors.New("proposal source does not match provider")
	}
	requested, err := cleanHostFSReadPath(proposal.RequestedPath)
	if err != nil {
		return hostfs.Grant{}, "", err
	}
	canonical, err := filepath.EvalSymlinks(requested)
	if err != nil || canonical != proposal.CanonicalPath {
		return hostfs.Grant{}, "", errors.New("proposal canonical path is stale")
	}
	canonical, err = cleanHostFSReadPath(canonical)
	if err != nil {
		return hostfs.Grant{}, "", err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() {
		return hostfs.Grant{}, "", errors.New("proposal target is not a regular file")
	}
	visibility := p.policy.Visibility(requested)
	canonicalVisibility := p.policy.Visibility(canonical)
	if !visibility.ExplicitDomain || visibility.State == hostfs.VisibilityHidden || visibility.DiscoverDenied || !canonicalVisibility.ExplicitDomain || canonicalVisibility.State == hostfs.VisibilityHidden || canonicalVisibility.DiscoverDenied {
		return hostfs.Grant{}, "", fmt.Errorf("proposal is outside explicit discovery policy (requested=%+v canonical=%+v)", visibility, canonicalVisibility)
	}
	if visibility.RuleID != proposal.Visibility.RuleID || visibility.Source != proposal.Visibility.Source {
		return hostfs.Grant{}, "", errors.New("proposal visibility authority drifted")
	}
	if p.policy.Decide(hostfs.OpRead, requested).Effect == "deny" || p.policy.Decide(hostfs.OpRead, canonical).Effect == "deny" {
		return hostfs.Grant{}, "", errors.New("read policy explicitly denies proposal")
	}
	grant, ok := hostFSReadVisibilityGrant(p.policy, visibility)
	if !ok {
		return hostfs.Grant{}, "", errors.New("winning visibility grant is unavailable")
	}
	return grant, sanitizeHostFSReadReason(proposal.UntrustedReason), nil
}

func (p *hostFSReadProvider) validateActiveGrant(check hostfs.ReadGrantCheck, grant readgrant.Grant) error {
	visibility := p.policy.Visibility(check.RequestedPath)
	canonicalVisibility := p.policy.Visibility(check.CanonicalPath)
	if !visibility.ExplicitDomain || visibility.State == hostfs.VisibilityHidden || visibility.DiscoverDenied || !canonicalVisibility.ExplicitDomain || canonicalVisibility.State == hostfs.VisibilityHidden || canonicalVisibility.DiscoverDenied {
		return errors.New("grant is outside current explicit visibility policy")
	}
	if visibility.RuleID != grant.VisibilityRuleID || string(visibility.Source) != grant.VisibilitySource {
		return errors.New("grant visibility authority drifted")
	}
	if p.policy.Decide(hostfs.OpRead, check.RequestedPath).Effect == "deny" || p.policy.Decide(hostfs.OpRead, check.CanonicalPath).Effect == "deny" {
		return errors.New("current read policy denies grant")
	}
	return nil
}

func hostFSReadVisibilityGrant(policy hostfs.EffectivePolicy, visibility hostfs.VisibilityResult) (hostfs.Grant, bool) {
	for _, grant := range policy.Grants {
		if grant.ID == visibility.RuleID && grant.Source == visibility.Source && slices.Contains(grant.Ops, hostfs.OpDiscover) {
			return grant, true
		}
	}
	return hostfs.Grant{}, false
}

func hostFSReadDecision(record readgrant.RequestRecord, sessionID string) decision.Decision {
	facts := map[string]any{
		"scope":          "exact-file",
		"contentPreview": false,
		"reasonTrust":    "untrusted-target-input",
	}
	if record.UntrustedReason != "" {
		facts["untrustedReason"] = record.UntrustedReason
	}
	return decision.Decision{
		Version: decision.DecisionVersion,
		ID:      record.DecisionID,
		Kind:    decision.KindHostFSRead,
		Source: decision.Source{
			Profile: record.Profile,
			Session: sessionID,
			Backend: record.Backend,
		},
		State: decision.StatePending,
		Risk: map[string]any{
			"hostUserData": true,
			"scope":        "exact-file",
		},
		ProposedAction: map[string]any{
			"operation":        readgrant.OperationRead,
			"path":             record.RequestedPath,
			"visibilityRuleId": record.VisibilityRule,
			"visibilitySource": record.VisibilitySource,
			"lifetime":         "session",
		},
		Preview: decision.Preview{
			Summary:         "Allow this running session to read one exact host file",
			Facts:           facts,
			UserDataPresent: true,
		},
		AllowedActions: []string{decision.ActionApprove, decision.ActionDeny},
		DefaultOutcome: decision.DefaultOutcomeDeny,
		TimeoutAt:      record.TimeoutAt,
		ProviderRef: decision.ProviderRef{
			Provider:   decision.KindHostFSRead,
			DecisionID: record.DecisionID,
			SessionID:  sessionID,
		},
		AuditRef:  "audit:hostfs-read:" + record.DecisionID,
		Revision:  record.Revision,
		CreatedAt: record.CreatedAt,
	}
}

func hostFSReadProposalResultForDecision(d decision.Decision) broker.HostFSReadProposalResult {
	switch d.State {
	case decision.StatePending, decision.StateClaimed:
		return broker.HostFSReadProposalResult{Code: broker.HostFSErrorReadApprovalRequired, DecisionRef: d.ID}
	case decision.StateDenied, decision.StateTimedOut, decision.StateApplied, decision.StateFailed, decision.StateStale:
		return broker.HostFSReadProposalResult{Code: broker.HostFSErrorReadDenied}
	default:
		return broker.HostFSReadProposalResult{Code: broker.HostFSErrorBrokerUnavailable}
	}
}

func hostFSReadKey(sessionID, canonicalPath string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + canonicalPath + "\x00" + readgrant.OperationRead))
	return hex.EncodeToString(sum[:])
}

func compactHostFSReadCreationTimes(values []time.Time, now time.Time) []time.Time {
	cutoff := now.Add(-hostFSReadRateWindow)
	out := values[:0]
	for _, value := range values {
		if value.After(cutoff) {
			out = append(out, value)
		}
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return out
}

func sanitizeHostFSReadReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if !utf8.ValidString(reason) {
		return ""
	}
	reason = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, reason)
	details := audit.RedactDetails(map[string]any{"untrustedReason": reason})
	reason, _ = details["untrustedReason"].(string)
	for len([]byte(reason)) > hostFSReadReasonMaxBytes {
		_, size := utf8.DecodeLastRuneInString(reason)
		reason = reason[:len(reason)-size]
	}
	return reason
}

func cleanHostFSReadPath(path string) (string, error) {
	if strings.TrimSpace(path) != path || !filepath.IsAbs(path) || strings.HasPrefix(path, "//") || strings.Contains(path, `\`) {
		return "", errors.New("HostFS read path must be a clean absolute host path")
	}
	clean := filepath.Clean(path)
	if clean != path {
		return "", errors.New("HostFS read path must be canonical lexical form")
	}
	return clean, nil
}

func (c Core) claimHostFSRead(req DecisionClaimRequest, d decision.Decision) (decision.ClaimResponse, error) {
	providerStore, err := c.liveHostFSReadStore(d.Source.Session)
	if err != nil {
		return decision.ClaimResponse{}, err
	}
	lease, err := decisionClaimLease(req.LeaseSeconds)
	if err != nil {
		return decision.ClaimResponse{}, err
	}
	var claim decision.ClaimResponse
	var updated decision.Decision
	err = providerStore.WithExclusive(func() error {
		state, err := providerStore.LoadState()
		if err != nil {
			return err
		}
		key, record, err := hostFSReadRecordByDecision(state, d.ID)
		if err != nil {
			return err
		}
		if record.State != decision.StatePending && record.State != decision.StateClaimed {
			return fmt.Errorf("HostFS read decision %s is %s", d.ID, record.State)
		}
		decisionStore, err := c.decisionStore()
		if err != nil {
			return err
		}
		claim, updated, err = decisionStore.ClaimDecisionWithOptions(d.ID, decision.ClaimOptions{
			Surface:          req.Surface,
			Operator:         decisionClaimOperator,
			Lease:            lease,
			ExpectedRevision: req.ExpectedRevision,
			TakeoverExpired:  req.Takeover,
		})
		if err != nil {
			return err
		}
		record.State = updated.State
		record.Revision = updated.Revision
		record.TimeoutAt = updated.TimeoutAt
		state.Requests[key] = record
		state.UpdatedAt = c.decisionNow()
		return providerStore.SaveState(state)
	})
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.ClaimResponse{}, err
	}
	action := decision.ActionDecisionClaim
	phase := "claimed"
	if claim.Takeover {
		action = decision.ActionDecisionTakeover
		phase = "claim-taken-over"
	}
	if err := c.emitDecisionAudit(action, "allow", updated, map[string]any{
		"surface":          claim.Surface,
		"leaseSeconds":     claim.LeaseSeconds,
		"claimExpiresAt":   claim.ClaimExpiresAt,
		"takeover":         claim.Takeover,
		"expectedRevision": req.ExpectedRevision,
	}); err != nil {
		return decision.ClaimResponse{}, err
	}
	c.emitDecision(updated, phase, "")
	return claim, nil
}

func (c Core) approveHostFSRead(req DecisionResolveRequest, d decision.Decision) (decision.Resolution, error) {
	providerStore, err := c.liveHostFSReadStore(d.Source.Session)
	if err != nil {
		return decision.Resolution{}, err
	}
	var resolution decision.Resolution
	var updated decision.Decision
	var activationErr error
	var lifecycleRef lifecycle.ResourceRef
	if c.LifecycleResources != nil {
		lifecycleRef, err = c.LifecycleResources.RegisterSessionResource(context.Background(), d.Source.Session, lifecycle.RegistrationSpec{
			Kind: lifecycle.KindHostFSLiveGrant, ID: d.ID,
			OwnerKind: "manager", OwnerID: d.Source.Session, State: lifecycle.StateStarting,
			Dependencies: []lifecycle.DependencySpec{
				{Ref: lifecycle.ResourceRef{Kind: lifecycle.KindHostFSReadProvider, ID: d.Source.Session}, StopMode: lifecycle.StopModeDrain},
				{Ref: lifecycle.ResourceRef{Kind: lifecycle.KindRunSession, ID: d.Source.Session}, StopMode: lifecycle.StopModeDrain},
			},
			Persistence: lifecycle.PersistenceEphemeral, ClosePolicy: lifecycle.ClosePreStopDrain,
			PossibleVMDependency: true,
		})
		if err != nil {
			return decision.Resolution{}, err
		}
	}
	lifecycleCommitted := false
	defer func() {
		if !lifecycleCommitted && lifecycleRef.Kind != "" {
			_ = c.LifecycleResources.ReleaseSessionResource(context.Background(), d.Source.Session, lifecycleRef, activationErr)
		}
	}()
	err = providerStore.WithExclusive(func() error {
		state, err := providerStore.LoadState()
		if err != nil {
			return err
		}
		key, record, err := hostFSReadRecordByDecision(state, d.ID)
		if err != nil {
			return err
		}
		if record.State != decision.StateClaimed {
			return fmt.Errorf("HostFS read decision %s is not claimed", d.ID)
		}
		policy, err := c.hostFSReadPolicyForRecord(record)
		if err != nil {
			return err
		}
		if err := validateHostFSReadRecord(record, d, policy); err != nil {
			return err
		}
		decisionStore, err := c.decisionStore()
		if err != nil {
			return err
		}
		if _, err := decisionStore.ValidateDecisionClaim(d.ID, req.ClaimToken); err != nil {
			return err
		}
		previous, hadPrevious, err := loadHostFSReadManifest(providerStore, d.Source.Session)
		if err != nil {
			return err
		}
		resolution, updated, err = decisionStore.ResolveDecision(d.ID, req.ClaimToken, decision.StateApplied, "allow", req.Reason, map[string]any{
			"activated": true,
			"scope":     "exact-file",
		})
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		record.State = updated.State
		record.Revision = updated.Revision
		state.Requests[key] = record
		state.UpdatedAt = now
		if err := providerStore.SaveState(state); err != nil {
			activationErr = err
			return failHostFSReadActivation(c, providerStore, state, key, updated, err)
		}
		manifest := nextHostFSReadManifest(previous, hadPrevious, d.Source.Session, record, updated.Revision, now)
		if err := providerStore.SaveManifest(manifest); err != nil {
			activationErr = err
			return failHostFSReadActivation(c, providerStore, state, key, updated, err)
		}
		if lifecycleRef.Kind != "" {
			if err := c.LifecycleResources.TransitionSessionResource(context.Background(), d.Source.Session, lifecycleRef, lifecycle.StateActive); err != nil {
				activationErr = err
				if restoreErr := restoreHostFSReadManifest(providerStore, previous, hadPrevious); restoreErr != nil {
					activationErr = errors.Join(activationErr, restoreErr)
				}
				return failHostFSReadActivation(c, providerStore, state, key, updated, activationErr)
			}
		}
		if err := c.emitDecisionAudit(decision.ActionDecisionApply, "allow", updated, map[string]any{
			"reason":           req.Reason,
			"operation":        readgrant.OperationRead,
			"visibilityRuleId": record.VisibilityRule,
			"visibilitySource": record.VisibilitySource,
			"grantGeneration":  manifest.Generation,
			"suppressedCount":  record.SuppressedCount,
		}); err != nil {
			activationErr = err
			if restoreErr := restoreHostFSReadManifest(providerStore, previous, hadPrevious); restoreErr != nil {
				activationErr = errors.Join(activationErr, restoreErr)
			}
			return failHostFSReadActivation(c, providerStore, state, key, updated, activationErr)
		}
		resolution.ProviderResult["generation"] = manifest.Generation
		resolution.ProviderResult["expiresAt"] = now.Add(hostFSReadGrantLifetime)
		lifecycleCommitted = true
		return nil
	})
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error(), "activationFailed": activationErr != nil})
		return decision.Resolution{}, err
	}
	c.emitDecision(updated, decision.StateApplied, req.Reason)
	return resolution, nil
}

func (c Core) denyHostFSRead(req DecisionResolveRequest, d decision.Decision) (decision.Resolution, error) {
	providerStore, err := c.liveHostFSReadStore(d.Source.Session)
	if err != nil {
		return decision.Resolution{}, err
	}
	var resolution decision.Resolution
	var updated decision.Decision
	err = providerStore.WithExclusive(func() error {
		state, err := providerStore.LoadState()
		if err != nil {
			return err
		}
		key, record, err := hostFSReadRecordByDecision(state, d.ID)
		if err != nil {
			return err
		}
		decisionStore, err := c.decisionStore()
		if err != nil {
			return err
		}
		resolution, updated, err = decisionStore.ResolveDecision(d.ID, req.ClaimToken, decision.StateDenied, "deny", req.Reason, nil)
		if err != nil {
			return err
		}
		record.State = updated.State
		record.Revision = updated.Revision
		state.Requests[key] = record
		state.UpdatedAt = time.Now().UTC()
		return providerStore.SaveState(state)
	})
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.Resolution{}, err
	}
	details := c.hostFSReadAuditDetails(updated)
	details["reason"] = req.Reason
	if err := c.emitDecisionAudit(decision.ActionDecisionDeny, "deny", updated, details); err != nil {
		return decision.Resolution{}, err
	}
	c.emitDecision(updated, decision.StateDenied, req.Reason)
	return resolution, nil
}

func (c Core) ReopenDecision(req HostFSReadReopenRequest) (decision.Resolution, error) {
	if req.ExpectedVersion != "" && req.ExpectedVersion != decision.DecisionVersion {
		return decision.Resolution{}, errors.New("invalid decision version")
	}
	d, err := c.inspectDecisionPrivate(strings.TrimSpace(req.DecisionID))
	if err != nil {
		return decision.Resolution{}, err
	}
	if d.Kind != decision.KindHostFSRead {
		return decision.Resolution{}, fmt.Errorf("decision kind %q does not support reopen", d.Kind)
	}
	providerStore, err := c.liveHostFSReadStore(d.Source.Session)
	if err != nil {
		return decision.Resolution{}, err
	}
	var resolution decision.Resolution
	var updated decision.Decision
	err = providerStore.WithExclusive(func() error {
		state, err := providerStore.LoadState()
		if err != nil {
			return err
		}
		key, record, err := hostFSReadRecordByDecision(state, d.ID)
		if err != nil {
			return err
		}
		if record.State != decision.StateDenied && record.State != decision.StateTimedOut {
			return fmt.Errorf("HostFS read decision %s state %q is not reopenable", d.ID, record.State)
		}
		policy, err := c.hostFSReadPolicyForRecord(record)
		if err != nil {
			return err
		}
		if err := validateHostFSReadRecord(record, d, policy); err != nil {
			return err
		}
		decisionStore, err := c.decisionStore()
		if err != nil {
			return err
		}
		resolution, updated, err = decisionStore.ReopenProviderDecision(d.ID, decision.KindHostFSRead, hostFSReadDecisionTimeout, req.Reason)
		if err != nil {
			return err
		}
		record.State = updated.State
		record.Revision = updated.Revision
		record.TimeoutAt = updated.TimeoutAt
		state.Requests[key] = record
		state.UpdatedAt = time.Now().UTC()
		return providerStore.SaveState(state)
	})
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.Resolution{}, err
	}
	details := c.hostFSReadAuditDetails(updated)
	details["reason"] = req.Reason
	if err := c.emitDecisionAudit(decision.ActionDecisionReopen, "allow", updated, details); err != nil {
		return decision.Resolution{}, err
	}
	c.emitDecision(updated, decision.StatePending, req.Reason)
	return resolution, nil
}

func (c Core) syncHostFSReadDecision(d decision.Decision) error {
	if d.Kind != decision.KindHostFSRead || d.Source.Session == "" {
		return nil
	}
	providerStore, err := c.liveHostFSReadStore(d.Source.Session)
	if err != nil {
		if errors.Is(err, readgrant.ErrSessionNotLive) || errors.Is(err, readgrant.ErrSessionUnprovable) || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return providerStore.WithExclusive(func() error {
		state, err := providerStore.LoadState()
		if err != nil {
			return err
		}
		key, record, err := hostFSReadRecordByDecision(state, d.ID)
		if err != nil {
			return err
		}
		record.State = d.State
		record.Revision = d.Revision
		record.TimeoutAt = d.TimeoutAt
		state.Requests[key] = record
		state.UpdatedAt = time.Now().UTC()
		return providerStore.SaveState(state)
	})
}

func (c Core) hostFSReadAuditDetails(d decision.Decision) map[string]any {
	details := map[string]any{"operation": readgrant.OperationRead, "revision": d.Revision}
	providerStore, err := c.openHostFSReadStore(d.Source.Session)
	if err != nil {
		return details
	}
	_ = providerStore.WithShared(func() error {
		state, err := providerStore.LoadState()
		if err != nil {
			return err
		}
		_, record, err := hostFSReadRecordByDecision(state, d.ID)
		if err != nil {
			return err
		}
		details["visibilityRuleId"] = record.VisibilityRule
		details["visibilitySource"] = record.VisibilitySource
		details["suppressedCount"] = record.SuppressedCount
		return nil
	})
	return details
}

func (c Core) openHostFSReadStore(sessionID string) (*readgrant.Store, error) {
	if c.Store.Root == "" {
		return nil, errors.New("manager store root is required")
	}
	if !session.ValidID(sessionID) {
		return nil, errors.New("valid HostFS read session id is required")
	}
	return readgrant.OpenStore(filepath.Join(c.Store.Root, "sessions", sessionID, "hostfs-read"), sessionID)
}

func (c Core) liveHostFSReadStore(sessionID string) (*readgrant.Store, error) {
	if err := readgrant.ProbeOwner(c.hostFSReadOwnerPath(sessionID)); err != nil {
		return nil, fmt.Errorf("HostFS read session liveness is unprovable: %w", err)
	}
	return c.openHostFSReadStore(sessionID)
}

func (c Core) hostFSReadOwnerPath(sessionID string) string {
	return filepath.Join(c.Store.Root, "sessions", sessionID, "hostfs-read", "owner.lock")
}

func (c Core) hostFSReadPolicyForRecord(record readgrant.RequestRecord) (hostfs.EffectivePolicy, error) {
	rule := hostfs.Rule{
		ID:       record.VisibilityRule,
		HostPath: record.VisibilityRoot,
		Ops:      []hostfs.Op{hostfs.OpDiscover},
		Scope:    hostfs.Scope(record.VisibilityScope),
		Reason:   "immutable session visibility snapshot",
	}
	input := hostfs.BuildInput{StoreRoot: c.Store.Root}
	switch hostfs.Source(record.VisibilitySource) {
	case hostfs.SourceProfile:
		profileValue, err := c.Store.Load(record.Profile)
		if err != nil {
			return hostfs.EffectivePolicy{}, err
		}
		input.Profile = profileValue.HostFS
	case hostfs.SourceEnvironment:
		input.Environment = hostfs.Config{Grants: []hostfs.Rule{rule}}
	case hostfs.SourceRun:
		input.Run = hostfs.Config{Grants: []hostfs.Rule{rule}}
	default:
		return hostfs.EffectivePolicy{}, errors.New("unsupported HostFS read visibility source")
	}
	policy, err := hostfs.Build(input)
	if err != nil {
		return hostfs.EffectivePolicy{}, err
	}
	return hostfs.NewService(policy).Policy, nil
}

func validateHostFSReadRecord(record readgrant.RequestRecord, d decision.Decision, policy hostfs.EffectivePolicy) error {
	if d.Kind != decision.KindHostFSRead || d.ID != record.DecisionID || d.Source.Profile != record.Profile || d.Source.Backend != record.Backend {
		return errors.New("HostFS read decision/provider identity mismatch")
	}
	canonical, err := filepath.EvalSymlinks(record.RequestedPath)
	if err != nil || canonical != record.CanonicalPath {
		return errors.New("HostFS read target canonical path changed")
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("HostFS read target is no longer a regular file")
	}
	visibility := policy.Visibility(record.RequestedPath)
	canonicalVisibility := policy.Visibility(record.CanonicalPath)
	if !visibility.ExplicitDomain || visibility.State == hostfs.VisibilityHidden || visibility.DiscoverDenied || !canonicalVisibility.ExplicitDomain || canonicalVisibility.State == hostfs.VisibilityHidden || canonicalVisibility.DiscoverDenied {
		return errors.New("HostFS read visibility is no longer allowed")
	}
	if visibility.RuleID != record.VisibilityRule || string(visibility.Source) != record.VisibilitySource {
		return errors.New("HostFS read visibility rule drifted")
	}
	if policy.Decide(hostfs.OpRead, record.RequestedPath).Effect == "deny" || policy.Decide(hostfs.OpRead, record.CanonicalPath).Effect == "deny" {
		return errors.New("HostFS read is explicitly denied by current policy")
	}
	return nil
}

func hostFSReadRecordByDecision(state readgrant.ProviderState, decisionID string) (string, readgrant.RequestRecord, error) {
	for key, record := range state.Requests {
		if record.DecisionID == decisionID {
			return key, record, nil
		}
	}
	return "", readgrant.RequestRecord{}, fmt.Errorf("HostFS read provider record for %s is missing", decisionID)
}

func loadHostFSReadManifest(store *readgrant.Store, sessionID string) (readgrant.Manifest, bool, error) {
	manifest, err := store.LoadManifest()
	if errors.Is(err, os.ErrNotExist) {
		return readgrant.Manifest{Version: readgrant.GrantVersion, SessionID: sessionID, Grants: []readgrant.Grant{}}, false, nil
	}
	if err != nil {
		return readgrant.Manifest{}, false, err
	}
	return manifest, true, nil
}

func nextHostFSReadManifest(previous readgrant.Manifest, hadPrevious bool, sessionID string, record readgrant.RequestRecord, revision int, now time.Time) readgrant.Manifest {
	generation := uint64(1)
	grants := []readgrant.Grant{}
	if hadPrevious {
		generation = previous.Generation + 1
		for _, grant := range previous.Grants {
			if now.Before(grant.ExpiresAt) && grant.DecisionID != record.DecisionID {
				grants = append(grants, grant)
			}
		}
	}
	grants = append(grants, readgrant.Grant{
		DecisionID:       record.DecisionID,
		Revision:         revision,
		Operation:        readgrant.OperationRead,
		RequestedPath:    record.RequestedPath,
		CanonicalPath:    record.CanonicalPath,
		VisibilityRuleID: record.VisibilityRule,
		VisibilitySource: record.VisibilitySource,
		IssuedAt:         now,
		ExpiresAt:        now.Add(hostFSReadGrantLifetime),
	})
	return readgrant.Manifest{Version: readgrant.GrantVersion, SessionID: sessionID, Generation: generation, UpdatedAt: now, Grants: grants}
}

func restoreHostFSReadManifest(store *readgrant.Store, previous readgrant.Manifest, hadPrevious bool) error {
	if hadPrevious {
		return store.SaveManifest(previous)
	}
	err := os.Remove(store.GrantsPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func failHostFSReadActivation(core Core, providerStore *readgrant.Store, state readgrant.ProviderState, key string, applied decision.Decision, cause error) error {
	decisionStore, err := core.decisionStore()
	if err != nil {
		return errors.Join(cause, err)
	}
	failed, failErr := decisionStore.FailAppliedProviderDecision(applied.ID, decision.KindHostFSRead)
	record := state.Requests[key]
	if failErr == nil {
		record.State = failed.State
		record.Revision = failed.Revision
		state.Requests[key] = record
		state.UpdatedAt = time.Now().UTC()
		failErr = providerStore.SaveState(state)
	}
	_ = core.emitDecisionAudit(decision.ActionDecisionStale, "deny", applied, map[string]any{"activationFailed": true, "reason": "provider-activation-failed"})
	return errors.Join(cause, failErr)
}
