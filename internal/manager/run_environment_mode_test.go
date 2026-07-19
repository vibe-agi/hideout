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
