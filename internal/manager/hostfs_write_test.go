package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/hostfs/overlay"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestHostFSWriteClaimSingleWinnerAndStaleTokensFailClosed(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	_, decision := stageHostFSWriteFixture(t, core)

	claim, err := core.ClaimHostFSWrite(HostFSWriteClaimRequest{
		DecisionID:      decision.DecisionID,
		ExpectedVersion: HostFSWritePlanVersion,
		Surface:         "webui",
	})
	if err != nil {
		t.Fatalf("ClaimHostFSWrite: %v", err)
	}
	if claim.ClaimToken == "" || claim.State != overlay.StateClaimed {
		t.Fatalf("bad claim response: %+v", claim)
	}
	if _, err := core.ClaimHostFSWrite(HostFSWriteClaimRequest{
		DecisionID:      decision.DecisionID,
		ExpectedVersion: HostFSWritePlanVersion,
		Surface:         "tui",
	}); err == nil {
		t.Fatal("second active claimant should fail closed")
	}
	if _, err := core.DiscardHostFSWrite(HostFSWriteDiscardRequest{
		DecisionID:      decision.DecisionID,
		ExpectedVersion: HostFSWritePlanVersion,
		ClaimToken:      "claim_wrong",
		Reason:          "operator-denied",
	}); err == nil {
		t.Fatal("stale/wrong claim token should fail closed")
	}
	result, err := core.DiscardHostFSWrite(HostFSWriteDiscardRequest{
		DecisionID:      decision.DecisionID,
		ExpectedVersion: HostFSWritePlanVersion,
		ClaimToken:      claim.ClaimToken,
		Reason:          "operator-denied",
	})
	if err != nil {
		t.Fatalf("DiscardHostFSWrite: %v", err)
	}
	if result.Decision != overlay.DecisionDeny || result.Status != overlay.StateDiscarded {
		t.Fatalf("bad discard result: %+v", result)
	}
	auditBody := readHostFSWriteAudit(t, core.Store.Root, "ses_20260708T000000Z_00112233445566778899")
	for _, want := range []string{overlay.ActionClaim, overlay.ActionDiscard} {
		if !strings.Contains(auditBody, want) {
			t.Fatalf("audit missing %s: %s", want, auditBody)
		}
	}
	if !strings.Contains(auditBody, overlay.ActionCleanup) {
		t.Fatalf("discard cleanup audit missing: %s", auditBody)
	}
	if strings.Contains(auditBody, claim.ClaimToken) || strings.Contains(auditBody, "tokenHash") {
		t.Fatalf("audit leaked claim token material: %s", auditBody)
	}
	ref, err := core.findHostFSWriteByDecision(decision.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ref.storeObjectPathForTest(ref.operation)); !os.IsNotExist(err) {
		t.Fatalf("discard should remove content object, err=%v", err)
	}
	status, err := core.HostFSWriteStatus(HostFSWriteStatusRequest{})
	if err != nil {
		t.Fatalf("HostFSWriteStatus: %v", err)
	}
	if len(status.Pending) != 0 {
		t.Fatalf("discarded decision should not remain pending: %+v", status)
	}
}

func TestHostFSWriteTimeoutDefaultsDenyAndEmitsEvent(t *testing.T) {
	recorder := &recordingHostFSWriteObserver{}
	core := Core{Store: profile.Store{Root: t.TempDir()}, Observer: recorder}
	_, decision := stageHostFSWriteFixture(t, core)
	ref, err := core.findHostFSWriteByDecision(decision.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	ref.decision.TimeoutAt = time.Now().Add(-time.Minute)
	if err := ref.store.SaveDecision(ref.decision); err != nil {
		t.Fatal(err)
	}
	expired, err := core.ExpireHostFSWriteTimeouts(time.Now())
	if err != nil {
		t.Fatalf("ExpireHostFSWriteTimeouts: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired=%d want 1", expired)
	}
	ref, err = core.findHostFSWriteByDecision(decision.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.decision.State != overlay.StateExpired || ref.operation.Status != overlay.StateExpired {
		t.Fatalf("decision/operation not expired: %+v %+v", ref.decision, ref.operation)
	}
	if len(recorder.events) != 1 || recorder.events[0].kind != "hostfs-write" || recorder.events[0].details["reason"] != "approval-timeout" || recorder.events[0].details["profile"] != "default" {
		t.Fatalf("timeout event mismatch: %+v", recorder.events)
	}
	auditBody := readHostFSWriteAudit(t, core.Store.Root, "ses_20260708T000000Z_00112233445566778899")
	if !strings.Contains(auditBody, overlay.ActionTimeout) || !strings.Contains(auditBody, "approval-timeout") {
		t.Fatalf("timeout audit missing: %s", auditBody)
	}
	if !strings.Contains(auditBody, overlay.ActionCleanup) {
		t.Fatalf("timeout cleanup audit missing: %s", auditBody)
	}
	if _, err := os.Stat(ref.storeObjectPathForTest(ref.operation)); !os.IsNotExist(err) {
		t.Fatalf("timeout should remove content object, err=%v", err)
	}
}

func TestHostFSWriteStatusCarriesAndFiltersProfile(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	_, decision := stageHostFSWriteFixture(t, core)
	status, err := core.HostFSWriteStatus(HostFSWriteStatusRequest{Profile: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Pending) != 1 || status.Pending[0].DecisionID != decision.DecisionID || status.Pending[0].Profile != "default" {
		t.Fatalf("profile status mismatch: %+v", status.Pending)
	}
	other, err := core.HostFSWriteStatus(HostFSWriteStatusRequest{Profile: "other"})
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Pending) != 0 {
		t.Fatalf("other profile saw HostFS write: %+v", other.Pending)
	}
}

func TestHostFSWriteClaimAfterApprovalTimeoutFailsClosed(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	_, decision := stageHostFSWriteFixture(t, core)
	ref, err := core.findHostFSWriteByDecision(decision.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	ref.decision.TimeoutAt = time.Now().Add(-time.Minute)
	if err := ref.store.SaveDecision(ref.decision); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ClaimHostFSWrite(HostFSWriteClaimRequest{
		DecisionID:      decision.DecisionID,
		ExpectedVersion: HostFSWritePlanVersion,
		Surface:         "cli",
	}); err == nil {
		t.Fatal("expired HostFS write decision should reject claim")
	}
	ref, err = core.findHostFSWriteByDecision(decision.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.decision.State != overlay.StateExpired || ref.operation.Status != overlay.StateExpired {
		t.Fatalf("expired claim did not persist expired state: %+v %+v", ref.decision, ref.operation)
	}
}

func TestHostFSWriteApplyMutatesHostAndAudits(t *testing.T) {
	recorder := &recordingHostFSWriteObserver{}
	core := Core{Store: profile.Store{Root: t.TempDir()}, Observer: recorder}
	op, decision := stageHostFSWriteFixture(t, core)
	claim, err := core.ClaimHostFSWrite(HostFSWriteClaimRequest{
		DecisionID:      decision.DecisionID,
		ExpectedVersion: HostFSWritePlanVersion,
		Surface:         "cli",
	})
	if err != nil {
		t.Fatalf("ClaimHostFSWrite: %v", err)
	}
	result, err := core.ApplyHostFSWrite(HostFSWriteApplyRequest{
		DecisionID:      decision.DecisionID,
		ExpectedVersion: HostFSWritePlanVersion,
		ClaimToken:      claim.ClaimToken,
	})
	if err != nil {
		t.Fatalf("ApplyHostFSWrite: %v", err)
	}
	if result.Status != overlay.StateApplied || result.Decision != overlay.DecisionAllow {
		t.Fatalf("bad apply result: %+v", result)
	}
	body, err := os.ReadFile(op.RequestedPath)
	if err != nil || string(body) != "staged" {
		t.Fatalf("host content=%q err=%v", body, err)
	}
	auditBody := readHostFSWriteAudit(t, core.Store.Root, "ses_20260708T000000Z_00112233445566778899")
	if !strings.Contains(auditBody, overlay.ActionApply) || strings.Contains(auditBody, claim.ClaimToken) {
		t.Fatalf("apply audit mismatch: %s", auditBody)
	}
	if !strings.Contains(auditBody, overlay.ActionCleanup) {
		t.Fatalf("apply cleanup audit missing: %s", auditBody)
	}
	if len(recorder.events) == 0 || recorder.events[len(recorder.events)-1].phase != overlay.StateApplied {
		t.Fatalf("apply event missing: %+v", recorder.events)
	}
	status, err := core.HostFSWriteStatus(HostFSWriteStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Pending) != 0 {
		t.Fatalf("applied decision should not remain pending: %+v", status)
	}
}

func TestHostFSWritePlanApplySurfacesDegradedPrivilegeStatus(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	op, decision := stageHostFSWriteFixtureWithPrivilege(t, core, overlay.Privilege{Status: "degraded", Reason: "target passwordless sudo available"})
	plan, err := core.PlanHostFSWrite(HostFSWritePlanRequest{OperationID: op.ID, IncludePreview: true})
	if err != nil {
		t.Fatalf("PlanHostFSWrite: %v", err)
	}
	if plan.Privilege.Status != "degraded" || !strings.Contains(plan.Privilege.Reason, "sudo") {
		t.Fatalf("plan did not surface degraded privilege: %+v", plan.Privilege)
	}
	claim, err := core.ClaimHostFSWrite(HostFSWriteClaimRequest{
		DecisionID:      decision.DecisionID,
		ExpectedVersion: HostFSWritePlanVersion,
		Surface:         "cli",
	})
	if err != nil {
		t.Fatalf("ClaimHostFSWrite: %v", err)
	}
	result, err := core.ApplyHostFSWrite(HostFSWriteApplyRequest{
		DecisionID:      decision.DecisionID,
		ExpectedVersion: HostFSWritePlanVersion,
		ClaimToken:      claim.ClaimToken,
	})
	if err != nil {
		t.Fatalf("ApplyHostFSWrite: %v", err)
	}
	if result.Privilege.Status != "degraded" {
		t.Fatalf("apply result lost degraded privilege: %+v", result)
	}
}

func stageHostFSWriteFixture(t *testing.T, core Core) (overlay.Operation, overlay.Decision) {
	return stageHostFSWriteFixtureWithPrivilege(t, core, overlay.Privilege{Status: "enforced", Reason: "target-no-sudo"})
}

func stageHostFSWriteFixtureWithPrivilege(t *testing.T, core Core, privilege overlay.Privilege) (overlay.Operation, overlay.Decision) {
	t.Helper()
	sessionID := "ses_20260708T000000Z_00112233445566778899"
	root := filepath.Join(core.Store.Root, "sessions", sessionID, "hostfs-overlay")
	store, err := overlay.NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.txt")
	result, err := store.Stage(overlay.StageRequest{
		SessionID:   sessionID,
		Profile:     "default",
		Backend:     "native",
		Operation:   "create",
		Path:        target,
		GrantID:     "hfs_overlay",
		GrantSource: "profile",
		Data:        []byte("staged"),
		Privilege:   privilege,
	})
	if err != nil {
		t.Fatalf("stage fixture: %v", err)
	}
	return result.Operation, result.Decision
}

type recordingHostFSWriteObserver struct {
	events []recordedHostFSWriteEvent
}

type recordedHostFSWriteEvent struct {
	kind    string
	phase   string
	details map[string]any
}

func (r *recordingHostFSWriteObserver) OperationEvent(kind, phase string, details map[string]any) {
	r.events = append(r.events, recordedHostFSWriteEvent{kind: kind, phase: phase, details: details})
}

func readHostFSWriteAudit(t *testing.T, storeRoot, sessionID string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(storeRoot, "sessions", sessionID, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (ref hostFSWriteRef) storeObjectPathForTest(op overlay.Operation) string {
	return filepath.Join(ref.store.Root, "objects", filepath.Base(op.ContentObject))
}
