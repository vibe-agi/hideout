package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestDecisionCenterHostFSWriteParityAndSingleClaim(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	op, hostDecision := stageHostFSWriteFixture(t, core)
	plan, err := core.PlanHostFSWrite(HostFSWritePlanRequest{OperationID: op.ID, IncludePreview: true})
	if err != nil {
		t.Fatalf("PlanHostFSWrite: %v", err)
	}
	if plan.DecisionID != hostDecision.DecisionID {
		t.Fatalf("plan decision=%s want %s", plan.DecisionID, hostDecision.DecisionID)
	}
	decisions, err := core.ListDecisions(DecisionListRequest{Kind: decision.KindHostFSWrite})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].ID != hostDecision.DecisionID {
		t.Fatalf("generic decision mismatch: %+v", decisions)
	}
	if decisions[0].ProviderRef.Provider != "" || decisions[0].ProviderRef.OperationID != "" {
		t.Fatalf("public decision leaked provider reference: %+v", decisions[0].ProviderRef)
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: hostDecision.DecisionID, ExpectedVersion: decision.DecisionVersion, Surface: "cli"})
	if err != nil {
		t.Fatalf("ClaimDecision: %v", err)
	}
	if claim.ClaimToken == "" {
		t.Fatalf("missing claim token: %+v", claim)
	}
	if _, err := core.ClaimHostFSWrite(HostFSWriteClaimRequest{DecisionID: hostDecision.DecisionID, ExpectedVersion: HostFSWritePlanVersion, Surface: "webui"}); err == nil {
		t.Fatalf("compat route should see generic/hostfs active claim and fail")
	}
	if _, err := core.DenyDecision(DecisionResolveRequest{DecisionID: hostDecision.DecisionID, ExpectedVersion: decision.DecisionVersion, ClaimToken: "claim_wrong"}); err == nil {
		t.Fatalf("wrong generic claim token should fail closed")
	}
	res, err := core.DenyDecision(DecisionResolveRequest{DecisionID: hostDecision.DecisionID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken, Reason: "operator-denied"})
	if err != nil {
		t.Fatalf("DenyDecision: %v", err)
	}
	if res.Status != decision.StateDiscarded || res.Decision != "deny" {
		t.Fatalf("bad resolution: %+v", res)
	}
	status, err := core.HostFSWriteStatus(HostFSWriteStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Pending) != 0 {
		t.Fatalf("hostfs compatibility status diverged: %+v", status)
	}
}

func TestNoticesAreAcknowledgedButNeverApproved(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	n, err := core.CreateNotice(decision.Notice{
		ID:       "privilege-degraded-default",
		Kind:     decision.KindPrivilegeStatus,
		Severity: decision.NoticeSeverityWarning,
		Status:   "degraded",
		Preview:  decision.Preview{Summary: "target can passwordless sudo"},
		AuditRef: "audit:privilege",
	})
	if err != nil {
		t.Fatalf("CreateNotice: %v", err)
	}
	if n.Acknowledged {
		t.Fatalf("new notice should be unacknowledged")
	}
	if _, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: n.ID, ExpectedVersion: decision.DecisionVersion, Surface: "cli"}); err == nil {
		t.Fatalf("notice must not be claimable")
	}
	ack, err := core.AckNotice(NoticeAckRequest{NoticeID: n.ID, Surface: "webui"})
	if err != nil {
		t.Fatalf("AckNotice: %v", err)
	}
	if ack.Surface != "webui" {
		t.Fatalf("bad ack: %+v", ack)
	}
	notices, err := core.ListNotices(NoticeListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || !notices[0].Acknowledged {
		t.Fatalf("notice not acked: %+v", notices)
	}
	body := readOperatorCenterAudit(t, core.Store.Root)
	if !strings.Contains(body, decision.ActionNoticeCreate) || !strings.Contains(body, decision.ActionNoticeAck) {
		t.Fatalf("notice audit missing lifecycle: %s", body)
	}
}

func TestDecisionStatusIsMachineReadableAndProviderPrivateFree(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	op, hostDecision := stageHostFSWriteFixture(t, core)
	if _, err := core.PlanHostFSWrite(HostFSWritePlanRequest{OperationID: op.ID, IncludePreview: true}); err != nil {
		t.Fatalf("PlanHostFSWrite: %v", err)
	}
	if _, err := core.CreateNotice(decision.Notice{
		ID:       "bg-1",
		Kind:     decision.KindBackgroundStatus,
		Severity: decision.NoticeSeverityInfo,
		Status:   "running",
		Preview:  decision.Preview{Summary: "background running"},
		AuditRef: "audit:bg",
	}); err != nil {
		t.Fatal(err)
	}
	status, err := core.DecisionStatus()
	if err != nil {
		t.Fatalf("DecisionStatus: %v", err)
	}
	if status.PendingDecisions != 1 || status.UnackedNotices != 1 || len(status.DecisionIDs) != 1 || status.DecisionIDs[0] != hostDecision.DecisionID {
		t.Fatalf("status mismatch: %+v", status)
	}
	if strings.Contains(status.OldestPendingAge, "hostfs-overlay") {
		t.Fatalf("diagnostic status leaked provider path: %+v", status)
	}
}

func TestDecisionStatusExpiresGenericDecisionsAndAuditsTimeout(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	expiredAt := time.Now().Add(-time.Minute)
	if _, err := core.CreateDecision(decision.Decision{
		ID:             "share-timeout-1",
		Kind:           decision.KindEvidenceShare,
		State:          decision.StatePending,
		Source:         decision.Source{Profile: "default", Session: "ses_timeout", Backend: "lima"},
		Preview:        decision.Preview{Summary: "share evidence"},
		DefaultOutcome: decision.DefaultOutcomeDeny,
		TimeoutAt:      expiredAt,
		AllowedActions: []string{decision.ActionApprove, decision.ActionDeny},
		AuditRef:       "audit:share-timeout-1",
	}); err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	status, err := core.DecisionStatus()
	if err != nil {
		t.Fatalf("DecisionStatus: %v", err)
	}
	if status.PendingDecisions != 0 || status.TerminalDecisions != 1 {
		t.Fatalf("status did not expire decision: %+v", status)
	}
	decisions, err := core.ListDecisions(DecisionListRequest{Kind: decision.KindEvidenceShare, IncludeTerminal: true})
	if err != nil {
		t.Fatalf("ListDecisions: %v", err)
	}
	if len(decisions) != 1 || decisions[0].State != decision.StateTimedOut {
		t.Fatalf("decision did not time out: %+v", decisions)
	}
	body := readOperatorCenterAudit(t, core.Store.Root)
	if !strings.Contains(body, decision.ActionDecisionTimeout) {
		t.Fatalf("timeout audit missing: %s", body)
	}
	if _, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: "share-timeout-1", ExpectedVersion: decision.DecisionVersion, Surface: "cli"}); err == nil {
		t.Fatalf("timed-out decision should not be claimable")
	}
}

func TestAdapterProposalWithoutProviderFailsClosed(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	d, err := core.CreateDecision(decision.Decision{
		ID:             "adapter-proposal-1",
		Kind:           decision.KindAdapterProposal,
		State:          decision.StatePending,
		Preview:        decision.Preview{Summary: "adapter requests guest.privilege.plan"},
		DefaultOutcome: decision.DefaultOutcomeDeny,
		TimeoutAt:      time.Now().Add(time.Minute),
		AllowedActions: []string{decision.ActionApprove, decision.ActionDeny},
		AuditRef:       "audit:adapter",
	})
	if err != nil {
		t.Fatalf("CreateDecision: %v", err)
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "cli"})
	if err != nil {
		t.Fatalf("ClaimDecision: %v", err)
	}
	if _, err := core.ApproveDecision(DecisionResolveRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken}); err == nil {
		t.Fatalf("adapter proposal without promoted provider should fail closed")
	}
}

func readOperatorCenterAudit(t *testing.T, storeRoot string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(storeRoot, "operator-center", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
