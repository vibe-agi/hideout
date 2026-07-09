package manager

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/hostfs/overlay"
)

type DecisionListRequest struct {
	Kind            string
	State           string
	Profile         string
	Session         string
	IncludeTerminal bool
}

type DecisionClaimRequest struct {
	DecisionID      string `json:"decisionId"`
	ExpectedVersion string `json:"expectedVersion"`
	Surface         string `json:"surface,omitempty"`
}

type DecisionResolveRequest struct {
	DecisionID      string `json:"decisionId"`
	ExpectedVersion string `json:"expectedVersion"`
	ClaimToken      string `json:"claimToken"`
	Reason          string `json:"reason,omitempty"`
}

type NoticeListRequest struct {
	Kind     string
	Profile  string
	Session  string
	Severity string
}

type NoticeAckRequest struct {
	NoticeID string `json:"noticeId"`
	Surface  string `json:"surface,omitempty"`
}

type DecisionStatus = decision.Status

func (c Core) decisionStore() (*decision.Store, error) {
	if c.Store.Root == "" {
		return nil, errors.New("manager store root is required")
	}
	return decision.NewStore(c.Store.Root), nil
}

func (c Core) ListDecisions(req DecisionListRequest) ([]decision.Decision, error) {
	if err := c.syncHostFSWriteDecisions(); err != nil {
		return nil, err
	}
	if err := c.expireDecisionTimeouts(); err != nil {
		return nil, err
	}
	store, err := c.decisionStore()
	if err != nil {
		return nil, err
	}
	return store.Decisions(decision.ListFilter{
		Kind:            strings.TrimSpace(req.Kind),
		State:           strings.TrimSpace(req.State),
		Profile:         strings.TrimSpace(req.Profile),
		Session:         strings.TrimSpace(req.Session),
		IncludeTerminal: req.IncludeTerminal,
	})
}

func (c Core) InspectDecision(id string) (decision.Decision, error) {
	if err := c.syncHostFSWriteDecisions(); err != nil {
		return decision.Decision{}, err
	}
	if err := c.expireDecisionTimeouts(); err != nil {
		return decision.Decision{}, err
	}
	store, err := c.decisionStore()
	if err != nil {
		return decision.Decision{}, err
	}
	return store.Decision(id)
}

func (c Core) inspectDecisionPrivate(id string) (decision.Decision, error) {
	if err := c.syncHostFSWriteDecisions(); err != nil {
		return decision.Decision{}, err
	}
	if err := c.expireDecisionTimeouts(); err != nil {
		return decision.Decision{}, err
	}
	store, err := c.decisionStore()
	if err != nil {
		return decision.Decision{}, err
	}
	return store.RawDecision(id)
}

func (c Core) CreateDecision(d decision.Decision) (decision.Decision, error) {
	store, err := c.decisionStore()
	if err != nil {
		return decision.Decision{}, err
	}
	out, err := store.CreateOrUpdateDecision(d)
	if err != nil {
		return decision.Decision{}, err
	}
	if err := c.emitDecisionAudit(decision.ActionDecisionCreate, "allow", out, nil); err != nil {
		return decision.Decision{}, err
	}
	c.emitDecision(out, "created", "")
	return out, nil
}

func (c Core) ClaimDecision(req DecisionClaimRequest) (decision.ClaimResponse, error) {
	if req.ExpectedVersion != "" && req.ExpectedVersion != decision.DecisionVersion {
		return decision.ClaimResponse{}, errors.New("invalid decision version")
	}
	d, err := c.inspectDecisionPrivate(req.DecisionID)
	if err != nil {
		return decision.ClaimResponse{}, err
	}
	if decision.DecisionTerminal(d.State) {
		err := fmt.Errorf("decision %s is %s", d.ID, d.State)
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.ClaimResponse{}, err
	}
	if d.Kind == decision.KindHostFSWrite {
		claim, err := c.ClaimHostFSWrite(HostFSWriteClaimRequest{
			DecisionID:      d.ProviderRef.DecisionID,
			ExpectedVersion: HostFSWritePlanVersion,
			Surface:         req.Surface,
		})
		if err != nil {
			_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
			return decision.ClaimResponse{}, err
		}
		_ = c.syncHostFSWriteDecisions()
		return decision.ClaimResponse{
			Version:        decision.DecisionClaimVersion,
			DecisionID:     d.ID,
			State:          decision.StateClaimed,
			ClaimToken:     claim.ClaimToken,
			ClaimExpiresAt: claim.ClaimExpiresAt,
			Revision:       d.Revision + 1,
		}, nil
	}
	store, err := c.decisionStore()
	if err != nil {
		return decision.ClaimResponse{}, err
	}
	claim, updated, err := store.ClaimDecision(req.DecisionID, req.Surface, time.Minute)
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.ClaimResponse{}, err
	}
	if err := c.emitDecisionAudit(decision.ActionDecisionClaim, "allow", updated, map[string]any{"surface": req.Surface}); err != nil {
		return decision.ClaimResponse{}, err
	}
	c.emitDecision(updated, "claimed", "")
	return claim, nil
}

func (c Core) ApproveDecision(req DecisionResolveRequest) (decision.Resolution, error) {
	return c.resolveDecision(req, true)
}

func (c Core) DenyDecision(req DecisionResolveRequest) (decision.Resolution, error) {
	return c.resolveDecision(req, false)
}

func (c Core) resolveDecision(req DecisionResolveRequest, approve bool) (decision.Resolution, error) {
	if req.ExpectedVersion != "" && req.ExpectedVersion != decision.DecisionVersion {
		return decision.Resolution{}, errors.New("invalid decision version")
	}
	d, err := c.inspectDecisionPrivate(req.DecisionID)
	if err != nil {
		return decision.Resolution{}, err
	}
	if decision.DecisionTerminal(d.State) {
		err := fmt.Errorf("decision %s is %s", d.ID, d.State)
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.Resolution{}, err
	}
	if d.Kind == decision.KindHostFSWrite {
		if approve {
			result, applyErr := c.ApplyHostFSWrite(HostFSWriteApplyRequest{
				DecisionID:      d.ProviderRef.DecisionID,
				ExpectedVersion: HostFSWritePlanVersion,
				ClaimToken:      req.ClaimToken,
			})
			_ = c.syncHostFSWriteDecisions()
			res := hostFSWriteResolution(d, result, applyErr)
			if applyErr != nil {
				return res, applyErr
			}
			return res, nil
		}
		result, discardErr := c.DiscardHostFSWrite(HostFSWriteDiscardRequest{
			DecisionID:      d.ProviderRef.DecisionID,
			ExpectedVersion: HostFSWritePlanVersion,
			ClaimToken:      req.ClaimToken,
			Reason:          req.Reason,
		})
		_ = c.syncHostFSWriteDecisions()
		res := hostFSWriteResolution(d, result, discardErr)
		if discardErr != nil {
			return res, discardErr
		}
		return res, nil
	}
	if d.Kind == decision.KindEvidenceShare && approve {
		return c.approveEvidenceShareDecision(req, d)
	}
	store, err := c.decisionStore()
	if err != nil {
		return decision.Resolution{}, err
	}
	state := decision.StateDenied
	decisionValue := "deny"
	action := decision.ActionDecisionDeny
	if approve {
		return decision.Resolution{}, fmt.Errorf("decision kind %q has no promoted provider", d.Kind)
	}
	res, updated, err := store.ResolveDecision(req.DecisionID, req.ClaimToken, state, decisionValue, req.Reason, nil)
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.Resolution{}, err
	}
	if err := c.emitDecisionAudit(action, decisionValue, updated, map[string]any{"reason": req.Reason}); err != nil {
		return decision.Resolution{}, err
	}
	c.emitDecision(updated, state, req.Reason)
	return res, nil
}

func (c Core) approveEvidenceShareDecision(req DecisionResolveRequest, d decision.Decision) (decision.Resolution, error) {
	store, err := c.decisionStore()
	if err != nil {
		return decision.Resolution{}, err
	}
	if _, err := store.ValidateDecisionClaim(req.DecisionID, req.ClaimToken); err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.Resolution{}, err
	}
	opts, err := c.loadShareExportRequest(req.DecisionID)
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.Resolution{}, err
	}
	plan, err := c.PlanExport(opts)
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.Resolution{}, err
	}
	result, err := c.ApplyExport(plan, opts)
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.Resolution{}, err
	}
	res, updated, err := store.ResolveDecision(req.DecisionID, req.ClaimToken, decision.StateApplied, "allow", req.Reason, map[string]any{
		"artifactPath":  result.ArtifactPath,
		"recordCount":   result.RecordCount,
		"metaAuditPath": result.MetaAuditPath,
	})
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{"reason": err.Error()})
		return decision.Resolution{}, err
	}
	if err := c.emitDecisionAudit(decision.ActionDecisionApply, "allow", updated, map[string]any{"reason": req.Reason, "artifactPath": result.ArtifactPath}); err != nil {
		return decision.Resolution{}, err
	}
	c.emitDecision(updated, decision.StateApplied, req.Reason)
	return res, nil
}

func (c Core) CreateNotice(n decision.Notice) (decision.Notice, error) {
	store, err := c.decisionStore()
	if err != nil {
		return decision.Notice{}, err
	}
	out, err := store.CreateOrUpdateNotice(n)
	if err != nil {
		return decision.Notice{}, err
	}
	if err := c.emitNoticeAudit(decision.ActionNoticeCreate, "allow", out, nil); err != nil {
		return decision.Notice{}, err
	}
	c.emitNotice(out, "created")
	return out, nil
}

func (c Core) ListNotices(req NoticeListRequest) ([]decision.Notice, error) {
	store, err := c.decisionStore()
	if err != nil {
		return nil, err
	}
	return store.Notices(decision.ListFilter{
		Kind:     strings.TrimSpace(req.Kind),
		Profile:  strings.TrimSpace(req.Profile),
		Session:  strings.TrimSpace(req.Session),
		Severity: strings.TrimSpace(req.Severity),
	})
}

func (c Core) InspectNotice(id string) (decision.Notice, error) {
	store, err := c.decisionStore()
	if err != nil {
		return decision.Notice{}, err
	}
	return store.Notice(id)
}

func (c Core) AckNotice(req NoticeAckRequest) (decision.Acknowledgement, error) {
	store, err := c.decisionStore()
	if err != nil {
		return decision.Acknowledgement{}, err
	}
	ack, n, err := store.AckNotice(req.NoticeID, req.Surface)
	if err != nil {
		return decision.Acknowledgement{}, err
	}
	if err := c.emitNoticeAudit(decision.ActionNoticeAck, "allow", n, map[string]any{"surface": req.Surface}); err != nil {
		return decision.Acknowledgement{}, err
	}
	c.emitNotice(n, "acknowledged")
	return ack, nil
}

func (c Core) DecisionStatus() (DecisionStatus, error) {
	if err := c.syncHostFSWriteDecisions(); err != nil {
		return DecisionStatus{}, err
	}
	if err := c.expireDecisionTimeouts(); err != nil {
		return DecisionStatus{}, err
	}
	store, err := c.decisionStore()
	if err != nil {
		return DecisionStatus{}, err
	}
	return store.Status()
}

func (c Core) expireDecisionTimeouts() error {
	store, err := c.decisionStore()
	if err != nil {
		return err
	}
	timedOut, err := store.TimeoutExpiredDecisions(time.Time{})
	if err != nil {
		return err
	}
	for _, d := range timedOut {
		if err := c.emitDecisionAudit(decision.ActionDecisionTimeout, "deny", d, map[string]any{"reason": "decision timed out"}); err != nil {
			return err
		}
		c.emitDecision(d, decision.StateTimedOut, "decision timed out")
	}
	return nil
}

func (c Core) syncHostFSWriteDecisions() error {
	refs, err := c.listHostFSWriteRefs()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if _, err := c.upsertHostFSWriteDecision(ref); err != nil {
			return err
		}
	}
	return nil
}

func (c Core) upsertHostFSWriteDecision(ref hostFSWriteRef) (decision.Decision, error) {
	store, err := c.decisionStore()
	if err != nil {
		return decision.Decision{}, err
	}
	state := hostFSDecisionState(ref.decision.State)
	d := decision.Decision{
		ID:   ref.decision.DecisionID,
		Kind: decision.KindHostFSWrite,
		Source: decision.Source{
			Profile: ref.operation.Profile,
			Session: ref.sessionID,
			Backend: ref.operation.Backend,
		},
		State: state,
		Risk: map[string]any{
			"privilegeStatus": ref.decision.Privilege.Status,
			"warnings":        ref.decision.Warnings,
		},
		ProposedAction: map[string]any{
			"operation":       ref.decision.Operation,
			"path":            ref.decision.Path,
			"destinationPath": ref.decision.DestinationPath,
		},
		Preview: decision.Preview{
			Summary: ref.decision.Preview.Summary,
			Facts: map[string]any{
				"operation":       ref.decision.Operation,
				"path":            ref.decision.Path,
				"destinationPath": ref.decision.DestinationPath,
				"previewKind":     ref.decision.Preview.Kind,
			},
		},
		AllowedActions: []string{decision.ActionApply, decision.ActionDiscard},
		DefaultOutcome: decision.DefaultOutcomeDiscard,
		TimeoutAt:      ref.decision.TimeoutAt,
		ProviderRef: decision.ProviderRef{
			Provider:    decision.KindHostFSWrite,
			OperationID: ref.operation.ID,
			DecisionID:  ref.decision.DecisionID,
			SessionID:   ref.sessionID,
		},
		AuditRef:  "audit:hostfs-write:" + ref.decision.DecisionID,
		CreatedAt: ref.operation.CreatedAt,
		UpdatedAt: ref.decision.UpdatedAt,
	}
	if ref.decision.Claim != nil && state == decision.StateClaimed {
		d.Claim = &decision.Claim{
			Surface:   ref.decision.Claim.Surface,
			Operator:  ref.decision.Claim.Operator,
			ClaimedAt: ref.decision.Claim.ClaimedAt,
			ExpiresAt: ref.decision.Claim.ExpiresAt,
			TokenHash: ref.decision.Claim.TokenHash,
		}
	}
	return store.CreateOrUpdateDecision(d)
}

func hostFSDecisionState(state string) string {
	switch state {
	case overlay.StatePending:
		return decision.StatePending
	case overlay.StateClaimed:
		return decision.StateClaimed
	case overlay.StateApplied:
		return decision.StateApplied
	case overlay.StateDiscarded:
		return decision.StateDiscarded
	case overlay.StateExpired:
		return decision.StateTimedOut
	case overlay.StateConflict, overlay.StateFailed:
		return decision.StateFailed
	case overlay.StateDenied:
		return decision.StateDenied
	default:
		return decision.StateStale
	}
}

func hostFSWriteResolution(d decision.Decision, result HostFSWriteResult, err error) decision.Resolution {
	state := hostFSDecisionState(result.Status)
	decisionValue := result.Decision
	reason := result.ConflictReason
	if err != nil && reason == "" {
		reason = err.Error()
	}
	return decision.RedactResolution(decision.Resolution{
		Version:    decision.DecisionResultVersion,
		DecisionID: d.ID,
		Kind:       d.Kind,
		Status:     state,
		Decision:   decisionValue,
		Reason:     reason,
		ProviderResult: map[string]any{
			"operationId":              result.OperationID,
			"changedPaths":             result.ChangedPaths,
			"partialMutationPrevented": result.PartialMutationPrevented,
			"privilegeStatus":          result.Privilege.Status,
		},
		AuditRef: d.AuditRef,
		Revision: d.Revision + 1,
	})
}

func (c Core) emitDecision(d decision.Decision, phase, reason string) {
	details := map[string]any{
		"id":             d.ID,
		"decisionId":     d.ID,
		"kind":           d.Kind,
		"state":          d.State,
		"status":         d.State,
		"profile":        d.Source.Profile,
		"session":        d.Source.Session,
		"backend":        d.Source.Backend,
		"defaultOutcome": d.DefaultOutcome,
		"preview":        d.Preview,
		"reason":         reason,
	}
	c.emitOperation("decision", phase, details)
}

func (c Core) emitNotice(n decision.Notice, phase string) {
	c.emitOperation("notice", phase, map[string]any{
		"id":           n.ID,
		"noticeId":     n.ID,
		"kind":         n.Kind,
		"status":       n.Status,
		"severity":     n.Severity,
		"profile":      n.Source.Profile,
		"session":      n.Source.Session,
		"backend":      n.Source.Backend,
		"acknowledged": n.Acknowledged,
		"preview":      n.Preview,
	})
}

func (c Core) emitDecisionAudit(action, auditDecision string, d decision.Decision, details map[string]any) error {
	return c.emitOperatorCenterAudit(decision.DecisionEvent(action, auditDecision, d, details))
}

func (c Core) emitNoticeAudit(action, auditDecision string, n decision.Notice, details map[string]any) error {
	return c.emitOperatorCenterAudit(decision.NoticeEvent(action, auditDecision, n, details))
}

func (c Core) emitOperatorCenterAudit(ev audit.Event) error {
	if c.Store.Root == "" {
		return errors.New("manager store root is required")
	}
	path := filepath.Join(c.Store.Root, "operator-center", "audit.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	aw, err := audit.NewFile(path)
	if err != nil {
		return err
	}
	defer aw.Close()
	return aw.Emit(ev)
}
