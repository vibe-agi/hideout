package manager

import (
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/hostfs/overlay"
)

const (
	decisionClaimOperator  = "local-operator"
	decisionReasonMaxBytes = 512
)

type DecisionReleaseRequest struct {
	DecisionID       string `json:"decisionId"`
	ExpectedVersion  string `json:"expectedVersion"`
	ExpectedRevision int    `json:"expectedRevision,omitempty"`
	ClaimToken       string `json:"claimToken"`
	Reason           string `json:"reason,omitempty"`
}

func (c Core) decisionNow() time.Time {
	if c.DecisionNow != nil {
		return c.DecisionNow().UTC()
	}
	return time.Now().UTC()
}

func decisionClaimLease(seconds int64) (time.Duration, error) {
	if seconds == 0 {
		return decision.DefaultClaimLease, nil
	}
	minimum := int64(decision.MinClaimLease / time.Second)
	maximum := int64(decision.MaxClaimLease / time.Second)
	if seconds < minimum || seconds > maximum {
		return 0, fmt.Errorf(
			"leaseSeconds must be between %d and %d",
			minimum,
			maximum,
		)
	}
	return time.Duration(seconds) * time.Second, nil
}

func validateDecisionReleaseRequest(req DecisionReleaseRequest) error {
	if req.ExpectedVersion != "" && req.ExpectedVersion != decision.DecisionVersion {
		return errors.New("invalid decision version")
	}
	if strings.TrimSpace(req.DecisionID) == "" {
		return errors.New("decisionId is required")
	}
	if strings.TrimSpace(req.ClaimToken) == "" {
		return errors.New("claimToken is required")
	}
	if len(req.Reason) > decisionReasonMaxBytes || strings.ContainsRune(req.Reason, '\x00') {
		return fmt.Errorf("decision release reason exceeds %d bytes or contains NUL", decisionReasonMaxBytes)
	}
	return nil
}

// ReleaseDecisionClaim gives a deciding client an explicit disconnect/close
// path. The exact opaque claim token authenticates release; a surface label can
// never release another client's live lease.
func (c Core) ReleaseDecisionClaim(req DecisionReleaseRequest) (decision.ClaimRelease, error) {
	if err := validateDecisionReleaseRequest(req); err != nil {
		return decision.ClaimRelease{}, err
	}
	d, err := c.inspectDecisionPrivate(req.DecisionID)
	if err != nil {
		return decision.ClaimRelease{}, err
	}
	if decision.DecisionTerminal(d.State) {
		return decision.ClaimRelease{}, fmt.Errorf("decision %s is %s", d.ID, d.State)
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "deciding client disconnected"
	}
	if d.Kind == decision.KindHostFSWrite {
		return c.releaseHostFSWriteDecisionClaim(req, d, reason)
	}
	store, err := c.decisionStore()
	if err != nil {
		return decision.ClaimRelease{}, err
	}
	release, updated, err := store.ReleaseDecisionClaim(
		req.DecisionID,
		req.ClaimToken,
		req.ExpectedRevision,
		reason,
	)
	if err != nil {
		_ = c.emitDecisionAudit(decision.ActionDecisionStale, "deny", d, map[string]any{
			"reason": err.Error(),
		})
		return decision.ClaimRelease{}, err
	}
	if updated.Kind == decision.KindHostFSRead {
		if err := c.syncHostFSReadDecision(updated); err != nil {
			return decision.ClaimRelease{}, err
		}
	}
	if err := c.emitDecisionClaimRelease(decision.ActionDecisionRelease, updated, release); err != nil {
		return decision.ClaimRelease{}, err
	}
	c.emitDecision(updated, "claim-released", release.Reason)
	return release, nil
}

func (c Core) emitDecisionClaimRelease(action string, d decision.Decision, release decision.ClaimRelease) error {
	return c.emitDecisionAudit(action, "allow", d, map[string]any{
		"reason":            release.Reason,
		"releasedAt":        release.ReleasedAt,
		"previousSurface":   release.PreviousSurface,
		"previousClaimedAt": release.PreviousClaimedAt,
		"previousExpiresAt": release.PreviousExpiresAt,
		"expired":           release.Expired,
		"revision":          release.Revision,
	})
}

// maintainDecisionCenter converges whole-decision timeouts and shorter claim
// leases before a public list/inspect/status projection is returned. An expired
// claim becomes pending and emits evidence; it is never silently overwritten.
func (c Core) maintainDecisionCenter() error {
	now := c.decisionNow()
	if _, err := c.ExpireHostFSWriteTimeouts(now); err != nil {
		return err
	}
	if err := c.releaseExpiredHostFSWriteClaims(now); err != nil {
		return err
	}
	if err := c.syncHostFSWriteDecisions(); err != nil {
		return err
	}
	if err := c.expireDecisionTimeouts(); err != nil {
		return err
	}
	store, err := c.decisionStore()
	if err != nil {
		return err
	}
	released, err := store.ReleaseExpiredClaims(now)
	if err != nil {
		return err
	}
	for _, item := range released {
		if item.Decision.Kind == decision.KindHostFSRead {
			if err := c.syncHostFSReadDecision(item.Decision); err != nil {
				return err
			}
		}
		if err := c.emitDecisionClaimRelease(decision.ActionDecisionExpiry, item.Decision, item.Release); err != nil {
			return err
		}
		c.emitDecision(item.Decision, "claim-expired", item.Release.Reason)
	}
	return nil
}

func (c Core) releaseExpiredHostFSWriteClaims(now time.Time) error {
	refs, err := c.listHostFSWriteRefs()
	if err != nil {
		return err
	}
	for _, ref := range refs {
		if ref.decision.State != overlay.StateClaimed || ref.decision.Claim == nil ||
			now.Before(ref.decision.Claim.ExpiresAt) {
			continue
		}
		previous := *ref.decision.Claim
		ref.decision.State = overlay.StatePending
		ref.decision.Claim = nil
		if err := ref.store.SaveDecision(ref.decision); err != nil {
			return err
		}
		updated, err := c.upsertHostFSWriteDecision(ref)
		if err != nil {
			return err
		}
		release := decision.ClaimRelease{
			Version:           decision.DecisionReleaseVersion,
			DecisionID:        updated.ID,
			State:             decision.StatePending,
			Reason:            "claim lease expired",
			ReleasedAt:        now,
			PreviousSurface:   previous.Surface,
			PreviousClaimedAt: previous.ClaimedAt,
			PreviousExpiresAt: previous.ExpiresAt,
			Expired:           true,
			Revision:          updated.Revision,
		}
		details := overlay.PendingDetails(ref.decision)
		details["reason"] = release.Reason
		details["previousSurface"] = previous.Surface
		details["previousExpiresAt"] = previous.ExpiresAt
		c.emitHostFSWriteAudit(ref, overlay.ActionExpiry, "allow", details)
		c.emitHostFSWrite(ref, "claim-expired", release.Reason)
		if err := c.emitDecisionClaimRelease(decision.ActionDecisionExpiry, updated, release); err != nil {
			return err
		}
		c.emitDecision(updated, "claim-expired", release.Reason)
	}
	return nil
}

func (c Core) releaseHostFSWriteDecisionClaim(
	req DecisionReleaseRequest,
	d decision.Decision,
	reason string,
) (decision.ClaimRelease, error) {
	if d.State != decision.StateClaimed || d.Claim == nil {
		return decision.ClaimRelease{}, errors.New("decision is not claimed")
	}
	if req.ExpectedRevision > 0 && req.ExpectedRevision != d.Revision {
		return decision.ClaimRelease{}, fmt.Errorf(
			"decision %s revision changed: expected %d, current %d",
			d.ID,
			req.ExpectedRevision,
			d.Revision,
		)
	}
	got := []byte(decision.HashClaimToken(req.ClaimToken))
	want := []byte(d.Claim.TokenHash)
	if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
		return decision.ClaimRelease{}, errors.New("claimToken is invalid")
	}
	ref, err := c.findHostFSWriteByDecision(d.ID)
	if err != nil {
		return decision.ClaimRelease{}, err
	}
	if err := validateHostFSWriteClaimToken(ref.decision, req.ClaimToken); err != nil {
		return decision.ClaimRelease{}, err
	}
	previous := *ref.decision.Claim
	ref.decision.State = overlay.StatePending
	ref.decision.Claim = nil
	if err := ref.store.SaveDecision(ref.decision); err != nil {
		return decision.ClaimRelease{}, err
	}
	store, err := c.decisionStore()
	if err != nil {
		return decision.ClaimRelease{}, err
	}
	release, updated, err := store.ReleaseDecisionClaim(
		req.DecisionID,
		req.ClaimToken,
		req.ExpectedRevision,
		reason,
	)
	if err != nil {
		// The provider is already fail-closed (pending). A later synchronization
		// converges the public record without restoring the released token.
		return decision.ClaimRelease{}, err
	}
	details := overlay.PendingDetails(ref.decision)
	details["reason"] = release.Reason
	details["previousSurface"] = previous.Surface
	details["previousExpiresAt"] = previous.ExpiresAt
	c.emitHostFSWriteAudit(ref, overlay.ActionRelease, "allow", details)
	c.emitHostFSWrite(ref, "claim-released", release.Reason)
	if err := c.emitDecisionClaimRelease(decision.ActionDecisionRelease, updated, release); err != nil {
		return decision.ClaimRelease{}, err
	}
	c.emitDecision(updated, "claim-released", release.Reason)
	return release, nil
}
