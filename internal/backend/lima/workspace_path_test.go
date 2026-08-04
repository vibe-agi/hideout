package lima_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/backend/lima"
	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestWorkspacePathPlanKeepsLogicalNavigationAndOpaquePhysicalIdentity(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	plan, err := lima.BuildWorkspacePathPlan(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if plan.LogicalRoot != workspaceattach.LogicalWorkspaceRoot ||
		plan.PhysicalRoot != workspaceattach.PhysicalWorkspaceBase+"/"+workspaceID ||
		plan.Mechanism != lima.WorkspacePathMechanism {
		t.Fatalf("path plan = %#v", plan)
	}
	if strings.Contains(plan.PhysicalRoot, "/Users/") || plan.LogicalRoot == plan.PhysicalRoot {
		t.Fatalf("path plan exposes or merges host/project identity: %#v", plan)
	}
	if _, err := lima.BuildWorkspacePathPlan("../../escape"); err == nil {
		t.Fatal("escaping workspace id accepted")
	}

	command, err := lima.BuildSessionViewCommand(lima.SessionViewSpec{
		SessionID:  "ses_20260717T000000Z_0123456789abcdef",
		TargetUser: "developer",
		GuestWork:  plan.LogicalRoot,
		Command:    []string{"bash"},
		Workspace: backend.WorkspaceAttachmentSpec{
			HostRoot:  "/Users/alice/project",
			GuestRoot: plan.LogicalRoot,
			Transport: backend.WorkspaceTransportPortal,
			Portal: &backend.WorkspacePortalBinding{
				PhysicalGuestRoot:   plan.PhysicalRoot,
				Endpoint:            "host.lima.internal:43127",
				CredentialGuestPath: workspaceattach.PortalCredentialGuestPath,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(command, " ")
	if !strings.Contains(joined, `'hideout-session-target' '/workspace'`) ||
		!strings.Contains(joined, `cd "$1"`) || strings.Contains(joined, "/Users/") {
		t.Fatalf("session shell navigation does not retain the logical root: %s", joined)
	}
	if !strings.Contains(joined, "ln -s '"+plan.PhysicalRoot+"' \"$workspace_root/workspace\"") {
		t.Fatalf("session view does not retain the opaque physical project identity: %s", joined)
	}
	if strings.Contains(joined, "mount --rbind '"+plan.PhysicalRoot+"' \"$workspace_root/workspace\"") {
		t.Fatalf("session view collapsed the project identity to the fixed logical root: %s", joined)
	}
}

func TestWorkspaceGitTrustNeedsNoSafeDirectoryForSyntheticOwnership(t *testing.T) {
	attachment := workspaceAttachmentFixture(t)
	got, err := manager.WorkspaceGitSafeDirectories(attachment)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("synthetic target ownership should need no Git trust exception, got %q", got)
	}
	for _, forbidden := range []string{"*", workspaceattach.LogicalWorkspaceRoot, attachment.PhysicalGuestRoot} {
		if slices.Contains(got, forbidden) {
			t.Fatalf("Git trust widened to %q", forbidden)
		}
	}
}

func TestAliasWorkspacePreflightRejectsExternalGitMetadata(t *testing.T) {
	t.Run("ordinary repository", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := manager.ValidateAliasWorkspaceMetadata(root); err != nil {
			t.Fatalf("ordinary repository rejected: %v", err)
		}
	})

	for _, test := range []struct {
		name  string
		setup func(t *testing.T, root, outside string)
	}{
		{name: "linked worktree gitdir", setup: func(t *testing.T, root, outside string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+outside+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "external commondir", setup: func(t *testing.T, root, outside string) {
			t.Helper()
			gitDir := filepath.Join(root, ".git")
			if err := os.Mkdir(gitDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte(outside+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked git metadata", setup: func(t *testing.T, root, outside string) {
			t.Helper()
			if err := os.Symlink(outside, filepath.Join(root, ".git")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			test.setup(t, root, outside)
			err := manager.ValidateAliasWorkspaceMetadata(root)
			var external manager.ExternalWorkspaceMetadataError
			if !errors.As(err, &external) {
				t.Fatalf("external Git metadata error = %T %v", err, err)
			}
			if strings.Contains(err.Error(), outside) {
				t.Fatalf("public recovery error exposed external host path: %v", err)
			}
		})
	}
}

func workspaceAttachmentFixture(t *testing.T) workspaceattach.Attachment {
	t.Helper()
	root := t.TempDir()
	canonical, identity, err := workspaceattach.CaptureRootIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	return workspaceattach.Attachment{
		ID: "att_0123456789abcdef", SessionID: "ses_fixture",
		EnvironmentID: "env_fixture", WorkspaceID: workspaceID,
		Incarnation: lifecycle.EnvironmentRef{
			EnvironmentID: "env_fixture", StartGeneration: 1, InstanceName: "hideout-fixture",
			BootID: "01234567-89ab-cdef-0123-456789abcdef",
		},
		CanonicalHostRoot: canonical, RootFileIdentity: identity, RootHandleIdentity: "root-fixture",
		LogicalGuestRoot:  workspaceattach.LogicalWorkspaceRoot,
		PhysicalGuestRoot: workspaceattach.PhysicalWorkspaceBase + "/" + workspaceID,
		Transport:         workspaceattach.SelectedTransport,
		ProviderRef:       lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceHostProvider, ID: "provider-fixture", Generation: 1},
		GuestViewRef:      lifecycle.ResourceRef{Kind: lifecycle.KindWorkspaceGuestView, ID: "view-fixture", Generation: 1},
		State:             workspaceattach.AttachmentPlanned, CreatedAt: time.Now().UTC(),
	}
}
