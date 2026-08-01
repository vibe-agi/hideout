//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestVerifiedObserverCommandExecutesOpenedInodeAfterPathReplacement(
	t *testing.T,
) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hideout-observer")
	original := []byte("#!/bin/sh\nprintf 'verified-inode\\n'\n")
	if err := os.WriteFile(path, original, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(original)
	helper, err := openVerifiedObserverHelper(
		path,
		"sha256:"+hex.EncodeToString(sum[:]),
		uint32(os.Getuid()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer helper.Close()

	if err := os.Rename(path, path+".opened"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		path,
		[]byte("#!/bin/sh\nprintf 'replacement-path\\n'\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}

	command := helper.Command()
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run verified descriptor: %v: %s", err, stderr.String())
	}
	if got := stdout.String(); got != "verified-inode\n" {
		t.Fatalf("executed %q, want the verified opened inode", got)
	}
}

func TestOpenVerifiedObserverHelperRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "observer-target")
	data := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(target, data, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hideout-observer")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	helper, err := openVerifiedObserverHelper(
		path,
		"sha256:"+hex.EncodeToString(sum[:]),
		uint32(os.Getuid()),
	)
	if helper != nil {
		_ = helper.Close()
		t.Fatal("symlink returned an executable helper")
	}
	if err == nil {
		t.Fatal("symlink helper was accepted")
	}
}

func TestObserverEndpointCloseNormalizesOnlyAlreadyClosedEndpoints(t *testing.T) {
	for _, err := range []error{
		os.ErrClosed,
		io.ErrClosedPipe,
		&os.PathError{Op: "close", Path: "|0", Err: os.ErrClosed},
	} {
		if got := observerEndpointCloseError(err); got != nil {
			t.Fatalf("already-closed endpoint error=%v", got)
		}
	}
	sentinel := errors.New("close failed")
	if got := observerEndpointCloseError(sentinel); !errors.Is(got, sentinel) {
		t.Fatalf("real close failure=%v want %v", got, sentinel)
	}
}

func TestObserverSessionRegistersBeforeReadyAndAbortsExactBoundary(t *testing.T) {
	spec := observerSupervisorStart(t)
	backend := newFakeSessionCgroupBackend()
	helper := newFakeObserverHelper(t, observerUnavailableCapabilities())
	cgroupRoot := observerTestCgroupRoot(t)
	session, err := prepareObserverSession(spec, observerSessionOptions{
		Cgroup: sessionCgroupOptions{
			Root: cgroupRoot, Backend: backend,
		},
		ObserverPath:  "/test/fixed/hideout-observer",
		Launch:        helper.Launch,
		MonotonicNS:   func() (uint64, error) { return 424242, nil },
		HandshakeWait: time.Second,
		ShutdownWait:  time.Second,
		Relay:         observerRelayTestOptions(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	ready := session.Ready()
	if ready.Boundary.CgroupID != backend.created.ID ||
		ready.Boundary.CgroupPath != filepath.Join(
			filepath.Dir(filepath.Dir(session.group.Path())),
			"sessions",
			spec.SessionID,
		) ||
		ready.Boundary.State != workloadtypes.BoundaryReady ||
		ready.Boundary.CreatedAtMonoNS != 424242 {
		t.Fatalf("ready=%+v", ready)
	}
	accepted, heartbeats, _ := helper.State()
	if !accepted || heartbeats != 1 {
		t.Fatalf("helper accepted=%v heartbeats=%d", accepted, heartbeats)
	}
	for _, coverage := range ready.Coverage {
		if coverage.State != workloadtypes.CoverageUnavailable ||
			coverage.DroppedEventCount != 0 {
			t.Fatalf("coverage=%+v", coverage)
		}
	}
	if backend.removed {
		t.Fatal("ready workload boundary was removed before commit decision")
	}
	if err := session.Abort(time.Second); err != nil {
		t.Fatal(err)
	}
	_, _, stopped := helper.State()
	if !backend.removed || backend.closed != 1 || !stopped {
		t.Fatalf(
			"cleanup removed=%v closes=%d helperStopped=%v",
			backend.removed,
			backend.closed,
			stopped,
		)
	}
}

func TestObserverSessionFailureClosesRelayWithoutDrainMarker(t *testing.T) {
	spec := observerSupervisorStart(t)
	backend := newFakeSessionCgroupBackend()
	helper := newFakeObserverHelper(t, observerUnavailableCapabilities())
	helper.exitErr = errors.New("observer helper exited unsuccessfully")
	session, err := prepareObserverSession(spec, observerSessionOptions{
		Cgroup: sessionCgroupOptions{
			Root: observerTestCgroupRoot(t), Backend: backend,
		},
		ObserverPath:  "/test/fixed/hideout-observer",
		Launch:        helper.Launch,
		MonotonicNS:   func() (uint64, error) { return 424242, nil },
		HandshakeWait: time.Second,
		ShutdownWait:  time.Second,
		Relay:         observerRelayTestOptions(t),
	})
	if err != nil {
		t.Fatal(err)
	}

	err = session.Abort(time.Second)
	if !errors.Is(err, helper.exitErr) {
		t.Fatalf("abort error=%v want %v", err, helper.exitErr)
	}
	if session.relay.draining.Load() {
		t.Fatal("unsuccessful helper exit published a false relay drain marker")
	}
	if !backend.removed {
		t.Fatal("unsuccessful helper exit leaked its observer cgroup")
	}
	for _, summary := range session.Completion(helper.exitErr).Coverage {
		if summary.DroppedEventCount == 0 ||
			!containsString(summary.Evidence, "observer-shutdown-unproved") {
			t.Fatalf("failed helper coverage=%+v", summary)
		}
	}
}

func TestObserverSessionRejectsForeignRegistrationAndCleansBoundary(t *testing.T) {
	spec := observerSupervisorStart(t)
	backend := newFakeSessionCgroupBackend()
	helper := newFakeObserverHelper(t, observerUnavailableCapabilities())
	cgroupRoot := observerTestCgroupRoot(t)
	helper.mutateHello = func(hello *sessionwire.ObserverHello) {
		hello.CgroupID++
	}
	_, err := prepareObserverSession(spec, observerSessionOptions{
		Cgroup: sessionCgroupOptions{
			Root: cgroupRoot, Backend: backend,
		},
		ObserverPath:  "/test/fixed/hideout-observer",
		Launch:        helper.Launch,
		MonotonicNS:   func() (uint64, error) { return 1, nil },
		HandshakeWait: time.Second,
		ShutdownWait:  time.Second,
		Relay:         observerRelayTestOptions(t),
	})
	if !errors.Is(err, sessionwire.ErrObserverIdentity) {
		t.Fatalf("error=%v want %v", err, sessionwire.ErrObserverIdentity)
	}
	if !backend.removed || backend.closed != 1 {
		t.Fatalf("rejected registration cleanup removed=%v closes=%d", backend.removed, backend.closed)
	}
}

func TestSupervisorCommitFailureAbortsPreparedObserverWithoutStartingTarget(t *testing.T) {
	spec := observerSupervisorStart(t)
	backend := newFakeSessionCgroupBackend()
	helper := newFakeObserverHelper(t, observerUnavailableCapabilities())
	cgroupRoot := observerTestCgroupRoot(t)
	wire := &commitFailureWire{
		spec:      spec,
		commitErr: errors.New("commit rejected"),
	}
	started := false
	err := runSupervisorWireWithActivity(
		wire,
		func(startSpec) error { return nil },
		func(value startSpec) (*observerSession, error) {
			return prepareObserverSession(value, observerSessionOptions{
				Cgroup: sessionCgroupOptions{
					Root: cgroupRoot, Backend: backend,
				},
				ObserverPath:  "/test/fixed/hideout-observer",
				Launch:        helper.Launch,
				MonotonicNS:   func() (uint64, error) { return 99, nil },
				HandshakeWait: time.Second,
				ShutdownWait:  time.Second,
				Relay:         observerRelayTestOptions(t),
			})
		},
		func(startSpec, supervisorWire) (*targetProcess, error) {
			started = true
			return nil, errors.New("must not start")
		},
		func(*targetProcess, supervisorWire) error {
			return errors.New("must not run")
		},
	)
	if !errors.Is(err, wire.commitErr) {
		t.Fatalf("error=%v want %v", err, wire.commitErr)
	}
	if started || !wire.ready || wire.readyActivity == nil {
		t.Fatalf("started=%v ready=%v activity=%+v", started, wire.ready, wire.readyActivity)
	}
	_, _, stopped := helper.State()
	if !backend.removed || !stopped {
		t.Fatalf("commit refusal leaked boundary/helper: removed=%v stopped=%v", backend.removed, stopped)
	}
}

func TestObservedTargetUsesPreparedBoundaryAndCompletesAfterObserverShutdown(t *testing.T) {
	spec := linuxTestStart(t, terminalSpec{Mode: "none"}, []string{"true"})
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	spec.ProjectionReadiness = &projectionReadinessSpec{
		EnvironmentID:     "env_fixture",
		SessionSnapshotID: "sha256:" + strings.Repeat("b", 64),
		CatalogDigest:     "sha256:" + strings.Repeat("c", 64),
		ExpectedEntries:   1,
		TargetProjected:   true,
	}
	spec.Activity = &sessionwire.SupervisorActivityExpectation{
		Owner: owner, ObserverGeneration: 5,
		ObserverHelperDigest: "sha256:" + strings.Repeat("a", 64),
		ObserverStreamToken:  sessionwire.ObserverStreamToken{1},
	}
	backend := newFakeSessionCgroupBackend()
	helper := newFakeObserverHelper(t, observerUnavailableCapabilities())
	session, err := prepareObserverSession(spec, observerSessionOptions{
		Cgroup: sessionCgroupOptions{
			Root: observerTestCgroupRoot(t), Backend: backend,
		},
		ObserverPath:  "/test/fixed/hideout-observer",
		Launch:        helper.Launch,
		MonotonicNS:   func() (uint64, error) { return 101, nil },
		HandshakeWait: time.Second,
		ShutdownWait:  time.Second,
		Relay:         observerRelayTestOptions(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	ready := session.Ready()
	spec.activityRuntime = session
	wire := newRecordingWire()
	process, err := startTargetWithSessionCgroup(spec, wire, sessionCgroupOptions{
		Root:                       observerTestCgroupRoot(t),
		Backend:                    newFakeSessionCgroupBackend(),
		SkipAtomicPlacementForTest: true,
	})
	if err != nil {
		_ = session.Abort(time.Second)
		t.Fatal(err)
	}
	process.queue.begin()
	result := <-process.wait
	if err := process.finishOutput(); err != nil {
		t.Fatal(err)
	}
	if result.cleanupErr != nil {
		t.Fatal(result.cleanupErr)
	}
	if !result.completion.Completed || !result.completion.CleanupCompleted ||
		result.completion.Activity == nil ||
		result.completion.Activity.BoundaryState != workloadtypes.BoundaryRemoved ||
		!result.completion.Activity.CleanupProved {
		t.Fatalf("completion=%+v", result.completion)
	}
	if err := result.completion.Activity.ValidateReady(spec.SessionID, ready); err != nil {
		t.Fatal(err)
	}
	_, _, stopped := helper.State()
	if !backend.removed || !stopped {
		t.Fatalf("observed target cleanup removed=%v observerStopped=%v", backend.removed, stopped)
	}
}

func observerTestCgroupRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join("/sys/fs/cgroup", "hideout-test-"+filepath.Base(t.TempDir()))
}

func observerRelayTestOptions(t *testing.T) observerRelayOptions {
	t.Helper()
	return observerRelayOptions{
		Root:                      shortObserverRelayRoot(t),
		QueueEntries:              8,
		QueueBytes:                16 << 10,
		HandshakeWait:             time.Second,
		SkipRootOwnerCheckForTest: true,
	}
}

func observerSupervisorStart(t *testing.T) startSpec {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_20260729T120000Z_observer"
	return startSpec{
		Protocol: testProtocol, SessionID: sessionID, TargetUser: "developer",
		GuestWork: "/workspace", Argv: []string{"true"},
		Env: []string{"PATH=/usr/bin:/bin"}, Terminal: terminalSpec{Mode: "none"},
		ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
		SessionSource:  "/hideout/runtime/sessions/" + sessionID,
		ProjectionReadiness: &projectionReadinessSpec{
			EnvironmentID:     "env_fixture",
			SessionSnapshotID: "sha256:" + strings.Repeat("b", 64),
			CatalogDigest:     "sha256:" + strings.Repeat("c", 64),
			ExpectedEntries:   1,
			TargetProjected:   true,
		},
		Activity: &sessionwire.SupervisorActivityExpectation{
			Owner: owner, ObserverGeneration: 3,
			ObserverHelperDigest: "sha256:" + strings.Repeat("a", 64),
			ObserverStreamToken:  sessionwire.ObserverStreamToken{1},
		},
	}
}

func observerUnavailableCapabilities() sessionwire.ObserverCapabilities {
	return sessionwire.ObserverCapabilities{
		Process: sessionwire.ObserverCapability{
			State: workloadtypes.CoverageUnavailable, Reason: "process-collector-not-loaded",
			Evidence: []string{"cgroup-v2"},
		},
		File: sessionwire.ObserverCapability{
			State: workloadtypes.CoverageUnavailable, Reason: "file-collector-not-loaded",
			Evidence: []string{"cgroup-v2"},
		},
		Network: sessionwire.ObserverCapability{
			State: workloadtypes.CoverageUnavailable, Reason: "network-collector-not-loaded",
			Evidence: []string{"cgroup-v2"},
		},
		DNS: sessionwire.ObserverCapability{
			State: workloadtypes.CoverageUnavailable, Reason: "dns-collector-not-loaded",
			Evidence: []string{"cgroup-v2"},
		},
	}
}

type fakeObserverHelper struct {
	t            *testing.T
	capabilities sessionwire.ObserverCapabilities
	mutateHello  func(*sessionwire.ObserverHello)
	exitErr      error

	mu         sync.Mutex
	accepted   bool
	heartbeats int
	stopped    bool
}

func newFakeObserverHelper(
	t *testing.T,
	capabilities sessionwire.ObserverCapabilities,
) *fakeObserverHelper {
	t.Helper()
	return &fakeObserverHelper{t: t, capabilities: capabilities}
}

func (helper *fakeObserverHelper) Launch(
	spec observerLaunchSpec,
) (*observerHelperProcess, error) {
	helperInput, supervisorInput := io.Pipe()
	supervisorOutput, helperOutput := io.Pipe()
	done := make(chan error, 1)
	var closeOnce sync.Once
	closeHelper := func() {
		closeOnce.Do(func() {
			_ = helperInput.Close()
			_ = helperOutput.Close()
		})
	}
	go func() {
		hello := sessionwire.ObserverHello{
			Type: "observer.hello", Schema: sessionwire.ObserverWireSchema,
			Owner: spec.Binding.Owner, SessionID: spec.Binding.SessionID,
			EnvironmentID:        spec.Binding.EnvironmentID,
			BackendIncarnationID: spec.Binding.BackendIncarnationID,
			GuestBootID:          spec.Binding.GuestBootID, CgroupID: spec.Binding.CgroupID,
			ObserverGeneration: spec.Binding.ObserverGeneration,
			HelperDigest:       spec.Digest, Capabilities: helper.capabilities,
		}
		if helper.mutateHello != nil {
			helper.mutateHello(&hello)
		}
		if err := sessionwire.WriteObserverHello(helperOutput, hello); err != nil {
			closeHelper()
			done <- err
			return
		}
		accepted, err := sessionwire.ReadObserverAccepted(helperInput)
		if err != nil {
			closeHelper()
			done <- err
			return
		}
		if err := accepted.ValidateBinding(spec.Binding); err != nil {
			closeHelper()
			done <- err
			return
		}
		helper.mu.Lock()
		helper.accepted = true
		helper.heartbeats++
		helper.mu.Unlock()
		payload := []byte(`{"latestSequence":1,"kernelDropped":0,"ringDropped":0}`)
		if err := sessionwire.WriteObserverEnvelope(helperOutput, sessionwire.ObservationEnvelope{
			Schema: sessionwire.ObservationSchema, Owner: spec.Binding.Owner,
			SessionID: spec.Binding.SessionID, CgroupID: spec.Binding.CgroupID,
			ObserverGeneration: spec.Binding.ObserverGeneration,
			CPU:                0, Sequence: 1, MonotonicNS: 100,
			Kind: "collector.heartbeat", Payload: payload,
		}); err != nil {
			closeHelper()
			done <- err
			return
		}
		var shutdown [1]byte
		_, _ = helperInput.Read(shutdown[:])
		helper.mu.Lock()
		helper.stopped = true
		helper.mu.Unlock()
		closeHelper()
		done <- helper.exitErr
	}()
	return &observerHelperProcess{
		stdin: supervisorInput, stdout: supervisorOutput,
		wait: func() error {
			return <-done
		},
		kill: func() error {
			closeHelper()
			return nil
		},
	}, nil
}

func (helper *fakeObserverHelper) State() (bool, int, bool) {
	helper.mu.Lock()
	defer helper.mu.Unlock()
	return helper.accepted, helper.heartbeats, helper.stopped
}
