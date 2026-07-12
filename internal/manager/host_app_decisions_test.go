package manager

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestHostAppDecisionUsesGenericKindAndExactBindingScope(t *testing.T) {
	core, profileRecord, _, binding := projectionGrantFixture(t)
	if err := core.SetProjectionIdeMode(profileRecord.Name, ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	d, err := core.ensureProjectionTrustedDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != decision.KindHostAppOpenResource || d.ProviderRef.Provider != "" {
		// Public decisions deliberately redact providerRef. The persisted record is
		// checked below through the exact grant checker.
		t.Fatalf("generic public decision mismatch: %+v", d)
	}

	const contenders = 12
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, claimErr := core.ClaimDecision(DecisionClaimRequest{
				DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "concurrent-test",
			})
			results <- claimErr
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	claims := 0
	for claimErr := range results {
		if claimErr == nil {
			claims++
		}
	}
	if claims != 1 {
		t.Fatalf("successful claims=%d, want exactly one", claims)
	}

	private, err := core.inspectDecisionPrivate(d.ID)
	if err != nil || private.Claim == nil {
		t.Fatalf("private decision after claim=%+v err=%v", private, err)
	}
	claimToken := ""
	// A second claim after expiring the first lease obtains the only usable
	// token, proving stale concurrent claims carry no authority.
	store, err := core.decisionStore()
	if err != nil {
		t.Fatal(err)
	}
	store.SetNow(func() time.Time { return private.Claim.ExpiresAt.Add(time.Second) })
	claim, _, err := store.ClaimDecision(d.ID, "lease-replacement", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claimToken = claim.ClaimToken
	if _, err := core.ApproveDecision(DecisionResolveRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claimToken,
	}); err != nil {
		t.Fatal(err)
	}

	checker := projectionGrantCheckerForTest(core.Store.Root, binding)
	if !checker.TrustedGrantActive(binding.scope()) {
		t.Fatal("approved exact host-app decision did not activate")
	}
	mutations := map[string]func(*hostcap.GrantScope){
		"app":         func(scope *hostcap.GrantScope) { scope.QualifiedAppRef += ".other" },
		"pack":        func(scope *hostcap.GrantScope) { scope.PackID += ".other" },
		"revision":    func(scope *hostcap.GrantScope) { scope.RevisionID += ".other" },
		"binding":     func(scope *hostcap.GrantScope) { scope.BindingID += ".other" },
		"command":     func(scope *hostcap.GrantScope) { scope.Command += "-other" },
		"session":     func(scope *hostcap.GrantScope) { scope.SessionID += "-other" },
		"profile":     func(scope *hostcap.GrantScope) { scope.Profile += "-other" },
		"workspace":   func(scope *hostcap.GrantScope) { scope.WorkspaceID += "-other" },
		"environment": func(scope *hostcap.GrantScope) { scope.EnvironmentID += "-other" },
		"run":         func(scope *hostcap.GrantScope) { scope.RunID += "-other" },
		"digest":      func(scope *hostcap.GrantScope) { scope.BindingDigest = "sha256:other" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			scope := binding.scope()
			mutate(&scope)
			if checker.TrustedGrantActive(scope) {
				t.Fatalf("approval crossed %s boundary: %+v", name, scope)
			}
		})
	}

	changedIdentity := binding
	changedIdentity.IdentityID += "-other"
	if projectionGrantCheckerForTest(core.Store.Root, changedIdentity).TrustedGrantActive(changedIdentity.scope()) {
		t.Fatal("approval crossed identity boundary")
	}
	if err := core.invalidateHostAppDecisions(binding.PackID, binding.RevisionID, "binding-disabled"); err != nil {
		t.Fatal(err)
	}
	if checker.TrustedGrantActive(binding.scope()) {
		t.Fatal("disabled binding retained an approved decision")
	}
}

func TestHostAppDecisionTimeoutAndOwnerLossFailClosed(t *testing.T) {
	core, profileRecord, _, binding := projectionGrantFixture(t)
	if err := core.SetProjectionIdeMode(profileRecord.Name, ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	d, err := core.ensureProjectionTrustedDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	store, err := core.decisionStore()
	if err != nil {
		t.Fatal(err)
	}
	private, err := store.RawDecision(d.ID)
	if err != nil {
		t.Fatal(err)
	}
	private.TimeoutAt = time.Now().UTC().Add(-time.Second)
	if _, err := store.CreateOrUpdateDecision(private); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, Surface: "late-owner"}); err == nil {
		t.Fatal("timed-out decision was claimable")
	}
	if projectionGrantCheckerForTest(core.Store.Root, binding).TrustedGrantActive(binding.scope()) {
		t.Fatal("timed-out decision activated a grant")
	}

	other := binding
	other.SessionID += "-new"
	other.RunID += "-new"
	if err := core.invalidateProjectionGrant(d.ID, "owner-session-ended"); err != nil {
		t.Fatal(err)
	}
	if projectionGrantCheckerForTest(core.Store.Root, other).TrustedGrantActive(other.scope()) {
		t.Fatal("owner loss carried authority into another session")
	}
}

func TestHostAppUpdateDisableAndRevokeInvalidateApprovedExactDecisions(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	profileRecord, err := store.LoadOrInit("privacy")
	if err != nil {
		t.Fatal(err)
	}
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	if err := core.SetProjectionIdeMode("privacy", ProjectionIdeModeTrusted); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.decision-lifecycle", "decision-editor")
	addPlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(addPlan); err != nil {
		t.Fatal(err)
	}

	updateDecision := approveHostAppPlanDecision(t, core, profileRecord, addPlan, "update")
	manifest := readManagerHostAppManifest(t, packDir)
	manifest.Version = "1.0.1"
	manifest.Description = "decision lifecycle update"
	writeManagerHostAppManifest(t, packDir, manifest)
	updatePlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "update", SourceKind: hostapppack.SourceLocal, SourcePath: packDir,
		ProfileName: "privacy", PackID: addPlan.PackID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(updatePlan); err != nil {
		t.Fatal(err)
	}
	assertHostAppDecisionStale(t, core, updateDecision.ID, "update")

	disableDecision := approveHostAppPlanDecision(t, core, profileRecord, updatePlan, "disable")
	disablePlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "disable", ProfileName: "privacy", PackID: updatePlan.PackID, RevisionID: updatePlan.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(disablePlan); err != nil {
		t.Fatal(err)
	}
	assertHostAppDecisionStale(t, core, disableDecision.ID, "disable")

	enablePlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "enable", ProfileName: "privacy", PackID: updatePlan.PackID, RevisionID: updatePlan.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(enablePlan); err != nil {
		t.Fatal(err)
	}
	revokeDecision := approveHostAppPlanDecision(t, core, profileRecord, enablePlan, "revoke")
	revokePlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "revoke", PackID: updatePlan.PackID, RevisionID: updatePlan.RevisionID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(revokePlan); err != nil {
		t.Fatal(err)
	}
	assertHostAppDecisionStale(t, core, revokeDecision.ID, "revoke")
}

func approveHostAppPlanDecision(t *testing.T, core Core, profileRecord profile.Profile, plan HostAppPackPlan, suffix string) decision.Decision {
	t.Helper()
	binding := projectionGrantBinding{
		SessionID: "ses_decision_" + suffix, Profile: "privacy", Backend: "lima", RunID: "run_decision_" + suffix,
		WorkspaceID: "wrk_decision_" + suffix, EnvironmentID: "env_decision_" + suffix,
		ProfileID: profileRecord.Metadata["profileId"], IdentityID: profileRecord.Metadata["identityId"],
		PackID: plan.PackID, RevisionID: plan.RevisionID, BindingID: "open-resource",
		QualifiedApp: plan.PackID + "/" + plan.RevisionID + "/editor", BindingDigest: plan.ExpectedPermissionFingerprint,
		Command: "decision-editor", Subject: "command:decision-editor", ResourceClasses: hostapppack.ResourceWorkspace,
	}
	d, err := core.ensureProjectionTrustedDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "lifecycle-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApproveDecision(DecisionResolveRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken,
	}); err != nil {
		t.Fatal(err)
	}
	return d
}

func assertHostAppDecisionStale(t *testing.T, core Core, decisionID, operation string) {
	t.Helper()
	d, err := core.inspectDecisionPrivate(decisionID)
	if err != nil || d.State != decision.StateStale {
		t.Fatalf("%s retained exact decision authority: decision=%+v err=%v", operation, d, err)
	}
}
