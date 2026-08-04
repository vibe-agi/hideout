package lima

import (
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/workspaceattach"
)

func TestPortalWorkspaceViewRemovesStagingAndControlBeforeTarget(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	siblingID := "wrk_" + strings.Repeat("b", 64)
	command, err := BuildSessionViewCommand(SessionViewSpec{
		SessionID: "ses_20260718T120000Z_0123456789abcdef", TargetUser: "developer",
		GuestWork: workspaceattach.LogicalWorkspaceRoot, Env: []string{"PATH=/hideout/session/shims:/usr/bin:/bin"},
		Command: []string{"git", "status"},
		Workspace: backend.WorkspaceAttachmentSpec{
			HostRoot: "/Users/operator/private/project", GuestRoot: workspaceattach.LogicalWorkspaceRoot,
			Transport: backend.WorkspaceTransportPortal,
			Portal: &backend.WorkspacePortalBinding{
				PhysicalGuestRoot: workspaceattach.PhysicalWorkspaceBase + "/" + workspaceID,
				Endpoint:          "host.lima.internal:43127", CredentialGuestPath: workspaceattach.PortalCredentialGuestPath,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(command) < 10 {
		t.Fatalf("session command shape=%q", command)
	}
	script := command[9]
	for _, required := range []string{
		"mount -t tmpfs -o mode=0755,size=16m hideout-workspace-rootfs \"$workspace_root\"",
		"rm -f '/hideout/session/workspace/credential.bin'",
		"umount '/hideout/workspaces/" + workspaceID + "'",
		"umount /hideout/workspaces",
		"kill \"$workspace_portal_pid\" 2>/dev/null || true",
		"kill -KILL \"$workspace_portal_pid\" 2>/dev/null || true",
		"'chroot' '/hideout/runtime/workspace-rootfs' 'unshare' '--mount' '--pid' '--fork' '--kill-child=KILL' '--mount-proc=/proc'",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("session view omitted isolation step %q:\n%s", required, script)
		}
	}
	for _, forbidden := range []string{"/Users/operator", siblingID, "mount --rbind /Users", "mount --bind /Users"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("session view exposed broad/sibling authority %q:\n%s", forbidden, script)
		}
	}
	logicalLink := "ln -s '/hideout/workspaces/" + workspaceID + "' \"$workspace_root/workspace\""
	if !strings.Contains(script, logicalLink) {
		t.Fatal("logical /workspace did not retain the selected opaque project identity")
	}
	if strings.Contains(script, "mount --rbind '/hideout/workspaces/"+workspaceID+"' \"$workspace_root/workspace\"") {
		t.Fatal("fixed /workspace bind mount collapsed the project identity")
	}
	if strings.Count(script, workspaceattach.PhysicalWorkspaceBase+"/"+workspaceID) < 3 {
		t.Fatal("selected opaque child was not bound as the sole workspace view")
	}
}

func TestPortalWorkspaceBindingRejectsGuessedSiblingAndControlOverrides(t *testing.T) {
	workspaceID := "wrk_" + strings.Repeat("a", 64)
	base := SessionViewSpec{
		SessionID: "ses_20260718T120000Z_0123456789abcdef", TargetUser: "developer",
		GuestWork: workspaceattach.LogicalWorkspaceRoot, Command: []string{"true"},
		Workspace: backend.WorkspaceAttachmentSpec{
			HostRoot: t.TempDir(), GuestRoot: workspaceattach.LogicalWorkspaceRoot,
			Transport: backend.WorkspaceTransportPortal,
			Portal: &backend.WorkspacePortalBinding{
				PhysicalGuestRoot: workspaceattach.PhysicalWorkspaceBase + "/" + workspaceID,
				Endpoint:          "host.lima.internal:43127", CredentialGuestPath: workspaceattach.PortalCredentialGuestPath,
			},
		},
	}
	tests := []struct {
		name   string
		mutate func(*SessionViewSpec)
	}{
		{name: "sibling physical root", mutate: func(spec *SessionViewSpec) {
			spec.Workspace.Portal.PhysicalGuestRoot = workspaceattach.PhysicalWorkspaceBase + "/../wrk_sibling"
		}},
		{name: "credential override", mutate: func(spec *SessionViewSpec) { spec.Workspace.Portal.CredentialGuestPath = "/tmp/guest-controlled" }},
		{name: "logical root override", mutate: func(spec *SessionViewSpec) { spec.Workspace.GuestRoot = "/hideout/workspaces" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			portal := *base.Workspace.Portal
			candidate.Workspace.Portal = &portal
			test.mutate(&candidate)
			if _, err := BuildSessionViewCommand(candidate); err == nil {
				t.Fatal("guest-controlled workspace binding was accepted")
			}
		})
	}
}
