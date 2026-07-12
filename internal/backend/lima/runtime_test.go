package lima

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
)

func TestRuntimeObservationRunsBeforeSetupAsTargetDirectArgv(t *testing.T) {
	runner := newRuntimeRecordingRunner(true)
	b := Backend{Runner: runner, Stdin: bytes.NewReader(nil), Stdout: io.Discard, Stderr: io.Discard}
	var report backend.RuntimeObservationReport
	session := runtimeTestSession()
	session.RuntimeContract = &backend.RuntimeContract{
		ID: "developer-standard/v1", Digest: "sha256:" + strings.Repeat("a", 64),
		Observations: []backend.RuntimeObservation{{
			ID: "boundary.sh", Class: backend.RuntimeObservationBoundary, Command: "sh",
			VersionArgs: []string{"--version"}, OutputPattern: "^call7$",
		}},
	}
	session.RuntimeResultSink = func(got backend.RuntimeObservationReport) error { report = got; return nil }
	env := GuestEnv([]string{"HOME=/Users/alice", "PATH=/Users/alice/bin:/usr/bin"})
	if err := b.Run(context.Background(), session, []string{"true"}, env); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || !report.Results[0].Present || !report.Results[0].Matched {
		t.Fatalf("runtime report=%+v", report)
	}
	if len(runner.calls) != 11 {
		t.Fatalf("runtime/setup/target calls=%+v", runner.calls)
	}
	check := strings.Join(runner.calls[5].args, " ")
	version := strings.Join(runner.calls[6].args, " ")
	if !strings.Contains(check, `command -v "$1"`) || !strings.HasSuffix(version, " sh --version") {
		t.Fatalf("runtime probes are not fixed check + direct argv: check=%s version=%s", check, version)
	}
	if strings.Contains(check+version, "/Users/alice") || !strings.Contains(check+version, "PATH=/hideout/session/shims:/hideout/profile/home/.local/bin") {
		t.Fatalf("runtime probe did not use target guest env: %s %s", check, version)
	}
	if got := strings.Join(runner.calls[7].args, " "); !strings.HasSuffix(got, " /hideout/session/network/probe.sh") {
		t.Fatalf("network setup did not follow runtime observations: %s", got)
	}
}

func TestRuntimeBoundaryFailureStopsBeforeSetupAndTarget(t *testing.T) {
	runner := newRuntimeRecordingRunner(false)
	runner.failCall = 6
	b := Backend{Runner: runner, Stdin: bytes.NewReader(nil), Stdout: io.Discard, Stderr: io.Discard}
	session := runtimeTestSession()
	session.RuntimeContract = &backend.RuntimeContract{
		ID: "developer-standard/v1", Digest: "sha256:" + strings.Repeat("a", 64),
		Observations: []backend.RuntimeObservation{{ID: "boundary.iptables", Class: backend.RuntimeObservationBoundary, Command: "iptables"}},
	}
	var report backend.RuntimeObservationReport
	session.RuntimeResultSink = func(got backend.RuntimeObservationReport) error { report = got; return nil }
	err := b.Run(context.Background(), session, []string{"touch", "/workspace/target-side-effect"}, GuestEnv([]string{"HOME=/tmp", "PATH=/usr/bin"}))
	var boundary backend.RuntimeBoundaryError
	if !errors.As(err, &boundary) || len(boundary.FailedIDs) != 1 {
		t.Fatalf("expected boundary failure, got %T %v", err, err)
	}
	if len(runner.calls) != 6 || len(report.BoundaryFailed) != 1 || report.Results[0].Reason != "command-missing" {
		t.Fatalf("boundary failure crossed setup/target: calls=%+v report=%+v", runner.calls, report)
	}
}

func TestRuntimeBaselineFailureDoesNotBlockDifferentPresentCommand(t *testing.T) {
	runner := newRuntimeRecordingRunner(false)
	runner.failCall = 6
	b := Backend{Runner: runner, Stdin: bytes.NewReader(nil), Stdout: io.Discard, Stderr: io.Discard}
	session := runtimeTestSession()
	session.RuntimeContract = &backend.RuntimeContract{
		ID: "developer-standard/v1", Digest: "sha256:" + strings.Repeat("a", 64),
		Observations: []backend.RuntimeObservation{{ID: "baseline.git", Class: backend.RuntimeObservationBaseline, Command: "git"}},
	}
	var report backend.RuntimeObservationReport
	session.RuntimeResultSink = func(got backend.RuntimeObservationReport) error { report = got; return nil }
	if err := b.Run(context.Background(), session, []string{"true"}, GuestEnv([]string{"HOME=/tmp", "PATH=/usr/bin"})); err != nil {
		t.Fatal(err)
	}
	if len(report.BaselineFailed) != 1 || len(report.BoundaryFailed) != 0 || len(runner.calls) != 10 {
		t.Fatalf("baseline degradation did not continue honestly: calls=%+v report=%+v", runner.calls, report)
	}
	if got := strings.Join(runner.calls[len(runner.calls)-1].args, " "); !strings.HasSuffix(got, " true") {
		t.Fatalf("unrelated target did not run: %s", got)
	}
}

func TestRuntimeObservationRejectsUnsafeContractAndBoundsOutput(t *testing.T) {
	unsafe := backend.RuntimeContract{
		ID: "unsafe", Digest: "sha256:" + strings.Repeat("a", 64),
		Observations: []backend.RuntimeObservation{{ID: "x", Class: backend.RuntimeObservationBaseline, Command: "sh", VersionArgs: []string{"-c", "id"}}},
	}
	if err := unsafe.Validate(); err == nil {
		t.Fatal("shell-like runtime contract should fail")
	}
	runner := &runtimeLargeOutputRunner{}
	b := Backend{Runner: runner}
	session := runtimeTestSession()
	session.RuntimeContract = &backend.RuntimeContract{
		ID: "bounded", Digest: "sha256:" + strings.Repeat("a", 64),
		Observations: []backend.RuntimeObservation{{ID: "baseline.git", Class: backend.RuntimeObservationBaseline, Command: "git", VersionArgs: []string{"--version"}}},
	}
	var report backend.RuntimeObservationReport
	session.RuntimeResultSink = func(got backend.RuntimeObservationReport) error { report = got; return nil }
	if err := b.observeRuntime(context.Background(), session, runner, nil, []string{"PATH=/usr/bin"}, runtimeInstanceObservation()); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].Reason != "output-limit-exceeded" || len(report.Results[0].Output) != runtimeProcessOutputLimit {
		t.Fatalf("output limit report=%+v", report)
	}
}

func TestRuntimeObservationCancellationWritesNoReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := runtimeCanceledRunner{}
	b := Backend{Runner: runner}
	session := runtimeTestSession()
	session.RuntimeContract = &backend.RuntimeContract{
		ID: "canceled", Digest: "sha256:" + strings.Repeat("a", 64),
		Observations: []backend.RuntimeObservation{{ID: "boundary.sh", Class: backend.RuntimeObservationBoundary, Command: "sh"}},
	}
	called := false
	session.RuntimeResultSink = func(backend.RuntimeObservationReport) error { called = true; return nil }
	if err := b.observeRuntime(ctx, session, runner, nil, []string{"PATH=/usr/bin"}, runtimeInstanceObservation()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled observation error=%v", err)
	}
	if called {
		t.Fatal("canceled observation wrote a report")
	}
}

func TestReusableRuntimeInstanceMismatchFailsBeforeStart(t *testing.T) {
	runner := &reusedRuntimeMismatchRunner{}
	b := Backend{Runner: runner, Stdout: io.Discard, Stderr: io.Discard}
	session := runtimeTestSession()
	session.EnvironmentID = "env_runtime_test"
	session.PreserveInstance = true
	session.RuntimeContract = &backend.RuntimeContract{
		ID: "developer-standard/v1", Digest: "sha256:" + strings.Repeat("a", 64),
		Observations: []backend.RuntimeObservation{{ID: "baseline.git", Class: backend.RuntimeObservationBaseline, Command: "git"}},
	}
	session.RuntimeResultSink = func(backend.RuntimeObservationReport) error { return nil }
	_, _, err := b.startAndObserveRuntime(context.Background(), session, []string{"PATH=/usr/bin"})
	if err == nil || !strings.Contains(err.Error(), "refuse mismatched reusable") {
		t.Fatalf("mismatched reused instance error=%v", err)
	}
	if runner.started {
		t.Fatal("mismatched reused instance was started by name")
	}
}

func TestRuntimeInstanceInspectionBindsInventoryImageArchAndBoot(t *testing.T) {
	runner := newRuntimeRecordingRunner(false)
	b := Backend{Runner: runner, Stdout: io.Discard, Stderr: io.Discard}
	session := runtimeTestSession()
	observed, err := b.inspectRuntimeInstance(context.Background(), runner, nil, session, true)
	if err != nil {
		t.Fatal(err)
	}
	if observed.InstanceName != session.InstanceName || observed.Status != "Running" || observed.VMType != "vz" ||
		observed.HostOS != "darwin" || observed.HostArch != "arm64" || observed.GuestArch != "aarch64" ||
		observed.ImageLocation != session.RuntimeInstanceExpected.ImageLocation || observed.ImageSHA256 != session.RuntimeInstanceExpected.ImageSHA256 ||
		observed.PackageInventorySHA256 != session.RuntimeInstanceExpected.PackageInventorySHA256 ||
		observed.BootID != "01234567-89ab-cdef-0123-456789abcdef" || observed.SessionID != session.ID {
		t.Fatalf("runtime instance observation=%+v", observed)
	}
	if len(runner.calls) != 4 {
		t.Fatalf("runtime instance observation calls=%+v", runner.calls)
	}
	assertDirectPackageInventoryArgv(t, runner.calls[3].args)
}

func TestRuntimeInstanceInspectionRejectsUntrustedGuestInventory(t *testing.T) {
	validDigest := strings.Repeat("f", 64)
	for _, tc := range []struct {
		name       string
		output     string
		commandErr error
		wantOK     bool
	}{
		{name: "valid exact path", output: validDigest + "  /etc/hideout/package-inventory.txt\n", wantOK: true},
		{name: "digest mismatch", output: strings.Repeat("9", 64) + "  /etc/hideout/package-inventory.txt\n"},
		{name: "missing file", commandErr: errors.New("sha256sum: file missing")},
		{name: "multiple lines", output: validDigest + "  /etc/hideout/package-inventory.txt\n" + validDigest + "  /etc/hideout/package-inventory.txt\n"},
		{name: "wrong path", output: validDigest + "  /tmp/package-inventory.txt\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := newRuntimeRecordingRunner(false)
			runner.inventoryOutput = tc.output
			runner.inventoryErr = tc.commandErr
			b := Backend{Runner: runner, Stdout: io.Discard, Stderr: io.Discard}
			observed, err := b.inspectRuntimeInstance(context.Background(), runner, nil, runtimeTestSession(), true)
			if tc.wantOK {
				if err != nil || observed.PackageInventorySHA256 != validDigest {
					t.Fatalf("valid active build identity observation=%+v err=%v", observed, err)
				}
			} else if err == nil {
				t.Fatalf("untrusted guest inventory was accepted: %+v", observed)
			}
			if len(runner.calls) == 4 {
				assertDirectPackageInventoryArgv(t, runner.calls[3].args)
			}
		})
	}
}

func runtimeTestSession() *backend.Session {
	return &backend.Session{
		ID: "ses_runtime_test", InstanceName: "hideout-runtime-test", ConfigPath: "/tmp/lima.yaml", GuestWork: "/workspace",
		NetworkBootstrapGuestPath: "/hideout/session/network/probe.sh", BootstrapPath: "/tmp/bootstrap.sh",
		RuntimeInstanceExpected: &backend.RuntimeInstanceExpectation{
			ImageLocation: "https://example.invalid/runtime.qcow2", ImageSHA256: strings.Repeat("a", 64),
			PackageInventorySHA256: strings.Repeat("f", 64),
			HostOS:                 "darwin", HostArch: "arm64", GuestArch: "aarch64", VMType: "vz",
		},
	}
}

func runtimeInstanceObservation() backend.RuntimeInstanceObservation {
	return backend.RuntimeInstanceObservation{
		InstanceName: "hideout-runtime-test", Status: "Running", VMType: "vz",
		HostOS: "darwin", HostArch: "arm64", GuestArch: "aarch64",
		ImageLocation: "https://example.invalid/runtime.qcow2", ImageSHA256: strings.Repeat("a", 64),
		PackageInventorySHA256: strings.Repeat("f", 64),
		BootID:                 "01234567-89ab-cdef-0123-456789abcdef", SessionID: "ses_runtime_test",
	}
}

type runtimeRecordingRunner struct {
	*recordingRunner
	inventoryOutput string
	inventoryErr    error
}

func newRuntimeRecordingRunner(emitOutput bool) *runtimeRecordingRunner {
	return &runtimeRecordingRunner{
		recordingRunner: &recordingRunner{lookPath: "/opt/homebrew/bin/limactl", emitOutput: emitOutput},
		inventoryOutput: strings.Repeat("f", 64) + "  /etc/hideout/package-inventory.txt\n",
	}
}

func (r *runtimeRecordingRunner) Run(ctx context.Context, name string, args []string, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if isDirectPackageInventoryArgv(args) {
		r.calls = append(r.calls, recordedCall{name: name, args: append([]string(nil), args...), env: append([]string(nil), env...)})
		if r.failCall > 0 && len(r.calls) == r.failCall {
			return errors.New("guest command not found")
		}
		_, _ = io.WriteString(stdout, r.inventoryOutput)
		return r.inventoryErr
	}
	return r.recordingRunner.Run(ctx, name, args, env, stdin, stdout, stderr)
}

func assertDirectPackageInventoryArgv(t *testing.T, args []string) {
	t.Helper()
	if !isDirectPackageInventoryArgv(args) {
		t.Fatalf("package inventory probe is not fixed direct argv: %q", args)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, " sh ") || strings.Contains(joined, "PATH=") || strings.Contains(joined, "/workspace") {
		t.Fatalf("package inventory probe used shell, target PATH, or target workdir: %q", args)
	}
}

func isDirectPackageInventoryArgv(args []string) bool {
	return len(args) >= 3 && args[len(args)-3] == "--" && args[len(args)-2] == "sha256sum" &&
		args[len(args)-1] == "/etc/hideout/package-inventory.txt"
}

type runtimeLargeOutputRunner struct{ calls int }

func (r *runtimeLargeOutputRunner) LookPath(string) (string, error) { return "/usr/bin/limactl", nil }

func (r *runtimeLargeOutputRunner) Run(_ context.Context, _ string, _ []string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
	r.calls++
	if r.calls == 2 {
		_, _ = io.WriteString(stdout, strings.Repeat("x", runtimeProcessOutputLimit+100))
	}
	return nil
}

type runtimeCanceledRunner struct{}

func (runtimeCanceledRunner) LookPath(string) (string, error) { return "/usr/bin/limactl", nil }

func (runtimeCanceledRunner) Run(ctx context.Context, _ string, _ []string, _ []string, _ io.Reader, _ io.Writer, _ io.Writer) error {
	return ctx.Err()
}

type reusedRuntimeMismatchRunner struct{ started bool }

func (*reusedRuntimeMismatchRunner) LookPath(string) (string, error) { return "/usr/bin/limactl", nil }

func (r *reusedRuntimeMismatchRunner) Run(_ context.Context, _ string, args []string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
	if len(args) == 2 && args[0] == "list" && args[1] == "--quiet" {
		_, _ = io.WriteString(stdout, "hideout-runtime-test\n")
		return nil
	}
	if len(args) >= 3 && args[0] == "list" && args[1] == "--format" {
		_, _ = io.WriteString(stdout, `{"name":"hideout-runtime-test","status":"Stopped","vmType":"qemu","arch":"aarch64","HostOS":"darwin","HostArch":"arm64","config":{"vmType":"qemu","arch":"aarch64","images":[{"location":"https://attacker.invalid/other.qcow2","arch":"aarch64","digest":"sha256:`+strings.Repeat("9", 64)+`"}]}}`+"\n")
		return nil
	}
	if len(args) > 0 && args[0] == "start" {
		r.started = true
	}
	return nil
}
