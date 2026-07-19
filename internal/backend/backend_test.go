package backend

import (
	"reflect"
	"strings"
	"testing"

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
