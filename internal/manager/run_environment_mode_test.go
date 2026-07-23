package manager

import (
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestAutomaticEnvironmentModeMatrixIsExplicit(t *testing.T) {
	p := profile.Default("matrix")
	p.Workspace.PathMode = profile.WorkspacePathModeAlias
	workspace := t.TempDir()
	for _, test := range []struct {
		name    string
		backend string
		hostOS  string
		arch    string
		mode    environment.Mode
	}{
		{name: "promoted Lima", backend: "lima", hostOS: "darwin", arch: "arm64", mode: environment.ModeShared},
		{name: "Linux Lima", backend: "lima", hostOS: "linux", arch: "arm64", mode: environment.ModeWorkspaceBound},
		{name: "Intel macOS Lima", backend: "lima", hostOS: "darwin", arch: "amd64", mode: environment.ModeWorkspaceBound},
		{name: "native", backend: "native", hostOS: "darwin", arch: "arm64", mode: environment.ModeWorkspaceBound},
	} {
		t.Run(test.name, func(t *testing.T) {
			spec, err := automaticRunEnvironmentSpecForPlatform(p, test.backend, workspace, "/workspace", test.hostOS, test.arch)
			if err != nil {
				t.Fatal(err)
			}
			if spec.Mode != test.mode {
				t.Fatalf("mode=%q want %q: %+v", spec.Mode, test.mode, spec)
			}
			if test.mode == environment.ModeShared {
				if spec.Name != environment.SharedDisplayName(p.Name) || spec.SharedSlot != environment.SharedSlotID(p.Name) ||
					spec.BoundWorkspace != "" || spec.DedicatedWorkspace != "" {
					t.Fatalf("invalid shared spec: %+v", spec)
				}
			} else if spec.BoundWorkspace != workspace || spec.BoundGuestRoot != "/workspace" || spec.SharedSlot != "" {
				t.Fatalf("invalid workspace-bound spec: %+v", spec)
			}
		})
	}
}

func TestPromotedSharedSelectionRejectsPreserveWithExecutableGuidance(t *testing.T) {
	p := profile.Default("preserve")
	p.Workspace.PathMode = profile.WorkspacePathModePreserve
	_, err := automaticRunEnvironmentSpecForPlatform(p, "lima", t.TempDir(), t.TempDir(), "darwin", "arm64")
	if err == nil {
		t.Fatal("promoted shared selection accepted preserve path mode")
	}
	for _, want := range []string{"pathMode", "hideout profile workspace-path-mode preserve alias", "hideout env create", "--env"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("preserve guidance missing %q: %v", want, err)
		}
	}
}

func TestDedicatedPathModeFlipRequiresRecreateAndNeverSilentlyRemaps(t *testing.T) {
	aliasProfile := runtimeConfigurationTestProfile("path-mode-drift")
	aliasProfile.Workspace.PathMode = profile.WorkspacePathModeAlias
	preserveProfile := aliasProfile
	preserveProfile.Workspace.PathMode = profile.WorkspacePathModePreserve

	aliasConfiguration, err := RuntimeConfigurationForProfile(aliasProfile, "lima", environment.ModeDedicated)
	if err != nil {
		t.Fatal(err)
	}
	preserveConfiguration, err := RuntimeConfigurationForProfile(preserveProfile, "lima", environment.ModeDedicated)
	if err != nil {
		t.Fatal(err)
	}
	if aliasConfiguration.Machine.StaticWorkspace == nil ||
		aliasConfiguration.Machine.StaticWorkspace.PathMode != profile.WorkspacePathModeAlias {
		t.Fatalf("alias path mode was silently remapped: %+v", aliasConfiguration.Machine.StaticWorkspace)
	}
	if preserveConfiguration.Machine.StaticWorkspace == nil ||
		preserveConfiguration.Machine.StaticWorkspace.PathMode != profile.WorkspacePathModePreserve {
		t.Fatalf("preserve path mode was silently remapped: %+v", preserveConfiguration.Machine.StaticWorkspace)
	}
	if aliasConfiguration.Layers.MachineID == preserveConfiguration.Layers.MachineID {
		t.Fatal("pathMode flip did not change machine identity")
	}
	if aliasConfiguration.Layers.SessionID == preserveConfiguration.Layers.SessionID {
		t.Fatal("pathMode flip did not change session identity")
	}
	changes := environment.CompareConfigurations(aliasConfiguration, preserveConfiguration)
	if got := environment.RequiredImpact(changes); got != environment.ImpactRecreate {
		t.Fatalf("pathMode flip impact=%q want=%q changes=%+v", got, environment.ImpactRecreate, changes)
	}
	layers := map[string]bool{}
	for _, change := range changes {
		layers[change.Layer] = true
	}
	if !layers["machine"] || !layers["session"] {
		t.Fatalf("pathMode flip changed layers=%v want machine+session", layers)
	}
}

func TestNamedEnvironmentIsDedicatedAndRejectsAnotherProject(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	core := New(store)
	sharedRunProfile(t, store, "default")
	firstWorkspace := t.TempDir()
	record, err := core.CreateEnvironment(EnvironmentCreateOptions{
		Name: "project-wall", Profile: "default", Backend: "lima", Workspace: firstWorkspace,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Mode != environment.ModeDedicated || record.DedicatedWorkspace != firstWorkspace || record.BoundWorkspace != "" {
		t.Fatalf("named environment is not dedicated: %+v", record)
	}
	p, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	_, err = SelectRunEnvironment(
		environment.Store{Root: store.Root}, p, "lima", t.TempDir(), "/workspace", false,
		RunEnvironmentOptions{EnvName: "project-wall", Create: true},
	)
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("named environment accepted another project: %v", err)
	}
}
