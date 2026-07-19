package hostcap

import (
	"context"
	"testing"
)

func TestProjectionResolvesSafeApplicationIdentityOnlyOnFirstCommandUse(t *testing.T) {
	observed := profileIDEModeTestBinding(t, "builtin.vscode", "code-command", "vscode", "code", BindingAccessSafe)
	static := cloneOpenResourceBinding(observed)
	static.IdentityDeferred = true
	static.ObservedIdentityDigest = ""
	static.ObservedIdentity = ObservedApplicationIdentity{}
	static, err := FinalizeBindingDigest(static)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := NewBindingCatalog([]OpenResourceBinding{static})
	if err != nil {
		t.Fatal(err)
	}
	resolved := cloneOpenResourceBinding(static)
	resolved.ObservedIdentity = observed.ObservedIdentity
	resolved.ObservedIdentityDigest = observed.ObservedIdentityDigest
	resolved, err = FinalizeBindingDigest(resolved)
	if err != nil {
		t.Fatal(err)
	}

	identityChecks := 0
	begun := false
	var completions []bool
	launcher := &fakeLauncher{beforeGuard: func() {
		if !begun {
			t.Fatal("host launcher ran before lifecycle registration")
		}
	}}
	projection := &ProjectionConfig{
		Platform: PlatformDarwin, SafeUserDataDir: t.TempDir(), Bindings: catalog, Launcher: launcher, RunID: "run_1", WorkspaceID: testWorkspaceAuthorityID,
		ValidateLifecycle: func(OpenResourceBinding) error { return nil },
		ResolveIdentity: func(binding OpenResourceBinding) (OpenResourceBinding, error) {
			identityChecks++
			if binding.ObservedIdentityDigest != "" {
				t.Fatal("static binding was already hydrated before command use")
			}
			return resolved, nil
		},
		RevalidateIdentity: func(_ ApplicationExpectation, previous ObservedApplicationIdentity) (ObservedApplicationIdentity, error) {
			return previous, nil
		},
		BeginHandoff: func(command string) (func(bool) error, error) {
			if command != "code" {
				t.Fatalf("handoff command=%q", command)
			}
			begun = true
			return func(launched bool) error {
				completions = append(completions, launched)
				begun = false
				return nil
			}, nil
		},
	}
	if identityChecks != 0 {
		t.Fatal("constructing a projection observed a host application")
	}
	request := BoundOpenRequest{Resources: []UnboundResource{{GuestPath: "/workspace/main.go"}}}
	resource := fakeBoundResolver{resource: ResolvedResource{
		Ref: ResourceRef{Kind: KindWorkspace, GuestPath: "/workspace/main.go", RelativePath: "main.go"}, HostPath: "/host/workspace/main.go", AuthorityID: testWorkspaceAuthorityID,
	}}
	for i := 0; i < 2; i++ {
		if _, _, err := projection.OpenCommand(context.Background(), "code", static.BindingDigest, request, resource, "session_1", "privacy"); err != nil {
			t.Fatal(err)
		}
	}
	if identityChecks != 1 || len(launcher.ran) != 2 || len(completions) != 2 || !completions[0] || !completions[1] {
		t.Fatalf("identity checks=%d launches=%d completions=%v, want one lazy resolution and two guarded launches", identityChecks, len(launcher.ran), completions)
	}
}

func TestProjectionRejectsLazyResolverThatChangesImmutableBinding(t *testing.T) {
	observed := profileIDEModeTestBinding(t, "builtin.vscode", "code-command", "vscode", "code", BindingAccessSafe)
	static := cloneOpenResourceBinding(observed)
	static.IdentityDeferred = true
	static.ObservedIdentityDigest = ""
	static.ObservedIdentity = ObservedApplicationIdentity{}
	static, _ = FinalizeBindingDigest(static)
	catalog, err := NewBindingCatalog([]OpenResourceBinding{static})
	if err != nil {
		t.Fatal(err)
	}
	launcher := &fakeLauncher{}
	projection := &ProjectionConfig{
		Bindings: catalog, Launcher: launcher, WorkspaceID: testWorkspaceAuthorityID,
		ValidateLifecycle: func(OpenResourceBinding) error { return nil },
		ResolveIdentity: func(binding OpenResourceBinding) (OpenResourceBinding, error) {
			binding.Commands = []string{"other"}
			binding.ObservedIdentity = observed.ObservedIdentity
			binding.ObservedIdentityDigest = observed.ObservedIdentityDigest
			return binding, nil
		},
	}
	request := BoundOpenRequest{Resources: []UnboundResource{{GuestPath: "/workspace/main.go"}}}
	resource := fakeBoundResolver{resource: ResolvedResource{Ref: ResourceRef{Kind: KindWorkspace}, HostPath: "/host/workspace/main.go", AuthorityID: testWorkspaceAuthorityID}}
	_, _, err = projection.OpenCommand(context.Background(), "code", static.BindingDigest, request, resource, "session_1", "privacy")
	if CodeOf(err) != CodeAppIdentityDrift || len(launcher.ran) != 0 {
		t.Fatalf("mutated lazy binding reached launch: err=%v launches=%v", err, launcher.ran)
	}
}
