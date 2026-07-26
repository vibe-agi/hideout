package hostcap

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

const testWorkspaceAuthorityID = "wrk_test_session_authority"

func shortHostAppStateBase(t *testing.T) string {
	t.Helper()
	path, err := os.MkdirTemp("/tmp", "hideout-hostapp-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	return path
}

type fakeBoundResolver struct {
	resource      ResolvedResource
	err           error
	revalidateErr error
}

func (f fakeBoundResolver) ResolveResource(string) (ResolvedResource, error) {
	return f.resource, f.err
}
func (f fakeBoundResolver) RevalidateResource(ResolvedResource) error { return f.revalidateErr }

type raceBoundResolver struct {
	resource    ResolvedResource
	revalidates int
	failFinal   bool
}

func (r *raceBoundResolver) ResolveResource(string) (ResolvedResource, error) { return r.resource, nil }
func (r *raceBoundResolver) RevalidateResource(ResolvedResource) error {
	r.revalidates++
	if r.failFinal && r.revalidates >= 2 {
		return errors.New("resource retargeted at launch boundary")
	}
	return nil
}

type fakeGrants struct{ active bool }

func (f fakeGrants) TrustedGrantActive(GrantScope) bool                         { return f.active }
func (f fakeGrants) TrustedGrantActiveForResource(GrantScope, ResourceRef) bool { return f.active }

type legacyOnlyGrants bool

func (g legacyOnlyGrants) TrustedGrantActive(GrantScope) bool { return bool(g) }

type workspaceResourceGrants struct{}

func (workspaceResourceGrants) TrustedGrantActive(GrantScope) bool { return true }
func (workspaceResourceGrants) TrustedGrantActiveForResource(_ GrantScope, resource ResourceRef) bool {
	return resource.Kind == KindWorkspace
}

type fakeLauncher struct {
	ran         [][]string
	fail        bool
	beforeGuard func()
}

func (f *fakeLauncher) Run(_ context.Context, argv []string, guard appopen.LaunchGuard) error {
	if f.beforeGuard != nil {
		f.beforeGuard()
	}
	if guard != nil {
		if err := guard(); err != nil {
			return err
		}
	}
	if f.fail {
		return errors.New("launch failed")
	}
	f.ran = append(f.ran, argv)
	return nil
}

func TestTrustedGrantActiveForResourceFailsClosedAndBindsDerivedClass(t *testing.T) {
	scope := GrantScope{SessionID: "session", Profile: "privacy", BindingID: "binding"}
	workspace := ResourceRef{Kind: KindWorkspace, GuestPath: "/workspace/a.go"}
	hostFS := ResourceRef{Kind: KindHostFS, GuestPath: "/hideout/hostfs/Users/example/a.go"}
	if TrustedGrantActiveForResource(legacyOnlyGrants(true), scope, workspace) {
		t.Fatal("legacy scope-only approval was treated as resource authority")
	}
	checker := workspaceResourceGrants{}
	if !TrustedGrantActiveForResource(checker, scope, workspace) || TrustedGrantActiveForResource(checker, scope, hostFS) {
		t.Fatal("workspace approval crossed the Core-derived HostFS resource class")
	}
}

func TestOpenBoundResourceUsesOneGenericPathForTwoRecipes(t *testing.T) {
	for _, recipe := range []struct {
		pack, binding, app, command string
	}{
		{"builtin.vscode", "code-command", "vscode", "code"},
		{"community.editor", "editor-command", "editor", "editor"},
	} {
		t.Run(recipe.command, func(t *testing.T) {
			qualified := recipe.pack + "/rev_0123456789abcdef/" + recipe.app
			identityDigest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
			identity := ObservedApplicationIdentity{
				QualifiedAppRef: qualified, ExecutablePath: "/Applications/Editor.app/Contents/MacOS/Editor",
				ExecutableRelativePath: "Contents/MacOS/Code", ExecutableCodeIdentity: "sha256:test-editor-executable",
				BundleID: "com.microsoft.VSCode", TeamID: "UBF8T346G9", CodeIdentity: "observed-code",
				Verification: AppVerificationVerified, identityDigest: identityDigest,
			}
			binding := OpenResourceBinding{
				PackID: recipe.pack, RevisionID: "rev_0123456789abcdef", BindingID: recipe.binding,
				QualifiedAppRef: qualified, Commands: []string{recipe.command}, CapabilityID: CapabilityAppOpenResource,
				ResourceKinds: []ResourceKind{KindWorkspace}, ResultPolicy: ResultNone, Access: BindingAccessSafe,
				Grammar:     BindingGrammar{Kind: BindingGrammarV1, ResourceCount: 1, GotoFlags: []string{"--goto"}, ReuseWindowFlags: []string{"--reuse-window"}, UnknownFlags: "deny"},
				Application: ApplicationExpectation{QualifiedAppRef: qualified, BundleNames: []string{"Editor.app"}, ExecutableRelativePath: "Contents/MacOS/Code"},
				Launch:      appopen.LaunchSpec{GotoFlag: "--goto", GotoSeparator: ":", ReuseWindowFlag: "--reuse-window"}, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "1",
				SourceDigest:           "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				PermissionFingerprint:  "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
				ObservedIdentityDigest: identity.IdentityDigest(), ObservedIdentity: identity,
			}
			binding, err := FinalizeBindingDigest(binding)
			if err != nil {
				t.Fatal(err)
			}
			launcher := &fakeLauncher{}
			base := shortHostAppStateBase(t)
			result, err := OpenBoundResource(context.Background(), binding, BoundOpenRequest{
				Resources: []UnboundResource{{GuestPath: "/workspace/src/main.go"}},
				Location:  &Location{Line: 12, Column: 3}, WindowMode: WindowReuse,
			}, BoundOpenContext{
				SessionID: "sess", Profile: "privacy", RunID: "run", WorkspaceID: testWorkspaceAuthorityID, SafeStateBase: base,
				Platform:  PlatformDarwin,
				Resources: fakeBoundResolver{resource: ResolvedResource{Ref: ResourceRef{Kind: KindWorkspace, GuestPath: "/workspace/src/main.go", RelativePath: "src/main.go"}, HostPath: "/host/project/src/main.go", AuthorityID: testWorkspaceAuthorityID}},
				Launcher:  launcher, RevalidateIdentity: func(ApplicationExpectation, ObservedApplicationIdentity) (ObservedApplicationIdentity, error) {
					return identity, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Outcome != outcomeLaunched || len(launcher.ran) != 1 || !slices.Contains(launcher.ran[0], "--goto") || strings.Join(launcher.ran[0], " ") == "" {
				t.Fatalf("result=%+v argv=%v", result, launcher.ran)
			}
		})
	}
}

func TestBindingCatalogRemovalIsIndependentAcrossGenericRecipes(t *testing.T) {
	first := finalizedTestBinding(t, baseTestBinding())
	second := cloneOpenResourceBinding(first)
	second.PackID, second.BindingID = "community.second", "second-command"
	second.QualifiedAppRef = "community.second/rev_0123456789abcdef/editor"
	second.Application.QualifiedAppRef = second.QualifiedAppRef
	second.ObservedIdentity.QualifiedAppRef = second.QualifiedAppRef
	second.Commands = []string{"second-editor"}
	second, err := FinalizeBindingDigest(second)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewBindingCatalog([]OpenResourceBinding{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.ResolveCommand("editor"); !ok {
		t.Fatal("first generic recipe is absent")
	}
	if _, ok := catalog.ResolveCommand("second-editor"); !ok {
		t.Fatal("second generic recipe is absent")
	}
	catalog, err = NewBindingCatalog([]OpenResourceBinding{second})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.ResolveCommand("editor"); ok {
		t.Fatal("removed recipe retained command authority")
	}
	if got, ok := catalog.ResolveCommand("second-editor"); !ok || got.PackID != "community.second" {
		t.Fatalf("independent recipe was affected by removal: %+v ok=%v", got, ok)
	}
}

func TestOpenBoundResourceRejectsDerivedKindOutsideBinding(t *testing.T) {
	qualified := "community.editor/rev_0123456789abcdef/editor"
	identityDigest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	identity := ObservedApplicationIdentity{
		QualifiedAppRef: qualified, ExecutablePath: "/Applications/Editor.app/Contents/MacOS/Editor",
		ExecutableRelativePath: "Contents/MacOS/Editor", ExecutableCodeIdentity: "sha256:test-editor-executable", identityDigest: identityDigest,
	}
	binding := OpenResourceBinding{
		PackID: "community.editor", RevisionID: "rev_0123456789abcdef", BindingID: "editor", QualifiedAppRef: qualified,
		Commands: []string{"editor"}, CapabilityID: CapabilityAppOpenResource, ResourceKinds: []ResourceKind{KindWorkspace}, ResultPolicy: ResultNone,
		Access: BindingAccessSafe, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "1",
		Grammar:               BindingGrammar{Kind: BindingGrammarV1, ResourceCount: 1, UnknownFlags: "deny"},
		Application:           ApplicationExpectation{QualifiedAppRef: qualified, BundleNames: []string{"Editor.app"}, ExecutableRelativePath: "Contents/MacOS/Editor"},
		SourceDigest:          "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		PermissionFingerprint: "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789", ObservedIdentityDigest: identityDigest, ObservedIdentity: identity,
	}
	binding, err := FinalizeBindingDigest(binding)
	if err != nil {
		t.Fatal(err)
	}
	_, err = OpenBoundResource(context.Background(), binding, BoundOpenRequest{Resources: []UnboundResource{{GuestPath: "/hideout/hostfs/Users/a.txt"}}}, BoundOpenContext{
		Resources: fakeBoundResolver{resource: ResolvedResource{Ref: ResourceRef{Kind: KindHostFS, GuestPath: "/hideout/hostfs/Users/a.txt"}, HostPath: "/Users/a.txt"}}, Launcher: &fakeLauncher{},
	})
	if CodeOf(err) != CodePathNoHostMapping {
		t.Fatalf("error=%v code=%q", err, CodeOf(err))
	}
}

func TestOpenBoundResourceRejectsResourceRetargetAtLauncherGuard(t *testing.T) {
	binding := finalizedTestBinding(t, baseTestBinding())
	binding.Access = BindingAccessAskEachRun
	binding.SafetyProfileID, binding.SafetyProfileVersion = "", ""
	binding, _ = FinalizeBindingDigest(binding)
	resolver := &raceBoundResolver{resource: ResolvedResource{
		Ref:         ResourceRef{Kind: KindWorkspace, GuestPath: "/workspace/a.go", RelativePath: "a.go"},
		HostPath:    "/host/workspace/a.go",
		AuthorityID: testWorkspaceAuthorityID,
	}}
	launcher := &fakeLauncher{beforeGuard: func() { resolver.failFinal = true }}
	_, err := OpenBoundResource(context.Background(), binding, BoundOpenRequest{Resources: []UnboundResource{{GuestPath: "/workspace/a.go"}}}, BoundOpenContext{
		SessionID: "session", Profile: "privacy", RunID: "run", WorkspaceID: testWorkspaceAuthorityID, Command: "editor",
		Resources: resolver, Grants: fakeGrants{active: true}, Launcher: launcher,
		RevalidateIdentity: func(_ ApplicationExpectation, prior ObservedApplicationIdentity) (ObservedApplicationIdentity, error) {
			return prior, nil
		},
	})
	if CodeOf(err) != CodePathNoHostMapping || len(launcher.ran) != 0 || resolver.revalidates != 2 {
		t.Fatalf("resource retarget race was not stopped at launcher boundary: err=%v launches=%v revalidates=%d", err, launcher.ran, resolver.revalidates)
	}
}

func TestOpenBoundHostFSResourceRechecksAuthorityAfterApprovedDecision(t *testing.T) {
	binding := finalizedTestBinding(t, baseTestBinding())
	binding.ResourceKinds = []ResourceKind{KindWorkspace, KindHostFS}
	binding.Access = BindingAccessAskEachRun
	binding.SafetyProfileID, binding.SafetyProfileVersion = "", ""
	binding, _ = FinalizeBindingDigest(binding)
	resolver := &raceBoundResolver{resource: ResolvedResource{
		Ref:      ResourceRef{Kind: KindHostFS, GuestPath: "/hideout/hostfs/Users/example/report.txt", RelativePath: "report.txt"},
		HostPath: "/Users/example/report.txt",
	}}
	launcher := &fakeLauncher{beforeGuard: func() { resolver.failFinal = true }}
	_, err := OpenBoundResource(context.Background(), binding, BoundOpenRequest{Resources: []UnboundResource{{GuestPath: resolver.resource.Ref.GuestPath}}}, BoundOpenContext{
		SessionID: "session", Profile: "privacy", RunID: "run", Command: "editor",
		Resources: resolver, Grants: fakeGrants{active: true}, Launcher: launcher,
		RevalidateIdentity: func(_ ApplicationExpectation, prior ObservedApplicationIdentity) (ObservedApplicationIdentity, error) {
			return prior, nil
		},
	})
	if CodeOf(err) != CodePathNoHostMapping || len(launcher.ran) != 0 || resolver.revalidates != 2 {
		t.Fatalf("approved decision fixed stale HostFS authority: err=%v launches=%v revalidates=%d", err, launcher.ran, resolver.revalidates)
	}
}

func TestOpenBoundHostFSResourceRejectsWorkspaceOnlyGrant(t *testing.T) {
	binding := finalizedTestBinding(t, baseTestBinding())
	binding.ResourceKinds = []ResourceKind{KindWorkspace, KindHostFS}
	binding.Access = BindingAccessAskEachRun
	binding.SafetyProfileID, binding.SafetyProfileVersion = "", ""
	binding, _ = FinalizeBindingDigest(binding)
	launcher := &fakeLauncher{}
	_, err := OpenBoundResource(context.Background(), binding, BoundOpenRequest{Resources: []UnboundResource{{GuestPath: "/hideout/hostfs/Users/example/report.txt"}}}, BoundOpenContext{
		SessionID: "session", Profile: "privacy", RunID: "run", Command: "editor",
		Resources: fakeBoundResolver{resource: ResolvedResource{
			Ref:      ResourceRef{Kind: KindHostFS, GuestPath: "/hideout/hostfs/Users/example/report.txt", RelativePath: "report.txt"},
			HostPath: "/Users/example/report.txt",
		}},
		Grants: workspaceResourceGrants{}, Launcher: launcher,
		RevalidateIdentity: func(_ ApplicationExpectation, prior ObservedApplicationIdentity) (ObservedApplicationIdentity, error) {
			return prior, nil
		},
	})
	if CodeOf(err) != CodeModeTrustedDenied || len(launcher.ran) != 0 {
		t.Fatalf("workspace-only grant authorized HostFS resource: err=%v launches=%v", err, launcher.ran)
	}
}

func TestOpenBoundResourceRejectsAppReplacementAtLauncherGuard(t *testing.T) {
	binding := finalizedTestBinding(t, baseTestBinding())
	binding.Access = BindingAccessAskEachRun
	binding.SafetyProfileID, binding.SafetyProfileVersion = "", ""
	binding, _ = FinalizeBindingDigest(binding)
	replaced := false
	checks := 0
	launcher := &fakeLauncher{beforeGuard: func() { replaced = true }}
	_, err := OpenBoundResource(context.Background(), binding, BoundOpenRequest{Resources: []UnboundResource{{GuestPath: "/workspace/a.go"}}}, BoundOpenContext{
		SessionID: "session", Profile: "privacy", RunID: "run", WorkspaceID: testWorkspaceAuthorityID, Command: "editor",
		Resources: fakeBoundResolver{resource: ResolvedResource{Ref: ResourceRef{Kind: KindWorkspace, GuestPath: "/workspace/a.go"}, HostPath: "/host/workspace/a.go", AuthorityID: testWorkspaceAuthorityID}},
		Grants:    fakeGrants{active: true}, Launcher: launcher,
		RevalidateIdentity: func(_ ApplicationExpectation, prior ObservedApplicationIdentity) (ObservedApplicationIdentity, error) {
			checks++
			if replaced {
				prior.identityDigest = "sha256:changed-at-launch-boundary"
			}
			return prior, nil
		},
	})
	if CodeOf(err) != CodeAppIdentityDrift || len(launcher.ran) != 0 || checks != 1 {
		t.Fatalf("app replacement race was not stopped at launcher boundary: err=%v launches=%v checks=%d", err, launcher.ran, checks)
	}
}
