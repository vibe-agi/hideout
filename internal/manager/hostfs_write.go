package manager

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/hostfs/overlay"
)

const (
	HostFSWritePlanVersion   = overlay.PlanVersion
	HostFSWriteResultVersion = overlay.ResultVersion
	HostFSWriteStatusVersion = overlay.StatusVersion
)

type HostFSWritePlanRequest struct {
	OperationID    string `json:"operationId"`
	IncludePreview bool   `json:"includePreview,omitempty"`
}

type HostFSWriteClaimRequest struct {
	DecisionID      string `json:"decisionId"`
	ExpectedVersion string `json:"expectedVersion"`
	Surface         string `json:"surface"`
}

type HostFSWriteApplyRequest struct {
	DecisionID      string `json:"decisionId"`
	ClaimToken      string `json:"claimToken"`
	ExpectedVersion string `json:"expectedVersion"`
}

type HostFSWriteDiscardRequest struct {
	DecisionID      string `json:"decisionId"`
	ClaimToken      string `json:"claimToken"`
	ExpectedVersion string `json:"expectedVersion"`
	Reason          string `json:"reason,omitempty"`
}

type HostFSWriteStatusRequest struct {
	Session string `json:"session,omitempty"`
	Profile string `json:"profile,omitempty"`
	State   string `json:"state,omitempty"`
}

type HostFSWritePlan = overlay.Decision
type HostFSWriteClaim = overlay.ClaimResponse
type HostFSWriteResult = overlay.Result

type HostFSWriteStatus struct {
	Version string                `json:"version"`
	Pending []overlay.StatusEntry `json:"pending"`
}

type hostFSWriteRef struct {
	sessionID string
	store     *overlay.Store
	operation overlay.Operation
	decision  overlay.Decision
}

func (c Core) PlanHostFSWrite(req HostFSWritePlanRequest) (HostFSWritePlan, error) {
	opID := strings.TrimSpace(req.OperationID)
	if opID == "" {
		return HostFSWritePlan{}, errors.New("operationId is required")
	}
	ref, err := c.findHostFSWriteByOperation(opID)
	if err != nil {
		return HostFSWritePlan{}, err
	}
	if _, err := c.upsertHostFSWriteDecision(ref); err != nil {
		return HostFSWritePlan{}, err
	}
	decision := publicHostFSWriteDecision(ref.decision)
	if !req.IncludePreview {
		decision.Preview = overlay.Preview{}
	}
	return decision, nil
}

func (c Core) ClaimHostFSWrite(req HostFSWriteClaimRequest) (HostFSWriteClaim, error) {
	if err := validateHostFSWriteExpectedVersion(req.ExpectedVersion); err != nil {
		return HostFSWriteClaim{}, err
	}
	decisionID := strings.TrimSpace(req.DecisionID)
	if decisionID == "" {
		return HostFSWriteClaim{}, errors.New("decisionId is required")
	}
	surface := strings.TrimSpace(req.Surface)
	if surface == "" {
		surface = "manager-client"
	}
	ref, err := c.findHostFSWriteByDecision(decisionID)
	if err != nil {
		return HostFSWriteClaim{}, err
	}
	now := time.Now().UTC()
	if hostFSWriteDecisionVisible(ref.decision.State) && !ref.decision.TimeoutAt.IsZero() && !now.Before(ref.decision.TimeoutAt) {
		if err := c.expireHostFSWriteRef(ref); err != nil {
			return HostFSWriteClaim{}, err
		}
		return HostFSWriteClaim{}, fmt.Errorf("HostFS write decision %s expired", decisionID)
	}
	if ref.decision.State != overlay.StatePending {
		if ref.decision.State == overlay.StateClaimed && ref.decision.Claim != nil && now.After(ref.decision.Claim.ExpiresAt) {
			// Stale claim can be replaced.
		} else {
			return HostFSWriteClaim{}, fmt.Errorf("HostFS write decision %s is %s", decisionID, ref.decision.State)
		}
	}
	token, err := newClaimToken()
	if err != nil {
		return HostFSWriteClaim{}, err
	}
	ref.decision.State = overlay.StateClaimed
	ref.decision.Claim = &overlay.Claim{
		Surface:   surface,
		ClaimedAt: now,
		ExpiresAt: now.Add(time.Minute),
		TokenHash: hashClaimToken(token),
	}
	if err := ref.store.SaveDecision(ref.decision); err != nil {
		return HostFSWriteClaim{}, err
	}
	if _, err := c.upsertHostFSWriteDecision(ref); err != nil {
		return HostFSWriteClaim{}, err
	}
	c.emitHostFSWriteAudit(ref, overlay.ActionClaim, "allow", overlay.ClaimDetails(ref.decision, surface))
	c.emitHostFSWrite(ref, "claimed", "")
	return HostFSWriteClaim{
		DecisionID:     decisionID,
		State:          overlay.StateClaimed,
		ClaimToken:     token,
		ClaimExpiresAt: ref.decision.Claim.ExpiresAt,
	}, nil
}

func (c Core) DiscardHostFSWrite(req HostFSWriteDiscardRequest) (HostFSWriteResult, error) {
	if err := validateHostFSWriteExpectedVersion(req.ExpectedVersion); err != nil {
		return HostFSWriteResult{}, err
	}
	ref, err := c.findHostFSWriteByDecision(strings.TrimSpace(req.DecisionID))
	if err != nil {
		return HostFSWriteResult{}, err
	}
	if err := validateHostFSWriteClaim(ref.decision, req.ClaimToken); err != nil {
		return HostFSWriteResult{}, err
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "operator-denied"
	}
	ref.decision.State = overlay.StateDiscarded
	ref.operation.Status = overlay.StateDiscarded
	ref.decision.Claim = nil
	if err := ref.store.SaveDecision(ref.decision); err != nil {
		return HostFSWriteResult{}, err
	}
	if err := ref.store.SaveOperation(ref.operation); err != nil {
		return HostFSWriteResult{}, err
	}
	if _, err := c.upsertHostFSWriteDecision(ref); err != nil {
		return HostFSWriteResult{}, err
	}
	result := overlay.Result{
		Version:     overlay.ResultVersion,
		DecisionID:  ref.decision.DecisionID,
		OperationID: ref.operation.ID,
		Decision:    overlay.DecisionDeny,
		Status:      overlay.StateDiscarded,
		Privilege:   ref.decision.Privilege,
		AuditRef:    "audit:hostfs-overlay:discard",
	}
	c.emitHostFSWriteAudit(ref, overlay.ActionDiscard, overlay.DecisionDeny, overlay.ResultDetails(result, ref.decision.Operation, ref.decision.Path, ref.decision.DestinationPath))
	if cleanupErr := ref.store.CleanupArtifacts(ref.operation); cleanupErr == nil {
		c.emitHostFSWriteAudit(ref, overlay.ActionCleanup, "allow", hostFSWriteCleanupDetails(ref, "discard"))
	}
	c.emitHostFSWrite(ref, "discarded", reason)
	return result, nil
}

func (c Core) ApplyHostFSWrite(req HostFSWriteApplyRequest) (HostFSWriteResult, error) {
	if err := validateHostFSWriteExpectedVersion(req.ExpectedVersion); err != nil {
		return HostFSWriteResult{}, err
	}
	ref, err := c.findHostFSWriteByDecision(strings.TrimSpace(req.DecisionID))
	if err != nil {
		return HostFSWriteResult{}, err
	}
	if err := validateHostFSWriteClaim(ref.decision, req.ClaimToken); err != nil {
		return HostFSWriteResult{}, err
	}
	result, op, decision, applyErr := ref.store.Apply(ref.decision.DecisionID)
	ref.operation = op
	ref.decision = decision
	if _, err := c.upsertHostFSWriteDecision(ref); err != nil {
		return result, err
	}
	action := overlay.ActionApply
	auditDecision := overlay.DecisionAllow
	reason := ""
	if result.Status == overlay.StateConflict {
		action = overlay.ActionConflict
		auditDecision = overlay.DecisionDeny
		reason = result.ConflictReason
	} else if result.Status == overlay.StateFailed {
		auditDecision = "error"
		reason = result.ConflictReason
	}
	c.emitHostFSWriteAudit(ref, action, auditDecision, overlay.ResultDetails(result, ref.decision.Operation, ref.decision.Path, ref.decision.DestinationPath))
	if ref.operation.ContentObject != "" {
		c.emitHostFSWriteAudit(ref, overlay.ActionCleanup, "allow", hostFSWriteCleanupDetails(ref, result.Status))
	}
	c.emitHostFSWrite(ref, result.Status, reason)
	if errors.Is(applyErr, overlay.ErrConflict) {
		return result, nil
	}
	return result, applyErr
}

func (c Core) HostFSWriteStatus(req HostFSWriteStatusRequest) (HostFSWriteStatus, error) {
	if _, err := c.ExpireHostFSWriteTimeouts(time.Now().UTC()); err != nil {
		return HostFSWriteStatus{}, err
	}
	refs, err := c.listHostFSWriteRefs()
	if err != nil {
		return HostFSWriteStatus{}, err
	}
	sessionFilter := strings.TrimSpace(req.Session)
	profileFilter := strings.TrimSpace(req.Profile)
	stateFilter := strings.TrimSpace(req.State)
	out := HostFSWriteStatus{Version: HostFSWriteStatusVersion}
	for _, ref := range refs {
		if sessionFilter != "" && ref.sessionID != sessionFilter {
			continue
		}
		if profileFilter != "" && ref.operation.Profile != profileFilter {
			continue
		}
		if stateFilter != "" && ref.decision.State != stateFilter {
			continue
		}
		if stateFilter == "" && !hostFSWriteDecisionVisible(ref.decision.State) {
			continue
		}
		out.Pending = append(out.Pending, overlay.StatusEntry{
			DecisionID:      ref.decision.DecisionID,
			OperationID:     ref.operation.ID,
			Profile:         ref.operation.Profile,
			State:           ref.decision.State,
			Operation:       ref.decision.Operation,
			Path:            ref.decision.Path,
			DestinationPath: ref.decision.DestinationPath,
			TimeoutAt:       ref.decision.TimeoutAt,
			PrivilegeStatus: ref.decision.Privilege.Status,
		})
	}
	slices.SortFunc(out.Pending, func(a, b overlay.StatusEntry) int {
		return strings.Compare(a.DecisionID, b.DecisionID)
	})
	return out, nil
}

func (c Core) ExpireHostFSWriteTimeouts(now time.Time) (int, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	refs, err := c.listHostFSWriteRefs()
	if err != nil {
		return 0, err
	}
	expired := 0
	for _, ref := range refs {
		if !hostFSWriteDecisionVisible(ref.decision.State) || ref.decision.TimeoutAt.IsZero() || now.Before(ref.decision.TimeoutAt) {
			continue
		}
		if err := c.expireHostFSWriteRef(ref); err != nil {
			return expired, err
		}
		expired++
	}
	return expired, nil
}

func (c Core) expireHostFSWriteRef(ref hostFSWriteRef) error {
	ref.decision.State = overlay.StateExpired
	ref.decision.Claim = nil
	ref.operation.Status = overlay.StateExpired
	if err := ref.store.SaveDecision(ref.decision); err != nil {
		return err
	}
	if err := ref.store.SaveOperation(ref.operation); err != nil {
		return err
	}
	if _, err := c.upsertHostFSWriteDecision(ref); err != nil {
		return err
	}
	c.emitHostFSWriteAudit(ref, overlay.ActionTimeout, overlay.DecisionDeny, overlay.TimeoutDetails(ref.decision))
	if cleanupErr := ref.store.CleanupArtifacts(ref.operation); cleanupErr == nil {
		c.emitHostFSWriteAudit(ref, overlay.ActionCleanup, "allow", hostFSWriteCleanupDetails(ref, "approval-timeout"))
	}
	c.emitHostFSWrite(ref, "expired", "approval-timeout")
	return nil
}

func validateHostFSWriteExpectedVersion(version string) error {
	if strings.TrimSpace(version) == "" {
		return errors.New("expectedVersion is required")
	}
	if version != HostFSWritePlanVersion {
		return errors.New("invalid HostFS write plan version")
	}
	return nil
}

func validateHostFSWriteClaim(decision overlay.Decision, token string) error {
	if decision.State != overlay.StateClaimed || decision.Claim == nil {
		return errors.New("HostFS write decision is not claimed")
	}
	if strings.TrimSpace(token) == "" {
		return errors.New("claimToken is required")
	}
	if time.Now().UTC().After(decision.Claim.ExpiresAt) {
		return errors.New("claimToken expired")
	}
	if hashClaimToken(token) != decision.Claim.TokenHash {
		return errors.New("claimToken is invalid")
	}
	return nil
}

func (c Core) findHostFSWriteByOperation(operationID string) (hostFSWriteRef, error) {
	refs, err := c.listHostFSWriteRefs()
	if err != nil {
		return hostFSWriteRef{}, err
	}
	for _, ref := range refs {
		if ref.operation.ID == operationID {
			return ref, nil
		}
	}
	return hostFSWriteRef{}, fmt.Errorf("HostFS write operation %q not found", operationID)
}

func (c Core) findHostFSWriteByDecision(decisionID string) (hostFSWriteRef, error) {
	if strings.TrimSpace(decisionID) == "" {
		return hostFSWriteRef{}, errors.New("decisionId is required")
	}
	refs, err := c.listHostFSWriteRefs()
	if err != nil {
		return hostFSWriteRef{}, err
	}
	for _, ref := range refs {
		if ref.decision.DecisionID == decisionID {
			return ref, nil
		}
	}
	return hostFSWriteRef{}, fmt.Errorf("HostFS write decision %q not found", decisionID)
}

func (c Core) listHostFSWriteRefs() ([]hostFSWriteRef, error) {
	sessionsDir := filepath.Join(c.Store.Root, "sessions")
	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var refs []hostFSWriteRef
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		overlayRoot := filepath.Join(sessionsDir, entry.Name(), "hostfs-overlay")
		if _, err := os.Stat(overlayRoot); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		store, err := overlay.NewStore(overlayRoot)
		if err != nil {
			return nil, err
		}
		ops, err := store.Operations()
		if err != nil {
			return nil, err
		}
		for _, op := range ops {
			decision, err := store.DecisionForOperation(op.ID)
			if err != nil {
				continue
			}
			refs = append(refs, hostFSWriteRef{sessionID: entry.Name(), store: store, operation: op, decision: decision})
		}
	}
	return refs, nil
}

func hostFSWriteDecisionVisible(state string) bool {
	switch state {
	case overlay.StatePending, overlay.StateClaimed:
		return true
	default:
		return false
	}
}

func publicHostFSWriteDecision(decision overlay.Decision) overlay.Decision {
	if decision.Claim != nil {
		claim := *decision.Claim
		claim.Token = ""
		claim.TokenHash = ""
		decision.Claim = &claim
	}
	return decision
}

func (c Core) emitHostFSWrite(ref hostFSWriteRef, status, reason string) {
	decision := ref.decision
	if status == "" {
		status = decision.State
	}
	c.emitOperation("hostfs-write", status, map[string]any{
		"operationId":     decision.OperationID,
		"decisionId":      decision.DecisionID,
		"profile":         ref.operation.Profile,
		"session":         ref.sessionID,
		"backend":         ref.operation.Backend,
		"status":          status,
		"operation":       decision.Operation,
		"path":            decision.Path,
		"destinationPath": decision.DestinationPath,
		"privilegeStatus": decision.Privilege.Status,
		"reason":          reason,
	})
}

func (c Core) emitHostFSWriteAudit(ref hostFSWriteRef, action, decision string, details map[string]any) {
	if c.Store.Root == "" || ref.sessionID == "" {
		return
	}
	path := filepath.Join(c.Store.Root, "sessions", ref.sessionID, "audit.jsonl")
	aw, err := audit.NewFile(path)
	if err != nil {
		return
	}
	defer aw.Close()
	_ = aw.Emit(audit.Event{
		Session:  ref.sessionID,
		Profile:  ref.operation.Profile,
		Backend:  ref.operation.Backend,
		Action:   action,
		Decision: decision,
		Details:  details,
	})
}

func hostFSWriteCleanupDetails(ref hostFSWriteRef, reason string) map[string]any {
	return map[string]any{
		"operation":       ref.operation.Operation,
		"path":            ref.operation.RequestedPath,
		"operationId":     ref.operation.ID,
		"decisionId":      ref.decision.DecisionID,
		"reason":          reason,
		"stagedDiscarded": true,
		"hostChanged":     false,
		"privilegeStatus": ref.decision.Privilege.Status,
	}
}

func newClaimToken() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "claim_" + hex.EncodeToString(raw[:]), nil
}

func hashClaimToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
