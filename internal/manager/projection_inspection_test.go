package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/audit"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/recovery"
	"github.com/vibe-agi/hideout/internal/session"
)

func TestHostAppInspectionSharesBoundedIdentityPermissionAndRecoveryFacts(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.inspect", "inspect-editor")
	manifest := readManagerHostAppManifest(t, packDir)
	manifest.Description = "inspection editor"
	manifest.InstallHint = &hostapppack.InstallHint{Text: "Copy this installation hint", URL: "https://example.test/editor"}
	writeManagerHostAppManifest(t, packDir, manifest)
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	plan, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(plan); err != nil {
		t.Fatal(err)
	}

	inspection, err := core.HostAppInspection("privacy", manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Schema != hostapppack.InspectionVersion || len(inspection.Entries) != 1 {
		t.Fatalf("inspection=%+v", inspection)
	}
	entry := inspection.Entries[0]
	if entry.Summary.Command != "inspect-editor" || entry.Summary.Readiness != "ready" || entry.Summary.Access != hostapppack.AccessAskEachRun {
		t.Fatalf("summary=%+v", entry.Summary)
	}
	if entry.Package.ID != manifest.ID || entry.Package.TestStatus != hostapppack.TestPassed || entry.Permissions.Status != "accepted" || entry.Permissions.Fingerprint != plan.ExpectedPermissionFingerprint {
		t.Fatalf("package/permissions=%+v %+v", entry.Package, entry.Permissions)
	}
	if entry.AppIdentity.Verification != "unverified" || entry.AppIdentity.ContentDigest == "" || entry.Safety.Posture != "unverified-app" {
		t.Fatalf("identity/safety=%+v %+v", entry.AppIdentity, entry.Safety)
	}
	if entry.Binding.ShadowStatus != "owned" || entry.Binding.CapabilityID != hostapppack.CapabilityOpenResource || entry.Runtime.GrantState != "pending" {
		t.Fatalf("binding/runtime=%+v %+v", entry.Binding, entry.Runtime)
	}
	if entry.Hint == nil || !entry.Hint.Untrusted || entry.Hint.Text != manifest.InstallHint.Text {
		t.Fatalf("untrusted hint=%+v", entry.Hint)
	}
	assertHostAppInspectionSchema(t, inspection)

	projection, err := core.ProjectionInspection("privacy")
	if err != nil {
		t.Fatal(err)
	}
	if len(projection.HostApps.Entries) == 0 || !containsString(projection.RecoveryCodes, recovery.CodeHostAppNewRunRequired) {
		t.Fatalf("projection did not consume shared host-app status: %+v", projection)
	}
	blob, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{root, os.Getenv("HOME"), "Contents/MacOS/Editor", "--reuse-window"} {
		if leak != "" && strings.Contains(string(blob), leak) {
			t.Fatalf("inspection leaked %q: %s", leak, blob)
		}
	}
}

func assertHostAppInspectionSchema(t *testing.T, inspection hostapppack.Inspection) {
	t.Helper()
	data, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	schemaData, err := os.ReadFile(filepath.Join("..", "..", "schemas", "host-app-inspection.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaData))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("host-app-inspection.schema.json", schemaDoc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("host-app-inspection.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(doc); err != nil {
		t.Fatalf("generated host-app inspection violates schema: %v\n%s", err, data)
	}
}

func TestHostAppInspectionClassifiesUnavailableIdentityWithoutTrustingPackageProse(t *testing.T) {
	for _, tc := range []struct {
		name         string
		resolveError error
		verification string
	}{
		{name: "absent", resolveError: &hostcap.Error{Code: hostcap.CodeAppAbsent, Reason: "not installed"}, verification: "absent"},
		{name: "drifted", resolveError: &hostcap.Error{Code: hostcap.CodeAppIdentityDrift, Reason: "changed"}, verification: "drifted"},
		{name: "unsupported", resolveError: errors.New("unsupported host"), verification: "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			store := profile.Store{Root: filepath.Join(root, "store")}
			if _, err := store.LoadOrInit("privacy"); err != nil {
				t.Fatal(err)
			}
			packDir := writeManagerHostAppPack(t, root, "community."+tc.name, tc.name+"-editor")
			core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
			install, err := core.PlanHostAppPack(HostAppPackOptions{Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, InstallOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := core.ApplyHostAppPack(install); err != nil {
				t.Fatal(err)
			}
			core.HostAppIdentityResolver = func(hostcap.ApplicationExpectation, []string) (hostcap.ObservedApplicationIdentity, error) {
				return hostcap.ObservedApplicationIdentity{}, tc.resolveError
			}
			inspection, err := core.HostAppInspection("privacy", install.PackID)
			if err != nil {
				t.Fatal(err)
			}
			if len(inspection.Entries) != 1 || inspection.Entries[0].AppIdentity.Verification != tc.verification || inspection.Entries[0].Summary.Readiness != "unavailable" || inspection.Entries[0].Safety.Posture != "unavailable" {
				t.Fatalf("classification=%+v", inspection)
			}
		})
	}
}

func TestHostAppInspectionReportsVerifiedSafeIdentity(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := filepath.Join(root, "verified-pack")
	if err := os.MkdirAll(packDir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest, err := builtinHostAppManifest()
	if err != nil {
		t.Fatal(err)
	}
	manifest.ID = "community.verified-inspection"
	manifest.Version = "1.0.0"
	manifest.Description = "verified inspection fixture"
	manifest.Bindings[0].Commands = []string{"verified-editor"}
	manifest.Tests = nil
	writeManagerHostAppManifest(t, packDir, manifest)
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	plan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Review.ApplicationsObserved) != 1 || plan.Review.ApplicationsObserved[0].Verification != "verified" || plan.Review.ApplicationsObserved[0].IdentityDigest == "" {
		t.Fatalf("verified plan review=%+v", plan.Review)
	}
	if _, err := core.ApplyHostAppPack(plan); err != nil {
		t.Fatal(err)
	}
	inspection, err := core.HostAppInspection("privacy", manifest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Entries) != 1 || inspection.Entries[0].AppIdentity.Verification != "verified" ||
		inspection.Entries[0].Summary.Access != hostapppack.AccessSafe || inspection.Entries[0].Safety.Posture != "safe" {
		t.Fatalf("verified inspection=%+v", inspection)
	}
	assertHostAppInspectionSchema(t, inspection)
}

func TestHostAppInspectionReportsConflictGrantOutcomeAndExactRecovery(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	profileRecord, err := store.LoadOrInit("privacy")
	if err != nil {
		t.Fatal(err)
	}
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	firstDir := writeManagerHostAppPack(t, root, "community.inspect-first", "shared-inspect")
	firstPlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: firstDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(firstPlan); err != nil {
		t.Fatal(err)
	}

	secondDir := writeManagerHostAppPack(t, root, "community.inspect-second", "shared-inspect")
	secondPlan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: secondDir, InstallOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(secondPlan); err != nil {
		t.Fatal(err)
	}
	secondInspection, secondRecovery, err := core.hostAppInspectionWithRecovery("privacy", secondPlan.PackID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondInspection.Entries) != 1 || secondInspection.Entries[0].Binding.ShadowStatus != "conflict" ||
		secondInspection.Entries[0].Permissions.Status != "review-required" || !containsString(secondRecovery, recovery.CodeHostAppCommandConflict) {
		t.Fatalf("conflict inspection=%+v recovery=%v", secondInspection, secondRecovery)
	}

	runSession := RunSession{
		Plan:        RunPlan{ProfileName: "privacy", Backend: "lima", Workspace: filepath.Join(root, "workspace"), RuntimeProfile: profileRecord},
		Environment: RunEnvironment{Active: true, Record: environment.Record{ID: "env_inspection", Profile: "privacy"}},
		Layout:      session.Layout{ID: "ses_inspection"},
	}
	grantBinding := projectionGrantBindingForRun(runSession, runSessionWorkspaceAuthority{
		WorkspaceID: "wrk_inspection", HostRoot: runSession.Plan.Workspace, GuestRoot: "/workspace",
	}, hostcap.OpenResourceBinding{
		PackID: firstPlan.PackID, RevisionID: firstPlan.RevisionID, BindingID: "open-resource",
		QualifiedAppRef: firstPlan.PackID + "/" + firstPlan.RevisionID + "/editor",
		BindingDigest:   firstPlan.ExpectedPermissionFingerprint, Commands: []string{"shared-inspect"},
		ResourceKinds: []hostcap.ResourceKind{hostcap.KindWorkspace},
	}, "shared-inspect")
	if err := core.SetProjectionHostAppMode("privacy", ProjectionHostAppModeTrusted); err != nil {
		t.Fatal(err)
	}
	d, err := core.ensureProjectionTrustedDecision(grantBinding)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := core.ClaimDecision(DecisionClaimRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, Surface: "inspection-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApproveDecision(DecisionResolveRequest{DecisionID: d.ID, ExpectedVersion: decision.DecisionVersion, ClaimToken: claim.ClaimToken}); err != nil {
		t.Fatal(err)
	}
	layout, err := session.New(store.Root)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := audit.NewFile(layout.AuditPath)
	if err != nil {
		t.Fatal(err)
	}
	event := audit.Event{
		Session: layout.ID, Profile: "privacy", Backend: "lima", Action: "host.app.refuse", Decision: "deny",
		Details: map[string]any{
			"packId": firstPlan.PackID, "revisionId": firstPlan.RevisionID, "bindingId": "open-resource",
			"command": "shared-inspect", "outcome": "resource-refused", "recoveryCode": recovery.CodeHostAppPortalUnavailable,
		},
	}
	if err := writer.Emit(event); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	firstInspection, firstRecovery, err := core.hostAppInspectionWithRecovery("privacy", firstPlan.PackID)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstInspection.Entries) != 1 || firstInspection.Entries[0].Runtime.GrantState != "approved" ||
		firstInspection.Entries[0].Runtime.LastOutcome != "resource-refused" || firstInspection.Entries[0].Runtime.AuditRef == "" ||
		!containsString(firstRecovery, recovery.CodeHostAppPortalUnavailable) {
		t.Fatalf("grant/outcome inspection=%+v recovery=%v", firstInspection, firstRecovery)
	}
}

func TestHostAppInspectionReportsSourceDigestDriftWithoutPathLeak(t *testing.T) {
	root := t.TempDir()
	store := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := store.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	packDir := writeManagerHostAppPack(t, root, "community.inspect-drift", "drift-editor")
	core := Core{Store: store, HostAppPlatform: hostcap.PlatformDarwin}
	configureManagerHostAppIdentity(t, &core, root)
	plan, err := core.PlanHostAppPack(HostAppPackOptions{
		Operation: "add", SourceKind: hostapppack.SourceLocal, SourcePath: packDir, ProfileName: "privacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.ApplyHostAppPack(plan); err != nil {
		t.Fatal(err)
	}
	installedDir := hostapppack.NewStore(store.Root).SourceDir(plan.PackID, plan.RevisionID)
	manifest := readManagerHostAppManifest(t, installedDir)
	manifest.Description = "tampered installed description"
	writeManagerHostAppManifest(t, installedDir, manifest)
	inspection, recoveryCodes, err := core.hostAppInspectionWithRecovery("privacy", plan.PackID)
	if err != nil {
		t.Fatal(err)
	}
	if len(inspection.Entries) != 1 || inspection.Entries[0].Summary.Readiness != "unavailable" ||
		!containsString(recoveryCodes, recovery.CodeHostAppDigestMismatch) {
		t.Fatalf("source drift inspection=%+v recovery=%v", inspection, recoveryCodes)
	}
	data, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), root) || strings.Contains(string(data), installedDir) {
		t.Fatalf("source drift inspection leaked a host path: %s", data)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
