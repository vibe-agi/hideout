package hostcap

import (
	"context"
	"testing"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

func TestOpenBoundResourceDerivesModeOnlyFromImmutableBindingAccess(t *testing.T) {
	builtin := profileIDEModeTestBinding(t, "builtin.vscode", "code-command", "vscode", "code", BindingAccessAskEachRun)
	externalSafe := profileIDEModeTestBinding(t, "community.editor", "editor-command", "editor", "editor", BindingAccessSafe)
	externalAsk := profileIDEModeTestBinding(t, "community.ask-editor", "ask-editor-command", "ask-editor", "ask-editor", BindingAccessAskEachRun)

	t.Run("built-in compatibility binding remains elevated", func(t *testing.T) {
		launcher := &fakeLauncher{}
		_, err := openProfileIDEModeTestBinding(t, builtin, fakeGrants{}, launcher)
		if CodeOf(err) != CodeModeTrustedDenied || len(launcher.ran) != 0 {
			t.Fatalf("built-in binding bypassed its run approval: err=%v launches=%v", err, launcher.ran)
		}
	})

	t.Run("external safe binding remains safe", func(t *testing.T) {
		launcher := &fakeLauncher{}
		result, err := openProfileIDEModeTestBinding(t, externalSafe, fakeGrants{}, launcher)
		if err != nil {
			t.Fatal(err)
		}
		if result.Mode != appopen.ModeSafe || len(launcher.ran) != 1 {
			t.Fatalf("profile IDE mode changed external safe access: result=%+v launches=%v", result, launcher.ran)
		}
	})

	t.Run("external ask-each-run does not require compatibility mode", func(t *testing.T) {
		launcher := &fakeLauncher{}
		_, err := openProfileIDEModeTestBinding(t, externalAsk, fakeGrants{}, launcher)
		if CodeOf(err) != CodeModeTrustedDenied || len(launcher.ran) != 0 {
			t.Fatalf("external binding launched without its run approval: err=%v launches=%v", err, launcher.ran)
		}

		result, err := openProfileIDEModeTestBinding(t, externalAsk, fakeGrants{active: true}, launcher)
		if err != nil {
			t.Fatal(err)
		}
		if result.Mode != appopen.ModeTrusted || len(launcher.ran) != 1 {
			t.Fatalf("external approval depended on profile IDE mode: result=%+v launches=%v", result, launcher.ran)
		}
	})
}

type boundRecordingDeduper struct {
	allow    bool
	reserved []string
	commits  []string
	releases []string
}

func (d *boundRecordingDeduper) Reserve(key string) bool {
	d.reserved = append(d.reserved, key)
	return d.allow
}
func (d *boundRecordingDeduper) Commit(key string)  { d.commits = append(d.commits, key) }
func (d *boundRecordingDeduper) Release(key string) { d.releases = append(d.releases, key) }

func TestOpenBoundResourceDedupRemainsTransactionalAfterLegacyPathRemoval(t *testing.T) {
	binding := profileIDEModeTestBinding(t, "community.editor", "editor-command", "editor", "editor", BindingAccessAskEachRun)
	resource := ResolvedResource{Ref: ResourceRef{Kind: KindWorkspace, GuestPath: "/workspace/main.go", RelativePath: "main.go"}, HostPath: "/host/workspace/main.go"}
	request := BoundOpenRequest{Resources: []UnboundResource{{GuestPath: resource.Ref.GuestPath}}}
	open := func(deduper Deduper, launcher *fakeLauncher, request BoundOpenRequest) (OpenResult, error) {
		return OpenBoundResource(context.Background(), binding, request, BoundOpenContext{
			SessionID: "session", Profile: "privacy", RunID: "run", Command: "editor",
			Resources: fakeBoundResolver{resource: resource}, Grants: fakeGrants{active: true}, Launcher: launcher, Deduper: deduper,
			RevalidateIdentity: func(_ ApplicationExpectation, previous ObservedApplicationIdentity) (ObservedApplicationIdentity, error) {
				return previous, nil
			},
		})
	}

	t.Run("suppressed duplicate has no host effect", func(t *testing.T) {
		deduper := &boundRecordingDeduper{allow: false}
		launcher := &fakeLauncher{}
		result, err := open(deduper, launcher, request)
		if err != nil || !result.Suppressed || len(launcher.ran) != 0 || len(deduper.reserved) != 1 || len(deduper.commits) != 0 || len(deduper.releases) != 0 {
			t.Fatalf("suppressed result=%+v err=%v launches=%v dedup=%+v", result, err, launcher.ran, deduper)
		}
	})

	t.Run("failed launch releases exactly its reservation", func(t *testing.T) {
		deduper := &boundRecordingDeduper{allow: true}
		launcher := &fakeLauncher{fail: true}
		_, err := open(deduper, launcher, request)
		if CodeOf(err) != CodeProviderUnavailable {
			t.Fatalf("launch failure=%v dedup=%+v", err, deduper)
		}
		if len(deduper.reserved) != 1 || len(deduper.commits) != 0 || len(deduper.releases) != 1 || deduper.releases[0] != deduper.reserved[0] {
			t.Fatalf("failed launch retained dedup authority: %+v", deduper)
		}
	})

	t.Run("location participates in dedup identity", func(t *testing.T) {
		deduper := &boundRecordingDeduper{allow: true}
		launcher := &fakeLauncher{}
		first := request
		first.Location = &Location{Line: 10, Column: 2}
		second := request
		second.Location = &Location{Line: 11, Column: 2}
		if _, err := open(deduper, launcher, first); err != nil {
			t.Fatal(err)
		}
		if _, err := open(deduper, launcher, second); err != nil {
			t.Fatal(err)
		}
		if len(deduper.commits) != 2 || deduper.commits[0] == deduper.commits[1] {
			t.Fatalf("locations did not produce distinct dedup identities: %+v", deduper)
		}
	})
}

func profileIDEModeTestBinding(t *testing.T, packID, bindingID, appID, command, access string) OpenResourceBinding {
	t.Helper()
	binding := baseTestBinding()
	binding.PackID = packID
	binding.BindingID = bindingID
	binding.QualifiedAppRef = packID + "/rev_0123456789abcdef/" + appID
	binding.Commands = []string{command}
	binding.Access = access
	binding.Application = ApplicationExpectation{
		QualifiedAppRef:        binding.QualifiedAppRef,
		BundleNames:            []string{"Visual Studio Code.app"},
		ExecutableRelativePath: "Contents/MacOS/Code",
	}
	binding.Launch = appopen.LaunchSpec{GotoFlag: "--goto", GotoSeparator: ":", ReuseWindowFlag: "--reuse-window"}
	if access == BindingAccessAskEachRun {
		binding.SafetyProfileID = ""
		binding.SafetyProfileVersion = ""
	}
	binding = finalizedTestBinding(t, binding)
	binding.ObservedIdentity.BundleID = "com.microsoft.VSCode"
	binding.ObservedIdentity.TeamID = "UBF8T346G9"
	binding.ObservedIdentity.CodeIdentity = "observed-code"
	binding.ObservedIdentity.Verification = AppVerificationVerified
	final, err := FinalizeBindingDigest(binding)
	if err != nil {
		t.Fatal(err)
	}
	return final
}

func openProfileIDEModeTestBinding(t *testing.T, binding OpenResourceBinding, grants GrantChecker, launcher *fakeLauncher) (OpenResult, error) {
	t.Helper()
	return OpenBoundResource(context.Background(), binding, BoundOpenRequest{
		Resources: []UnboundResource{{GuestPath: "/workspace/main.go"}},
	}, BoundOpenContext{
		SessionID: "session", Profile: "privacy", RunID: "run", Command: binding.Commands[0],
		SafeStateBase: t.TempDir(), Platform: PlatformDarwin,
		Resources: fakeBoundResolver{resource: ResolvedResource{
			Ref:      ResourceRef{Kind: KindWorkspace, GuestPath: "/workspace/main.go", RelativePath: "main.go"},
			HostPath: "/host/workspace/main.go",
		}},
		Grants: grants, Launcher: launcher,
		RevalidateIdentity: func(_ ApplicationExpectation, previous ObservedApplicationIdentity) (ObservedApplicationIdentity, error) {
			return previous, nil
		},
	})
}
