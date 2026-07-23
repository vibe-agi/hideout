package lima

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestSharedMachineConfigContainsNoSelectedWorkspace(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "operator-project-secret")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	machine := backend.MachineActivationSpec{
		EnvironmentID: "env_shared", ImageRef: environment.BuiltinBaseImage,
		Profile: profile.Default("default"), ProfileDir: filepath.Join(root, "profile"),
		IdentityRoot: filepath.Join(root, "profile"), RuntimeRoot: filepath.Join(root, "runtime"),
		InstanceName: "hideout-default", PreserveInstance: true, Mode: environment.ModeShared,
	}
	cfg, err := ConfigForMachineSpec(machine, nil)
	if err != nil {
		t.Fatal(err)
	}
	encoded := configYAML(t, cfg)
	if bytes.Contains(encoded, []byte(project)) || bytes.Contains(encoded, []byte("/workspace")) || bytes.Contains(encoded, []byte("operator-project-secret")) {
		t.Fatalf("shared machine config contains project authority:\n%s", encoded)
	}
	for _, mount := range cfg.Mounts {
		if mount.Location == project || mount.MountPoint == "/workspace" {
			t.Fatalf("shared machine has project mount: %+v", mount)
		}
	}
	if _, err := ConfigForMachineSpec(machine, &StaticRunMounts{
		Workspace:  backend.WorkspaceAttachmentSpec{HostRoot: project, GuestRoot: "/workspace", Transport: backend.WorkspaceTransportStatic},
		SessionDir: filepath.Join(root, "session"),
	}); err == nil {
		t.Fatal("shared machine accepted a static project mount")
	}
}

func TestSharedPrepareKeepsWorkspaceOutOfRetainedLimaYAML(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := testRunSpec(root)
	spec.Machine.Mode = environment.ModeShared
	spec.Machine.EnvironmentID = "env_shared"
	spec.Machine.RuntimeRoot = filepath.Join(root, "environment-runtime")
	spec.Workspace = backend.WorkspaceAttachmentSpec{
		HostRoot: project, GuestRoot: "/workspace", Transport: backend.WorkspaceTransportPortal,
		Portal: &backend.WorkspacePortalBinding{
			PhysicalGuestRoot: "/hideout/workspaces/wrk_fixture",
			Endpoint:          "host.lima.internal:43127", CredentialGuestPath: "/hideout/session/workspace/credential.bin",
		},
	}
	session, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(session.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(project)) || bytes.Contains(data, []byte("mountPoint: /workspace")) {
		t.Fatalf("shared Prepare persisted attachment facts:\n%s", data)
	}
	if session.HostWork != project || session.GuestWork != "/workspace" {
		t.Fatalf("execution session lost separate attachment: %+v", session)
	}
}

func TestSharedRuntimeVerificationPrepareRequiresNoWorkspaceAuthority(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.Mode = environment.ModeShared
	spec.Machine.EnvironmentID = "env_shared"
	spec.Machine.RuntimeRoot = filepath.Join(root, "environment-runtime")
	spec.Workspace = backend.WorkspaceAttachmentSpec{}
	spec.Command = nil
	spec.RuntimeContract = &backend.RuntimeContract{ID: "runtime-v1"}
	spec.RuntimeInstanceExpected = &backend.RuntimeInstanceExpectation{}

	session, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if session.HostWork != "" || session.GuestWork != "/" ||
		session.Workspace != (backend.WorkspaceAttachmentSpec{}) {
		t.Fatalf("runtime verification gained workspace authority: %+v", session)
	}
	data, err := os.ReadFile(session.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("/workspace")) {
		t.Fatalf("shared runtime verification config contains workspace authority:\n%s", data)
	}
}

func TestSharedPrepareWithoutRuntimeVerificationRequiresWorkspacePortal(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.Mode = environment.ModeShared
	spec.Machine.EnvironmentID = "env_shared"
	spec.Machine.RuntimeRoot = filepath.Join(root, "environment-runtime")
	spec.Workspace = backend.WorkspaceAttachmentSpec{}
	spec.Command = nil

	if _, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec); err == nil {
		t.Fatal("shared Prepare accepted an empty workspace without runtime verification authority")
	}
}

func TestSharedRuntimeVerificationPrepareRejectsWorkspaceAuthority(t *testing.T) {
	root := t.TempDir()
	spec := testRunSpec(root)
	spec.Machine.Mode = environment.ModeShared
	spec.Machine.EnvironmentID = "env_shared"
	spec.Machine.RuntimeRoot = filepath.Join(root, "environment-runtime")
	spec.Workspace = backend.WorkspaceAttachmentSpec{
		HostRoot: filepath.Join(root, "project"), GuestRoot: "/workspace", Transport: backend.WorkspaceTransportPortal,
		Portal: &backend.WorkspacePortalBinding{
			PhysicalGuestRoot: "/hideout/workspaces/wrk_fixture",
			Endpoint:          "host.lima.internal:43127", CredentialGuestPath: "/hideout/session/workspace/credential.bin",
		},
	}
	spec.Command = nil
	spec.RuntimeContract = &backend.RuntimeContract{ID: "runtime-v1"}
	spec.RuntimeInstanceExpected = &backend.RuntimeInstanceExpectation{}

	if _, err := (Backend{Runner: fakeRunner{lookPath: "/opt/homebrew/bin/limactl"}}).Prepare(context.Background(), spec); err == nil {
		t.Fatal("shared runtime verification accepted workspace attachment authority")
	}
}

func TestWorkspaceBoundConfigRetainsExactStaticMapping(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "project")
	machine := backend.MachineActivationSpec{
		ImageRef: environment.BuiltinBaseImage, Profile: profile.Default("default"),
		ProfileDir: filepath.Join(root, "profile"), IdentityRoot: filepath.Join(root, "profile"),
		Mode: environment.ModeWorkspaceBound,
	}
	static := &StaticRunMounts{
		Workspace:  backend.WorkspaceAttachmentSpec{HostRoot: project, GuestRoot: "/workspace", Transport: backend.WorkspaceTransportStatic},
		SessionDir: filepath.Join(root, "session"),
	}
	cfg, err := ConfigForMachineSpec(machine, static)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Mounts) == 0 || cfg.Mounts[0] != (mount{Location: project, MountPoint: "/workspace", Writable: true}) {
		t.Fatalf("workspace-bound exact mount missing: %+v", cfg.Mounts)
	}
}

func configYAML(t *testing.T, cfg limaConfig) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lima.yaml")
	if err := WriteConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
