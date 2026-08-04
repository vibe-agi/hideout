package lima

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
	"gopkg.in/yaml.v3"
)

func TestMigrationImportedRuntimeReconcilesOnlyDestinationMountsBeforeFirstStart(t *testing.T) {
	fixture := newImportedRuntimeFixture(t)
	runner := &importedRuntimeRunner{
		instance: fixture.session.InstanceName,
		config:   fixture.instanceConfig,
	}
	b := Backend{Runner: runner, Stdout: io.Discard, Stderr: io.Discard}

	if _, _, err := b.startAndObserveRuntime(
		context.Background(), fixture.session, []string{"PATH=/usr/bin:/bin"},
	); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 3 ||
		!reflect.DeepEqual(runner.calls[0].args, []string{"list", "--quiet"}) ||
		len(runner.calls[1].args) != 5 || runner.calls[1].args[0] != "edit" ||
		!reflect.DeepEqual(runner.calls[2].args, []string{"start", "--tty=false", fixture.session.InstanceName}) {
		t.Fatalf("imported first-start calls=%+v", runner.calls)
	}
	setExpression := runner.calls[1].args[3]
	if !strings.HasPrefix(setExpression, ".mounts = [") ||
		strings.Contains(setExpression, "additionalDisks") ||
		strings.Contains(setExpression, "images") ||
		strings.Contains(setExpression, "/workspace") {
		t.Fatalf("runtime reconciliation widened its edit: %q", setExpression)
	}
	updated, err := readImportedLimaRuntimeConfig(fixture.instanceConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Images) != 1 || updated.Images[0].Location != migrationImportedRootImageSentinel ||
		!reflect.DeepEqual(updated.AdditionalDisks, []string{"disk_imported_data1"}) ||
		len(updated.Mounts) != 7 {
		t.Fatalf("reconciled imported config=%+v", updated)
	}

	callCount := len(runner.calls)
	if err := b.reconcileImportedRuntimeMounts(
		context.Background(), runner, HostCommandEnv(os.Environ()), fixture.session,
	); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != callCount {
		t.Fatalf("idempotent mount reconciliation repeated an edit: %+v", runner.calls[callCount:])
	}
}

func TestMigrationImportedRuntimeMarkerAndRootFailClosedBeforeStart(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, importedRuntimeFixture)
		want   string
	}{
		{
			name: "runnable marker",
			mutate: func(t *testing.T, fixture importedRuntimeFixture) {
				marker := fixture.marker
				marker.Runnable = true
				writeImportedRuntimeJSON(t, fixture.normalizedPath, marker)
			},
			want: "grants pre-run authority",
		},
		{
			name: "missing root",
			mutate: func(t *testing.T, fixture importedRuntimeFixture) {
				if err := os.Remove(fixture.rootDisk); err != nil {
					t.Fatal(err)
				}
			},
			want: "protect imported Lima root disk",
		},
		{
			name: "substituted root size",
			mutate: func(t *testing.T, fixture importedRuntimeFixture) {
				if err := os.WriteFile(fixture.rootDisk, []byte("substituted-imported-root"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "protect imported Lima root disk",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newImportedRuntimeFixture(t)
			test.mutate(t, fixture)
			runner := &importedRuntimeRunner{instance: fixture.session.InstanceName, config: fixture.instanceConfig}
			b := Backend{Runner: runner, Stdout: io.Discard, Stderr: io.Discard}
			_, _, err := b.startAndObserveRuntime(context.Background(), fixture.session, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want %q", err, test.want)
			}
			if len(runner.calls) != 1 || !reflect.DeepEqual(runner.calls[0].args, []string{"list", "--quiet"}) {
				t.Fatalf("invalid import reached edit/start: %+v", runner.calls)
			}
		})
	}
}

func TestMigrationImportedRuntimeEditFailureAndImageBindingFailClosed(t *testing.T) {
	fixture := newImportedRuntimeFixture(t)
	runner := &importedRuntimeRunner{
		instance: fixture.session.InstanceName, config: fixture.instanceConfig, failEdit: true,
	}
	b := Backend{Runner: runner, Stdout: io.Discard, Stderr: io.Discard}
	_, _, err := b.startAndObserveRuntime(context.Background(), fixture.session, nil)
	if err == nil || !strings.Contains(err.Error(), "edit imported Lima runtime mounts") || len(runner.calls) != 2 {
		t.Fatalf("edit failure error=%v calls=%+v", err, runner.calls)
	}

	fixture.session.RuntimeInstanceExpected = &backend.RuntimeInstanceExpectation{
		ImageLocation: fixture.marker.RuntimeImageLocation,
		ImageSHA256: strings.TrimPrefix(
			string(fixture.marker.RuntimeImageDigest), "sha256:",
		),
		PackageInventorySHA256: strings.Repeat("c", 64),
		HostOS:                 "darwin", HostArch: "arm64", GuestArch: "aarch64", VMType: "vz",
	}
	matched, err := b.importedRuntimeImageMatches(
		HostCommandEnv(os.Environ()), fixture.session,
		[]runtimeLimaImage{{Location: migrationImportedRootImageSentinel, Arch: "aarch64"}},
	)
	if err != nil || !matched {
		t.Fatalf("imported runtime image binding matched=%t err=%v", matched, err)
	}
	fixture.session.RuntimeInstanceExpected.ImageSHA256 = strings.Repeat("d", 64)
	if matched, err = b.importedRuntimeImageMatches(
		HostCommandEnv(os.Environ()), fixture.session,
		[]runtimeLimaImage{{Location: migrationImportedRootImageSentinel, Arch: "aarch64"}},
	); err == nil || matched || !strings.Contains(err.Error(), "does not bind") {
		t.Fatalf("substituted image binding matched=%t err=%v", matched, err)
	}
}

func TestMigrationImportedRuntimeMountEditUsesInstalledLimaWithoutStartingVM(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("release-gated Lima import edit requires macOS arm64")
	}
	limactl, err := exec.LookPath("limactl")
	if err != nil {
		t.Skip("limactl not installed")
	}
	versionOutput, err := exec.Command(limactl, "--version").CombinedOutput()
	if err != nil || !strings.Contains(string(versionOutput), "2.2.0") {
		t.Skipf("installed Lima is not the release-gated 2.2.0: %s", versionOutput)
	}
	fixture := newImportedRuntimeFixture(t)
	if err := os.WriteFile(
		filepath.Join(filepath.Dir(fixture.instanceConfig), "lima-version"),
		[]byte("2.2.0\n"), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	expected, err := expectedImportedRuntimeMounts(fixture.session)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		limactl, "edit", "--tty=false", "--set", ".mounts = "+string(encoded),
		fixture.session.InstanceName,
	)
	command.Env = HostCommandEnv(os.Environ())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("zero-VM limactl edit: %v\n%s", err, output)
	}
	updated, err := readImportedLimaRuntimeConfig(fixture.instanceConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(updated.Mounts, expected) ||
		len(updated.Images) != 1 || updated.Images[0].Location != migrationImportedRootImageSentinel ||
		!reflect.DeepEqual(updated.AdditionalDisks, []string{"disk_imported_data1"}) {
		t.Fatalf("installed Lima changed unrelated imported config: %+v", updated)
	}
}

type importedRuntimeFixture struct {
	session        *backend.Session
	marker         migrationNormalizedStageConfig
	instanceConfig string
	normalizedPath string
	rootDisk       string
}

func newImportedRuntimeFixture(t *testing.T) importedRuntimeFixture {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "hm.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("LIMA_HOME", home)
	instance := "backend_importedruntime1"
	instanceDir := filepath.Join(home, instance)
	if err := os.Mkdir(instanceDir, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDisk := filepath.Join(instanceDir, "disk")
	if err := os.WriteFile(rootDisk, []byte("imported-root"), 0o600); err != nil {
		t.Fatal(err)
	}
	instanceConfig := filepath.Join(instanceDir, "lima.yaml")
	writeImportedRuntimeYAML(t, instanceConfig, migrationStagedLimaConfig{
		VMType: "vz", Arch: "aarch64",
		Images:    []limaImage{{Location: migrationImportedRootImageSentinel, Arch: "aarch64"}},
		MountType: "virtiofs", Mounts: []mount{},
		AdditionalDisks: []string{"disk_imported_data1"},
	})
	marker := migrationNormalizedStageConfig{
		Schema:         migrationStageConfigSchema,
		EnvironmentRef: "environment_source1", BackendIdentity: migration.OpaqueID(instance),
		Runtime: "linux", GuestArchitecture: "linux/arm64", GuestUser: "developer",
		ProfileComponent:     "component_profile1",
		RuntimeImageLocation: "https://example.invalid/runtime.qcow2",
		RuntimeImageDigest:   migration.Digest("sha256:" + strings.Repeat("a", 64)),
		RootDiskID:           "disk_rootimport1",
		RootDiskLogicalBytes: uint64(len("imported-root")),
		AttachedDiskHandles:  []migration.OpaqueID{"disk_imported_data1"},
	}
	normalizedPath := filepath.Join(instanceDir, "normalized.json")
	writeImportedRuntimeJSON(t, normalizedPath, marker)

	root := t.TempDir()
	identityRoot := filepath.Join(root, "profile")
	runtimeRoot := filepath.Join(root, "runtime")
	for _, path := range []string{identityRoot, runtimeRoot} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sessionConfig := filepath.Join(root, "session", "lima.yaml")
	expectedMounts := identityStateMounts(identityRoot)
	expectedMounts = append(expectedMounts, mount{
		Location: runtimeRoot, MountPoint: GuestRuntimeDir, Writable: true,
	})
	writeImportedRuntimeYAML(t, sessionConfig, migrationStagedLimaConfig{Mounts: expectedMounts})
	session := &backend.Session{
		ID: "ses_imported_runtime1", EnvironmentID: "env_imported_runtime1",
		InstanceName: instance, ConfigPath: sessionConfig, TargetUser: "developer",
		IdentityRoot: identityRoot, RuntimeRoot: runtimeRoot,
		Workspace:        backend.WorkspaceAttachmentSpec{Transport: backend.WorkspaceTransportPortal},
		PreserveInstance: true,
	}
	return importedRuntimeFixture{
		session: session, marker: marker, instanceConfig: instanceConfig,
		normalizedPath: normalizedPath, rootDisk: rootDisk,
	}
}

func writeImportedRuntimeYAML(t *testing.T, path string, value any) {
	t.Helper()
	data, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeImportedRuntimeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type importedRuntimeRunner struct {
	instance string
	config   string
	failEdit bool
	calls    []recordedCall
}

func (runner *importedRuntimeRunner) LookPath(string) (string, error) {
	return "/opt/homebrew/bin/limactl", nil
}

func (runner *importedRuntimeRunner) Run(
	_ context.Context,
	name string,
	args []string,
	env []string,
	_ io.Reader,
	stdout io.Writer,
	_ io.Writer,
) error {
	runner.calls = append(runner.calls, recordedCall{
		name: name, args: append([]string(nil), args...), env: append([]string(nil), env...),
	})
	if reflect.DeepEqual(args, []string{"list", "--quiet"}) {
		_, _ = io.WriteString(stdout, runner.instance+"\n")
		return nil
	}
	if len(args) == 5 && args[0] == "edit" && args[1] == "--tty=false" && args[2] == "--set" {
		if runner.failEdit {
			return errors.New("injected edit failure")
		}
		const prefix = ".mounts = "
		if !strings.HasPrefix(args[3], prefix) || args[4] != runner.instance {
			return errors.New("unexpected imported runtime edit")
		}
		var mounts []mount
		if err := json.Unmarshal([]byte(strings.TrimPrefix(args[3], prefix)), &mounts); err != nil {
			return err
		}
		config, err := readImportedLimaRuntimeConfig(runner.config)
		if err != nil {
			return err
		}
		config.Mounts = mounts
		data, err := yaml.Marshal(config)
		if err != nil {
			return err
		}
		return os.WriteFile(runner.config, data, 0o600)
	}
	if reflect.DeepEqual(args, []string{"start", "--tty=false", runner.instance}) {
		return nil
	}
	return errors.New("unexpected imported runtime command")
}
