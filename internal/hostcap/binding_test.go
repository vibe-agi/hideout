package hostcap

import (
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/hostcap/appopen"
)

func TestBindingCatalogDerivesApplicationFromCommand(t *testing.T) {
	binding := finalizedTestBinding(t, baseTestBinding())
	binding.Commands = []string{"edit", "editor"}
	binding, _ = FinalizeBindingDigest(binding)
	catalog, err := NewBindingCatalog([]OpenResourceBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := catalog.ResolveCommand("editor")
	if !ok || got.QualifiedAppRef != binding.QualifiedAppRef || !reflect.DeepEqual(got.Commands, binding.Commands) {
		t.Fatalf("resolved binding=%+v ok=%v", got, ok)
	}
	got.Commands[0] = "mutated"
	again, _ := catalog.ResolveCommand("edit")
	if again.Commands[0] != "edit" {
		t.Fatal("binding catalog returned mutable authority")
	}
}

func TestBindingCatalogRejectsAmbiguousOrAuthorityWideningBindings(t *testing.T) {
	valid := finalizedTestBinding(t, baseTestBinding())
	for name, mutate := range map[string]func(*OpenResourceBinding){
		"host path command":  func(b *OpenResourceBinding) { b.Commands = []string{"/tmp/editor"} },
		"unknown capability": func(b *OpenResourceBinding) { b.CapabilityID = "host.exec" },
		"result channel":     func(b *OpenResourceBinding) { b.ResultPolicy = ResultStream },
		"unknown resource":   func(b *OpenResourceBinding) { b.ResourceKinds = []ResourceKind{KindDevice} },
		"duplicate resource": func(b *OpenResourceBinding) { b.ResourceKinds = []ResourceKind{KindWorkspace, KindWorkspace} },
		"mismatched app":     func(b *OpenResourceBinding) { b.Application.QualifiedAppRef = "other/app" },
		"missing grammar":    func(b *OpenResourceBinding) { b.Grammar = BindingGrammar{} },
		"stale digest":       func(b *OpenResourceBinding) { b.Commands = []string{"changed"} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := cloneOpenResourceBinding(valid)
			mutate(&candidate)
			if _, err := NewBindingCatalog([]OpenResourceBinding{candidate}); err == nil {
				t.Fatal("expected fail-closed validation")
			}
		})
	}
	other := cloneOpenResourceBinding(valid)
	other.PackID, other.BindingID = "other.pack", "other"
	other.QualifiedAppRef, other.Application.QualifiedAppRef = "other.pack/rev_0123456789abcdef/editor", "other.pack/rev_0123456789abcdef/editor"
	other.ObservedIdentity.QualifiedAppRef = other.QualifiedAppRef
	other, _ = FinalizeBindingDigest(other)
	if _, err := NewBindingCatalog([]OpenResourceBinding{valid, other}); err == nil {
		t.Fatal("expected duplicate command owner rejection")
	}
}

func TestBindingCatalogUsesExactCommandAndAccessSafetyContracts(t *testing.T) {
	binding := finalizedTestBinding(t, baseTestBinding())
	catalog, err := NewBindingCatalog([]OpenResourceBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.ResolveCommand("/tmp/editor"); ok {
		t.Fatal("path-like attacker input resolved through basename")
	}
	for _, malformed := range []string{".", "a b", "a\nb", "a/b", strings.Repeat("x", 65)} {
		candidate := cloneOpenResourceBinding(binding)
		candidate.Commands = []string{malformed}
		candidate, _ = FinalizeBindingDigest(candidate)
		if _, err := NewBindingCatalog([]OpenResourceBinding{candidate}); err == nil {
			t.Fatalf("malformed command %q was accepted", malformed)
		}
		if _, ok := catalog.ResolveCommand(malformed); ok {
			t.Fatalf("malformed command %q resolved", malformed)
		}
	}
	ask := cloneOpenResourceBinding(binding)
	ask.Access = BindingAccessAskEachRun
	ask.SafetyProfileID = ""
	ask.SafetyProfileVersion = ""
	ask, _ = FinalizeBindingDigest(ask)
	if _, err := NewBindingCatalog([]OpenResourceBinding{ask}); err != nil {
		t.Fatalf("ask-each-run without safe profile: %v", err)
	}
	ask.SafetyProfileID = "vscode-family-v1"
	ask, _ = FinalizeBindingDigest(ask)
	if _, err := NewBindingCatalog([]OpenResourceBinding{ask}); err == nil {
		t.Fatal("ask-each-run claimed a safe profile")
	}
}

func baseTestBinding() OpenResourceBinding {
	return OpenResourceBinding{
		PackID: "community.editor", RevisionID: "rev_0123456789abcdef", BindingID: "edit",
		QualifiedAppRef: "community.editor/rev_0123456789abcdef/editor", Commands: []string{"editor"},
		CapabilityID: CapabilityAppOpenResource, ResourceKinds: []ResourceKind{KindWorkspace}, ResultPolicy: ResultNone,
		Access: BindingAccessSafe, SafetyProfileID: "vscode-family-v1", SafetyProfileVersion: "1",
		Grammar:               BindingGrammar{Kind: BindingGrammarV1, ResourceCount: 1, UnknownFlags: "deny"},
		Application:           ApplicationExpectation{QualifiedAppRef: "community.editor/rev_0123456789abcdef/editor", BundleNames: []string{"Editor.app"}, ExecutableRelativePath: "Contents/MacOS/Editor"},
		Launch:                appopen.LaunchSpec{GotoFlag: "--goto", GotoSeparator: ":"},
		SourceDigest:          "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		PermissionFingerprint: "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
	}
}

func finalizedTestBinding(t *testing.T, binding OpenResourceBinding) OpenResourceBinding {
	t.Helper()
	identityDigest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	binding.ObservedIdentityDigest = identityDigest
	binding.ObservedIdentity = ObservedApplicationIdentity{
		QualifiedAppRef: binding.QualifiedAppRef, ExecutablePath: "/Applications/Editor.app/Contents/MacOS/Editor",
		ExecutableRelativePath: binding.Application.ExecutableRelativePath, ExecutableCodeIdentity: "sha256:test-editor-executable",
		identityDigest: identityDigest,
	}
	final, err := FinalizeBindingDigest(binding)
	if err != nil {
		t.Fatal(err)
	}
	return final
}
