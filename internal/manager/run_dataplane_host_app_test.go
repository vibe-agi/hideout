package manager

import (
	"slices"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
)

func TestCompileRunProjectionGrantsKeepsMixedBindingAccessIndependent(t *testing.T) {
	core, profileRecord, runSession, _ := projectionGrantFixture(t)
	if err := core.SetProjectionHostAppMode(profileRecord.Name, ProjectionHostAppModeTrusted); err != nil {
		t.Fatal(err)
	}
	bindings := []hostcap.OpenResourceBinding{
		runProjectionTestBinding("builtin.vscode", "code-command", "vscode", "code", hostcap.BindingAccessAskEachRun),
		runProjectionTestBinding("community.safe-editor", "safe-command", "safe-editor", "safe-editor", hostcap.BindingAccessSafe),
		runProjectionTestBinding("community.ask-editor", "ask-command", "ask-editor", "ask-editor", hostcap.BindingAccessAskEachRun),
	}

	authority, err := workspaceAuthorityForRunSession(runSession)
	if err != nil {
		t.Fatal(err)
	}
	byCommand, required := compileRunProjectionGrants(runSession, authority, bindings)
	commands := make([]string, 0, len(required))
	for _, binding := range required {
		commands = append(commands, binding.Command)
	}
	slices.Sort(commands)
	if !slices.Equal(commands, []string{"ask-editor", "code"}) {
		t.Fatalf("decision commands=%v, want built-in and external ask-each-run only", commands)
	}
	if _, ok := byCommand["safe-editor"]; ok {
		t.Fatal("profile host-app mode created authority for an unrelated safe binding")
	}
	if byCommand["code"].PackID != "builtin.vscode" || byCommand["ask-editor"].PackID != "community.ask-editor" {
		t.Fatalf("run grant bindings crossed owners: %+v", byCommand)
	}
	if bindings[1].Access != hostcap.BindingAccessSafe {
		t.Fatal("grant compilation mutated external binding access")
	}
}

func TestRunProjectionAskEachRunDecisionDoesNotDependOnProfileIDEMode(t *testing.T) {
	core, profileRecord, _, binding := projectionGrantFixture(t)
	if got := ReadProjectionHostAppMode(core.Store.Root, profileRecord.Name); got != ProjectionHostAppModeSafe {
		t.Fatalf("profile host-app mode=%q, want compatibility default safe", got)
	}

	d, err := core.ensureRunProjectionDecision(binding)
	if err != nil {
		t.Fatal(err)
	}
	checker := runProjectionGrantChecker{
		storeRoot: core.Store.Root,
		bindings:  map[string]projectionGrantBinding{binding.Command: binding},
	}
	if checker.TrustedGrantActive(binding.scope()) {
		t.Fatal("pending external decision became authority")
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApproveDecision(DecisionResolveRequest{
		DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken,
	}); err != nil {
		t.Fatal(err)
	}
	if !checker.TrustedGrantActive(binding.scope()) {
		t.Fatal("approved external ask-each-run decision still depended on profile host-app mode")
	}
	if got := ReadProjectionHostAppMode(core.Store.Root, profileRecord.Name); got != ProjectionHostAppModeSafe {
		t.Fatalf("external decision changed compatibility mode to %q", got)
	}
}

func TestProfileIDEModeCompatibilityDerivesBuiltinBindingAndSafetyFromPackData(t *testing.T) {
	core, profileRecord, _, _ := projectionGrantFixture(t)
	safeSources, err := core.hostAppCatalogSources(profileRecord.Name)
	if err != nil {
		t.Fatal(err)
	}
	safeBuiltin := builtinSourceForProfileIDEModeTest(t, safeSources)
	if safeBuiltin.enablement.Access != hostapppack.AccessSafe {
		t.Fatalf("default built-in access=%q, want safe", safeBuiltin.enablement.Access)
	}
	wantBindingIDs := make([]string, 0, len(safeBuiltin.manifest.Bindings))
	apps := make(map[string]hostapppack.AppSpec, len(safeBuiltin.manifest.Apps))
	for _, app := range safeBuiltin.manifest.Apps {
		apps[app.ID] = app
	}
	wantSafetyID := ""
	for _, binding := range safeBuiltin.manifest.Bindings {
		wantBindingIDs = append(wantBindingIDs, binding.ID)
		requested := apps[binding.AppID].RequestedSafetyProfile
		if wantSafetyID == "" {
			wantSafetyID = requested
		} else if requested != wantSafetyID {
			t.Fatalf("fixture requires multiple safety profiles: %q and %q", wantSafetyID, requested)
		}
	}
	slices.Sort(wantBindingIDs)
	if !slices.Equal(safeBuiltin.enablement.BindingIDs, wantBindingIDs) {
		t.Fatalf("built-in binding ids=%v want data-derived %v", safeBuiltin.enablement.BindingIDs, wantBindingIDs)
	}
	profile, err := hostcap.CoreSafetyProfile(wantSafetyID)
	if err != nil {
		t.Fatal(err)
	}
	wantPermission, err := hostapppack.EffectivePermissionFingerprint(
		trustedBuiltinFingerprintManifest(safeBuiltin.manifest),
		hostapppack.EffectivePermissionContext{Access: hostapppack.AccessSafe, SafetyProfileID: profile.ID, SafetyProfileVersion: profile.Version},
	)
	if err != nil {
		t.Fatal(err)
	}
	if safeBuiltin.enablement.PermissionFingerprint != wantPermission {
		t.Fatalf("built-in permission fingerprint was not derived from pack/profile data")
	}

	if err := core.SetProjectionHostAppMode(profileRecord.Name, ProjectionHostAppModeTrusted); err != nil {
		t.Fatal(err)
	}
	trustedSources, err := core.hostAppCatalogSources(profileRecord.Name)
	if err != nil {
		t.Fatal(err)
	}
	trustedBuiltin := builtinSourceForProfileIDEModeTest(t, trustedSources)
	if trustedBuiltin.enablement.Access != hostapppack.AccessAskEachRun {
		t.Fatalf("trusted compatibility access=%q, want ask-each-run", trustedBuiltin.enablement.Access)
	}
	if !slices.Equal(trustedBuiltin.enablement.BindingIDs, wantBindingIDs) {
		t.Fatalf("trusted compatibility changed data-derived bindings: %v", trustedBuiltin.enablement.BindingIDs)
	}
}

func runProjectionTestBinding(packID, bindingID, appID, command, access string) hostcap.OpenResourceBinding {
	return hostcap.OpenResourceBinding{
		PackID: packID, RevisionID: "rev_0123456789abcdef", BindingID: bindingID,
		QualifiedAppRef: packID + "/rev_0123456789abcdef/" + appID,
		BindingDigest:   "sha256:test-" + command, Commands: []string{command}, Access: access,
		ResourceKinds: []hostcap.ResourceKind{hostcap.KindWorkspace},
	}
}

func builtinSourceForProfileIDEModeTest(t *testing.T, sources []hostAppCatalogSource) hostAppCatalogSource {
	t.Helper()
	for _, source := range sources {
		if source.builtIn {
			return source
		}
	}
	t.Fatal("built-in host-app catalog source is absent")
	return hostAppCatalogSource{}
}

// TestRunProjectionGrantChecksPersistentGrantBeforeDecision proves US1: a
// durable trusted-host-app workspace grant authorizes the open through
// runProjectionGrantChecker WITHOUT any per-run decision. This is the one-shot
// deadlock fix.
func TestRunProjectionGrantChecksPersistentGrantBeforeDecision(t *testing.T) {
	root := t.TempDir()
	binding := projectionGrantBinding{
		Profile: "default", Backend: "lima", Command: "code",
		SessionID: "ses_1", RunID: "ses_1", EnvironmentID: "env_1",
		WorkspaceID: "wrk_t007", QualifiedApp: "builtin.vscode/rev_1/vscode",
		BindingDigest: "sha256:t007", ResourceClasses: "workspace",
	}
	checker := runProjectionGrantChecker{storeRoot: root, bindings: map[string]projectionGrantBinding{binding.Command: binding}}
	scope := binding.scope()

	// No grant, no decision → not active.
	if checker.TrustedGrantActive(scope) {
		t.Fatal("active with neither grant nor decision")
	}
	// Trusted mode + persistent grant → active, no per-run decision needed.
	if err := WriteProjectionHostAppMode(root, "default", ProjectionHostAppModeTrusted, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := addTrustedHostAppGrant(root, "default", TrustedHostAppGrant{
		WorkspaceID: scope.WorkspaceID, QualifiedAppRef: scope.QualifiedAppRef, BindingDigest: scope.BindingDigest,
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !checker.TrustedGrantActive(scope) {
		t.Fatal("persistent grant did not authorize without a per-run decision")
	}
}
