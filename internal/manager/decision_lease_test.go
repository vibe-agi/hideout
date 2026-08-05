package manager

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/hostfs/overlay"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestDecisionClaimLeaseIsVisibleBoundedAndExplicitlyReleasedOnDisconnect(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	observer := &recordingObserver{}
	core := Core{
		Store:       profile.Store{Root: t.TempDir()},
		DecisionNow: func() time.Time { return now },
		Observer:    observer,
	}
	d := createDecisionLeaseFixture(t, core, "dec_disconnect", now.Add(time.Hour))

	for _, seconds := range []int64{1, 301} {
		if _, err := core.ClaimDecision(DecisionClaimRequest{
			DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion,
			Surface: "tui", LeaseSeconds: seconds,
		}); err == nil {
			t.Fatalf("out-of-bounds leaseSeconds=%d was accepted", seconds)
		}
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion,
		Surface: "tui", LeaseSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Surface != "tui" || claim.LeaseSeconds != 30 ||
		!claim.ClaimedAt.Equal(now) || !claim.ClaimExpiresAt.Equal(now.Add(30*time.Second)) ||
		claim.Revision <= d.Revision || claim.Takeover {
		t.Fatalf("claim lease is not exact and visible: %+v", claim)
	}
	visible, err := core.InspectDecision(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if visible.State != decision.StateClaimed || visible.Claim == nil ||
		visible.Claim.Surface != "tui" || visible.Claim.Operator != decisionClaimOperator ||
		!visible.Claim.ExpiresAt.Equal(claim.ClaimExpiresAt) || visible.Claim.TokenHash != "" {
		t.Fatalf("public claim metadata mismatch: %+v", visible)
	}
	if _, err := core.ReleaseDecisionClaim(DecisionReleaseRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion,
		ExpectedRevision: claim.Revision, ClaimToken: "claim_wrong",
	}); err == nil {
		t.Fatal("another client released a live claim without its token")
	}
	release, err := core.ReleaseDecisionClaim(DecisionReleaseRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion,
		ExpectedRevision: claim.Revision, ClaimToken: claim.ClaimToken,
		Reason: "decision dialog disconnected",
	})
	if err != nil {
		t.Fatal(err)
	}
	if release.State != decision.StatePending || release.Expired ||
		release.PreviousSurface != "tui" || release.Revision != claim.Revision+1 {
		t.Fatalf("release result mismatch: %+v", release)
	}
	if _, err := core.DenyDecision(DecisionResolveRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion,
		ClaimToken: claim.ClaimToken,
	}); err == nil {
		t.Fatal("released claimant retained decision authority")
	}
	replacement, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion,
		ExpectedRevision: release.Revision, Surface: "webui", LeaseSeconds: 60,
	})
	if err != nil {
		t.Fatalf("new authenticated client could not claim after disconnect release: %v", err)
	}
	if replacement.ClaimToken == claim.ClaimToken || replacement.Surface != "webui" {
		t.Fatalf("replacement claim mismatch: %+v", replacement)
	}
	if !containsObserverEvent(observer.events, "decision:claim-released") {
		t.Fatalf("disconnect release event missing: %v", observer.events)
	}
	auditBody, err := os.ReadFile(filepath.Join(core.Store.Root, "operator-center", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(auditBody), decision.ActionDecisionRelease) ||
		strings.Contains(string(auditBody), claim.ClaimToken) {
		t.Fatalf("release audit missing or leaked token: %s", auditBody)
	}
}

func TestDecisionClaimExpiryEmitsReleaseAndStaleClaimantFails(t *testing.T) {
	now := time.Date(2026, 7, 29, 13, 0, 0, 0, time.UTC)
	observer := &recordingObserver{}
	core := Core{
		Store:       profile.Store{Root: t.TempDir()},
		DecisionNow: func() time.Time { return now },
		Observer:    observer,
	}
	d := createDecisionLeaseFixture(t, core, "dec_expiry", now.Add(time.Hour))
	claim, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, Surface: "webui", LeaseSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	now = claim.ClaimExpiresAt
	listed, err := core.ListDecisions(DecisionListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].State != decision.StatePending ||
		listed[0].Claim != nil || listed[0].Revision != claim.Revision+1 {
		t.Fatalf("expired lease did not visibly return to pending: %+v", listed)
	}
	if _, err := core.DenyDecision(DecisionResolveRequest{
		DecisionID: d.ID, ClaimToken: claim.ClaimToken,
	}); err == nil {
		t.Fatal("expired claimant retained authority after release")
	}
	if !containsObserverEvent(observer.events, "decision:claim-expired") {
		t.Fatalf("claim expiry event missing: %v", observer.events)
	}
	auditBody, err := os.ReadFile(filepath.Join(core.Store.Root, "operator-center", "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(auditBody), decision.ActionDecisionExpiry) {
		t.Fatalf("claim expiry audit missing: %s", auditBody)
	}
}

func TestDecisionExpiredClaimTakeoverRequiresExplicitExactRevision(t *testing.T) {
	now := time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
	core := Core{
		Store:       profile.Store{Root: t.TempDir()},
		DecisionNow: func() time.Time { return now },
	}
	d := createDecisionLeaseFixture(t, core, "dec_takeover", now.Add(time.Hour))
	first, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, Surface: "cli", LeaseSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	now = first.ClaimExpiresAt.Add(time.Nanosecond)

	if _, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, Surface: "webui", LeaseSeconds: 30,
	}); err == nil || !strings.Contains(err.Error(), "explicit takeover") {
		t.Fatalf("silent expired-claim takeover was not rejected: %v", err)
	}
	if _, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, Surface: "webui", LeaseSeconds: 30, Takeover: true,
	}); err == nil || !strings.Contains(err.Error(), "expectedRevision") {
		t.Fatalf("takeover without CAS revision was not rejected: %v", err)
	}
	if _, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, Surface: "webui", LeaseSeconds: 30,
		Takeover: true, ExpectedRevision: first.Revision + 1,
	}); err == nil || !strings.Contains(err.Error(), "revision changed") {
		t.Fatalf("takeover with stale revision was not rejected: %v", err)
	}
	second, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, Surface: "webui", LeaseSeconds: 30,
		Takeover: true, ExpectedRevision: first.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.Takeover || second.Revision != first.Revision+1 ||
		second.ClaimToken == first.ClaimToken {
		t.Fatalf("authenticated takeover result mismatch: %+v", second)
	}
	if _, err := core.DenyDecision(DecisionResolveRequest{
		DecisionID: d.ID, ClaimToken: first.ClaimToken,
	}); err == nil {
		t.Fatal("stale claimant resolved after takeover")
	}
	if _, err := core.DenyDecision(DecisionResolveRequest{
		DecisionID: d.ID, ClaimToken: second.ClaimToken,
	}); err != nil {
		t.Fatalf("current claimant could not resolve: %v", err)
	}
}

func TestDecisionTakeoverAPIRequiresOperatorAuthentication(t *testing.T) {
	now := time.Date(2026, 7, 29, 15, 0, 0, 0, time.UTC)
	core := Core{
		Store:       profile.Store{Root: t.TempDir()},
		DecisionNow: func() time.Time { return now },
	}
	d := createDecisionLeaseFixture(t, core, "dec_api_takeover", now.Add(time.Hour))
	first, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, Surface: "cli", LeaseSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	now = first.ClaimExpiresAt.Add(time.Second)
	body, err := json.Marshal(DecisionClaimRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion,
		Surface: "webui", LeaseSeconds: 30, Takeover: true,
		ExpectedRevision: first.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	api := API{Core: core, Token: "operator_token"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/decision/claim", strings.NewReader(string(body)))
	req.Host = "localhost"
	rec := httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated takeover status=%d body=%s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodPost, "/api/v1/decision/claim", strings.NewReader(string(body)))
	req.Host = "localhost"
	req.Header.Set("Authorization", "Bearer operator_token")
	rec = httptest.NewRecorder()
	api.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"takeover":true`) {
		t.Fatalf("authenticated takeover status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHostFSWriteDecisionDisconnectReleaseRevokesProviderToken(t *testing.T) {
	core := Core{Store: profile.Store{Root: t.TempDir()}}
	_, staged := stageHostFSWriteFixture(t, core)
	claim, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: staged.DecisionID, ExpectedVersion: decision.DecisionVersion,
		Surface: "tui", LeaseSeconds: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	release, err := core.ReleaseDecisionClaim(DecisionReleaseRequest{
		DecisionID: staged.DecisionID, ExpectedVersion: decision.DecisionVersion,
		ExpectedRevision: claim.Revision, ClaimToken: claim.ClaimToken,
		Reason: "tui dialog closed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if release.State != decision.StatePending {
		t.Fatalf("HostFS release mismatch: %+v", release)
	}
	ref, err := core.findHostFSWriteByDecision(staged.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.decision.State != overlay.StatePending || ref.decision.Claim != nil {
		t.Fatalf("provider claim remained live: %+v", ref.decision)
	}
	if _, err := core.ApplyHostFSWrite(HostFSWriteApplyRequest{
		DecisionID: staged.DecisionID, ExpectedVersion: HostFSWritePlanVersion,
		ClaimToken: claim.ClaimToken,
	}); err == nil {
		t.Fatal("released HostFS provider token still applied")
	}
	replacement, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: staged.DecisionID, ExpectedVersion: decision.DecisionVersion,
		ExpectedRevision: release.Revision, Surface: "webui", LeaseSeconds: 60,
	})
	if err != nil {
		t.Fatalf("HostFS decision was not reclaimable: %v", err)
	}
	if replacement.ClaimToken == claim.ClaimToken {
		t.Fatal("HostFS provider reused a released claim token")
	}
}

func TestHostFSWriteDecisionLeaseExpiryConvergesProviderAndPublicState(t *testing.T) {
	now := time.Now().UTC()
	core := Core{
		Store:       profile.Store{Root: t.TempDir()},
		DecisionNow: func() time.Time { return now },
	}
	_, staged := stageHostFSWriteFixture(t, core)
	claim, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: staged.DecisionID, ExpectedVersion: decision.DecisionVersion,
		Surface: "webui", LeaseSeconds: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	now = claim.ClaimExpiresAt
	rows, err := core.ListDecisions(DecisionListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != decision.StatePending || rows[0].Claim != nil {
		t.Fatalf("public HostFS claim did not expire: %+v", rows)
	}
	ref, err := core.findHostFSWriteByDecision(staged.DecisionID)
	if err != nil {
		t.Fatal(err)
	}
	if ref.decision.State != overlay.StatePending || ref.decision.Claim != nil {
		t.Fatalf("provider HostFS claim did not expire: %+v", ref.decision)
	}
	if _, err := core.ApplyHostFSWrite(HostFSWriteApplyRequest{
		DecisionID: staged.DecisionID, ExpectedVersion: HostFSWritePlanVersion,
		ClaimToken: claim.ClaimToken,
	}); err == nil {
		t.Fatal("expired HostFS claimant retained provider authority")
	}
	auditBody := readHostFSWriteAudit(t, core.Store.Root, ref.sessionID)
	if !strings.Contains(auditBody, overlay.ActionExpiry) ||
		strings.Contains(auditBody, claim.ClaimToken) {
		t.Fatalf("HostFS claim expiry audit missing or leaked token: %s", auditBody)
	}
}

func TestWebDecisionClientReleasesOwnedClaimsOnCloseAndRefresh(t *testing.T) {
	html := renderUIHTML(time.Date(2026, 7, 29, 16, 0, 0, 0, time.UTC))
	for _, required := range []string{
		`function releaseOwnedClaims(reason)`,
		`"/api/v1/decision/release"`,
		`keepalive: true`,
		`window.addEventListener("pagehide"`,
		`releaseOwnedClaims("webui refreshed or changed scope")`,
		`data-decision-action="release"`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("Web decision release contract missing %q", required)
		}
	}
}

func createDecisionLeaseFixture(t *testing.T, core Core, id string, timeout time.Time) decision.Decision {
	t.Helper()
	d, err := core.CreateDecision(decision.Decision{
		ID:             id,
		Kind:           decision.KindEvidenceShare,
		State:          decision.StatePending,
		Source:         decision.Source{Profile: "default", Surface: "manager"},
		Preview:        decision.Preview{Summary: "review bounded evidence release"},
		DefaultOutcome: decision.DefaultOutcomeDeny,
		TimeoutAt:      timeout,
		AllowedActions: []string{decision.ActionApprove, decision.ActionDeny},
		AuditRef:       "audit:" + id,
	})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func containsObserverEvent(events []string, want string) bool {
	for _, event := range events {
		if event == want {
			return true
		}
	}
	return false
}
