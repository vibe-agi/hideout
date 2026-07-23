package backend

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestMachineActivationContractCannotCarryWorkspaceOrExecutionFacts(t *testing.T) {
	typeOf := reflect.TypeOf(MachineActivationSpec{})
	for i := range typeOf.NumField() {
		name := strings.ToLower(typeOf.Field(i).Name)
		for _, forbidden := range []string{"workspace", "hostwork", "guestwork", "command", "session", "terminal", "grant"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("machine activation field %q carries %s authority", typeOf.Field(i).Name, forbidden)
			}
		}
	}
	if _, ok := typeOf.FieldByName("EnvironmentID"); !ok {
		t.Fatal("machine activation lost environment identity")
	}
}

func TestWorkspaceAttachmentContractIsModeSpecific(t *testing.T) {
	root := t.TempDir()
	attachment := WorkspaceAttachmentSpec{
		HostRoot: root, GuestRoot: "/workspace", Transport: WorkspaceTransportPortal,
		Portal: &WorkspacePortalBinding{
			PhysicalGuestRoot: "/hideout/workspaces/wrk_fixture",
			Endpoint:          "host.lima.internal:43127", CredentialGuestPath: "/hideout/session/workspace/credential.bin",
		},
	}
	if err := attachment.Validate(environment.ModeShared); err != nil {
		t.Fatalf("shared portal attachment: %v", err)
	}
	if err := attachment.Validate(environment.ModeWorkspaceBound); err == nil {
		t.Fatal("workspace-bound mode accepted a dynamic shared attachment")
	}
	attachment.Transport = WorkspaceTransportStatic
	attachment.Portal = nil
	if err := attachment.Validate(environment.ModeWorkspaceBound); err != nil {
		t.Fatalf("workspace-bound static attachment: %v", err)
	}
	if err := attachment.Validate(environment.ModeShared); err == nil {
		t.Fatal("shared mode accepted a static project mapping")
	}
}

func TestMachineActivationValidationRequiresSharedMachineStateWithoutProject(t *testing.T) {
	root := t.TempDir()
	spec := MachineActivationSpec{
		EnvironmentID: "env_shared", ImageRef: environment.BuiltinBaseImage,
		Profile: profile.Default("default"), ProfileDir: root, RuntimeRoot: root,
		Mode: environment.ModeShared,
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("shared machine activation: %v", err)
	}
	spec.RuntimeRoot = ""
	if err := spec.Validate(); err == nil {
		t.Fatal("shared activation without retained machine state must fail")
	}
}

func TestSessionReadyProofBindsAuthenticatedSupervisorToExactSessionAndBoot(t *testing.T) {
	session := &Session{
		ID: "ses_ready", EnvironmentID: "env_ready", InstanceName: "hideout-ready",
		SessionSnapshotID: "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		ExpectedBootID:    "01234567-89ab-cdef-0123-456789abcdef",
	}
	proof, err := ReadyProofForSession(session, SessionReadyAuthenticatedSupervisor)
	if err != nil {
		t.Fatal(err)
	}
	if err := proof.ValidateSession(session, true); err != nil {
		t.Fatalf("authenticated proof: %v", err)
	}

	native := proof
	native.Source = SessionReadyNativeHarness
	native.BootID = ""
	nativeSession := *session
	nativeSession.ExpectedBootID = ""
	if err := native.ValidateSession(&nativeSession, true); err == nil {
		t.Fatal("shared ready barrier accepted a native harness proof")
	}

	for name, mutate := range map[string]func(*SessionReadyProof){
		"session":  func(value *SessionReadyProof) { value.SessionID = "ses_other" },
		"snapshot": func(value *SessionReadyProof) { value.SessionSnapshotID = "sha256:" + strings.Repeat("d", 64) },
		"boot":     func(value *SessionReadyProof) { value.BootID = "fedcba98-7654-3210-fedc-ba9876543210" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := proof
			mutate(&changed)
			if err := changed.ValidateSession(session, true); err == nil {
				t.Fatalf("ready proof with changed %s matched prepared session", name)
			}
		})
	}
}

func TestProjectionReadinessExpectationIsStrictAndCloneIsIndependent(t *testing.T) {
	manifest := ProjectionReadinessManifest{
		Schema:            ProjectionReadinessManifestSchema,
		SessionID:         "ses_ready",
		EnvironmentID:     "env_ready",
		SessionSnapshotID: "sha256:" + strings.Repeat("c", 64),
		CatalogDigest:     "sha256:" + strings.Repeat("d", 64),
		Entries: []ProjectionReadinessEntry{
			{Name: "code", RelativePath: "code", SHA256: "sha256:" + strings.Repeat("1", 64), Kind: ProjectionEntryCommand},
			{Name: "hideout-shim", RelativePath: "hideout-shim", SHA256: "sha256:" + strings.Repeat("2", 64), Kind: ProjectionEntryDispatcher},
		},
	}
	manifest.CatalogDigest = mustProjectionCatalogDigest(t, manifest)
	expectation := ProjectionReadinessExpectation{
		Manifest: manifest, ManifestRelativePath: ProjectionReadinessManifestFile,
		TargetProjected: true, Deadline: 2 * time.Second,
	}
	if err := expectation.Validate(); err != nil {
		t.Fatalf("valid readiness expectation: %v", err)
	}
	clone := CloneProjectionReadinessExpectation(&expectation)
	if clone == nil {
		t.Fatal("clone is nil")
	}
	clone.Manifest.Entries[0].Name = "changed"
	if expectation.Manifest.Entries[0].Name != "code" {
		t.Fatal("readiness clone shares entry storage")
	}

	for name, mutate := range map[string]func(*ProjectionReadinessExpectation){
		"manifest-path": func(value *ProjectionReadinessExpectation) { value.ManifestRelativePath = "../ready.json" },
		"deadline":      func(value *ProjectionReadinessExpectation) { value.Deadline = 0 },
		"catalog":       func(value *ProjectionReadinessExpectation) { value.Manifest.CatalogDigest = "sha256:bad" },
		"unsorted": func(value *ProjectionReadinessExpectation) {
			value.Manifest.Entries[0], value.Manifest.Entries[1] = value.Manifest.Entries[1], value.Manifest.Entries[0]
		},
		"symlink-path": func(value *ProjectionReadinessExpectation) { value.Manifest.Entries[0].RelativePath = "nested/code" },
		"duplicate": func(value *ProjectionReadinessExpectation) {
			value.Manifest.Entries[1].Name = value.Manifest.Entries[0].Name
			value.Manifest.Entries[1].RelativePath = value.Manifest.Entries[0].RelativePath
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := *CloneProjectionReadinessExpectation(&expectation)
			mutate(&changed)
			if err := changed.Validate(); err == nil {
				t.Fatalf("invalid readiness expectation %s passed", name)
			}
		})
	}
}

func TestProjectionReadinessObservationAndReadyProofBindCatalog(t *testing.T) {
	expectation := &ProjectionReadinessExpectation{
		ManifestRelativePath: ProjectionReadinessManifestFile,
		TargetProjected:      true,
		Deadline:             2 * time.Second,
		Manifest: ProjectionReadinessManifest{
			Schema: ProjectionReadinessManifestSchema, SessionID: "ses_ready", EnvironmentID: "env_ready",
			SessionSnapshotID: "sha256:" + strings.Repeat("c", 64),
			CatalogDigest:     "sha256:" + strings.Repeat("d", 64),
			Entries: []ProjectionReadinessEntry{
				{Name: "hideout-shim", RelativePath: "hideout-shim", SHA256: "sha256:" + strings.Repeat("2", 64), Kind: ProjectionEntryDispatcher},
			},
		},
	}
	expectation.Manifest.CatalogDigest = mustProjectionCatalogDigest(t, expectation.Manifest)
	session := &Session{
		ID: "ses_ready", EnvironmentID: "env_ready", InstanceName: "hideout-ready",
		SessionSnapshotID:   "sha256:" + strings.Repeat("c", 64),
		ExpectedBootID:      "01234567-89ab-cdef-0123-456789abcdef",
		ProjectionReadiness: expectation,
	}
	observation := ProjectionReadinessObservation{
		Status: ProjectionReadinessReady, CatalogDigest: expectation.Manifest.CatalogDigest,
		ExpectedEntries: 1, ObservedEntries: 1, DurationMillis: 27, TargetProjected: true,
	}
	if err := observation.Validate(expectation); err != nil {
		t.Fatalf("valid readiness observation: %v", err)
	}
	session.ProjectionReadinessObservation = &observation
	proof, err := ReadyProofForSession(session, SessionReadyAuthenticatedSupervisor)
	if err != nil {
		t.Fatal(err)
	}
	if err := proof.ValidateSession(session, true); err != nil {
		t.Fatalf("catalog-bound ready proof: %v", err)
	}

	wrong := proof
	wrong.ProjectionCatalogDigest = "sha256:" + strings.Repeat("e", 64)
	if err := wrong.ValidateSession(session, true); err == nil {
		t.Fatal("ready proof with foreign catalog matched prepared session")
	}
	refused := observation
	refused.Status = ProjectionReadinessRefused
	refused.ReasonCode = ProjectionReadinessEntryInvalid
	if err := refused.Validate(expectation); err != nil {
		t.Fatalf("valid refusal observation: %v", err)
	}
	refused.ReasonCode = ""
	if err := refused.Validate(expectation); err == nil {
		t.Fatal("refused readiness without reason passed")
	}
}

func mustProjectionCatalogDigest(t *testing.T, manifest ProjectionReadinessManifest) string {
	t.Helper()
	digest, err := ProjectionReadinessCatalogDigest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
