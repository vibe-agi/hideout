package hostapppack_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/cmdgrammar"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/hostapppack"
	"github.com/vibe-agi/hideout/internal/hostcap"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestBuiltinVSCodeAndExternalFixtureHaveByteEquivalentGenericShapes(t *testing.T) {
	root := t.TempDir()
	profileStore := profile.Store{Root: filepath.Join(root, "store")}
	if _, err := profileStore.LoadOrInit("privacy"); err != nil {
		t.Fatal(err)
	}
	core := manager.Core{Store: profileStore, HostAppPlatform: hostcap.PlatformDarwin}
	configureVSCodeIdentity(t, &core)

	fixture, err := filepath.Abs(filepath.Join("..", "..", "test", "host-app-packs", "gate2-external"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := core.PlanHostAppPack(manager.HostAppPackOptions{
		Operation:   "add",
		SourceKind:  hostapppack.SourceLocal,
		SourcePath:  fixture,
		ProfileName: "privacy",
	})
	if err != nil {
		t.Fatalf("plan external fixture: %v", err)
	}
	result, err := core.ApplyHostAppPack(plan)
	if err != nil {
		t.Fatalf("install and enable external fixture: %v", err)
	}
	if result.Enablement == nil || result.Enablement.State != hostapppack.EnablementEnabled {
		t.Fatalf("external fixture was not enabled: %+v", result.Enablement)
	}

	// These production entrypoints parse and normalize the embedded and
	// snapshotted manifests before constructing the shared public/runtime models.
	builtinInspection, err := core.InspectHostAppPack("builtin.vscode", "privacy")
	if err != nil {
		t.Fatalf("inspect built-in pack: %v", err)
	}
	externalInspection, err := core.InspectHostAppPack("test.external-vscode", "privacy")
	if err != nil {
		t.Fatalf("inspect external fixture: %v", err)
	}
	assertByteEquivalent(t, "inspection", genericInspectionShape(t, builtinInspection.Status), genericInspectionShape(t, externalInspection.Status))

	catalog, registrations, err := core.CompileHostAppCatalog("privacy", "run_t077", nil)
	if err != nil {
		t.Fatalf("compile shared catalog: %v", err)
	}
	builtinBinding, ok := catalog.ResolveCommand("code")
	if !ok {
		t.Fatal("compiled catalog has no built-in code binding")
	}
	externalBinding, ok := catalog.ResolveCommand("hcode")
	if !ok {
		t.Fatal("compiled catalog has no external hcode binding")
	}
	builtinRegistration := registrationForCommand(t, registrations, "code")
	externalRegistration := registrationForCommand(t, registrations, "hcode")
	assertByteEquivalent(t, "binding", genericBindingShape(t, builtinBinding, builtinRegistration), genericBindingShape(t, externalBinding, externalRegistration))
}

func configureVSCodeIdentity(t *testing.T, core *manager.Core) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	appFixtureRoot, err := os.MkdirTemp(home, ".hideout-t077-app-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(appFixtureRoot) })
	appsRoot := filepath.Join(appFixtureRoot, "Applications")
	executable := filepath.Join(appsRoot, "Visual Studio Code.app", "Contents", "MacOS", "Code")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("test-vscode"), 0o700); err != nil {
		t.Fatal(err)
	}
	baseOptions := hostcap.ApplicationIdentityOptions{
		Roots:       []hostcap.ApplicationRoot{{Class: hostcap.ApplicationRootOperator, Path: appsRoot}},
		OperatorUID: uint32(os.Getuid()),
		ObserveSigning: func(bundlePath string) (hostcap.SigningObservation, error) {
			if filepath.Base(bundlePath) != "Visual Studio Code.app" {
				return hostcap.SigningObservation{}, fmt.Errorf("unexpected application bundle %q", filepath.Base(bundlePath))
			}
			return hostcap.SigningObservation{
				Signed: true, Trusted: true, TrustAnchor: "test-platform-trust",
				BundleID: "com.microsoft.VSCode", TeamID: "UBF8T346G9",
				CodeIdentity: "Developer ID Application: Microsoft Corporation",
			}, nil
		},
	}
	core.HostAppIdentityResolver = func(expectation hostcap.ApplicationExpectation, forbiddenRoots []string) (hostcap.ObservedApplicationIdentity, error) {
		options := baseOptions
		options.ForbiddenRoots = append([]string(nil), forbiddenRoots...)
		return hostcap.ResolveApplicationIdentity(expectation, options)
	}
}

func genericInspectionShape(t *testing.T, inspection hostapppack.Inspection) []byte {
	t.Helper()
	if len(inspection.Entries) != 1 {
		t.Fatalf("inspection entries=%d, want one", len(inspection.Entries))
	}
	entry := inspection.Entries[0]
	entry.Summary.Command = "generic-command"
	entry.Package.ID = "generic.pack"
	entry.Package.RevisionID = "rev_generic"
	entry.Package.SourceKind = "generic-source"
	entry.Package.SourceDigest = "sha256:source"
	entry.Permissions.Fingerprint = "sha256:permission"
	entry.Binding.ID = "generic-binding"
	entry.Binding.Commands = []string{"generic-command"}
	inspection.GeneratedAt = time.Time{}
	inspection.Entries = []hostapppack.InspectionEntry{entry}
	return marshalShape(t, inspection)
}

func genericBindingShape(t *testing.T, binding hostcap.OpenResourceBinding, registration cmdproxy.Registration) []byte {
	t.Helper()
	if registration.OpenResourceGrammar == nil {
		t.Fatal("compiled registration has no open-resource grammar")
	}
	grammar := *registration.OpenResourceGrammar
	registration.OpenResourceGrammar = nil

	binding.PackID = "generic.pack"
	binding.RevisionID = "rev_generic"
	binding.BindingID = "generic-binding"
	binding.QualifiedAppRef = "generic.pack/rev_generic/vscode"
	binding.Commands = []string{"generic-command"}
	binding.Application.QualifiedAppRef = binding.QualifiedAppRef
	binding.SourceDigest = "sha256:source"
	binding.PermissionFingerprint = "sha256:permission"
	binding.ObservedIdentityDigest = "sha256:identity"
	binding.BindingDigest = "sha256:binding"
	binding.ObservedIdentity.QualifiedAppRef = binding.QualifiedAppRef
	binding.ObservedIdentity.BundlePath = "application-bundle"
	binding.ObservedIdentity.ExecutablePath = "application-executable"
	binding.ObservedIdentity.CanonicalPathDigest = "sha256:canonical-path"
	binding.ObservedIdentity.ObservedAt = time.Time{}
	registration.Name = "generic-command"
	registration.BindingDigest = binding.BindingDigest

	return marshalShape(t, struct {
		Binding      hostcap.OpenResourceBinding    `json:"binding"`
		Registration cmdproxy.Registration          `json:"registration"`
		Grammar      cmdgrammar.OpenResourceGrammar `json:"openResourceGrammar"`
	}{Binding: binding, Registration: registration, Grammar: grammar})
}

func registrationForCommand(t *testing.T, registrations []cmdproxy.Registration, command string) cmdproxy.Registration {
	t.Helper()
	for _, registration := range registrations {
		if registration.Name == command {
			return registration
		}
	}
	t.Fatalf("compiled registrations have no %q command", command)
	return cmdproxy.Registration{}
}

func marshalShape(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertByteEquivalent(t *testing.T, name string, builtin, external []byte) {
	t.Helper()
	if !bytes.Equal(builtin, external) {
		t.Fatalf("%s shapes differ\nbuilt-in: %s\nexternal: %s", name, builtin, external)
	}
}
