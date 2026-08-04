//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestObserverRegistersHeartbeatsAndStopsOnSupervisorEOF(t *testing.T) {
	config := observerTestConfig(t)
	workspace := observerWorkspaceBinding(t, "a")
	config.Workspace = &workspace
	collectors := newObserverTestCollectors()
	collectors.stop = func() {
		collectors.counters = observerDropCounters{
			Kernel: 2,
			Ring:   5,
			Local: observerLocalDropCounters{
				Process: 1,
			},
			File: observerFileCollectorCounters{
				MatchedEvents:     57,
				ReservedEvents:    55,
				RingbufDrops:      2,
				StateDrops:        1,
				StateDegradations: 6,
				PathFailures:      1,
				IdentityFailures:  1,
			},
		}
	}
	accepted := sessionwire.ObserverAccepted{
		Type: "observer.accepted", Schema: sessionwire.ObserverWireSchema,
		Owner: config.Binding.Owner, SessionID: config.Binding.SessionID,
		CgroupID: config.Binding.CgroupID, ObserverGeneration: config.Binding.ObserverGeneration,
		ExpectedNextSequence: 1, MaxFrameBytes: sessionwire.MaxObserverFrameSize,
	}
	var supervisorInput bytes.Buffer
	if err := sessionwire.WriteObserverAccepted(&supervisorInput, accepted); err != nil {
		t.Fatal(err)
	}
	var guestOutput bytes.Buffer
	err := runObserver(
		context.Background(),
		&supervisorInput,
		&guestOutput,
		config,
		observerDependencies{
			EUID:             func() int { return 0 },
			ExecutableDigest: func() (string, error) { return config.ExpectedDigest, nil },
			ValidateBoundary: func(path string, id uint64) error {
				if path != config.CgroupPath || id != config.Binding.CgroupID {
					t.Fatalf("boundary=%s/%d", path, id)
				}
				return nil
			},
			OpenCollectors: func(
				collectorConfig observerCollectorConfig,
			) (observerCollectorRuntime, error) {
				if collectorConfig.Workspace == nil ||
					collectorConfig.Workspace.WorkspaceID != workspace.WorkspaceID {
					t.Fatalf("collector workspace = %#v", collectorConfig.Workspace)
				}
				return collectors, nil
			},
			Now:         func() time.Time { return time.Unix(1, 0).UTC() },
			MonotonicNS: func() (uint64, error) { return 9001, nil },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	hello, err := sessionwire.ReadObserverHello(&guestOutput)
	if err != nil {
		t.Fatal(err)
	}
	if hello.HelperDigest != config.ExpectedDigest ||
		!hello.Owner.Equal(config.Binding.Owner) {
		t.Fatalf("hello=%+v", hello)
	}
	heartbeat, err := sessionwire.ReadObserverEnvelope(&guestOutput)
	if err != nil {
		t.Fatal(err)
	}
	if heartbeat.Kind != "collector.heartbeat" ||
		heartbeat.CPU != sessionwire.ObserverControlCPU ||
		heartbeat.Sequence != 1 ||
		heartbeat.MonotonicNS != 9001 {
		t.Fatalf("heartbeat=%+v", heartbeat)
	}
	var initialCounters struct {
		Final bool `json:"final"`
	}
	if err := json.Unmarshal(heartbeat.Payload, &initialCounters); err != nil {
		t.Fatal(err)
	}
	if initialCounters.Final {
		t.Fatal("initial heartbeat was marked final")
	}
	finalHeartbeat, err := sessionwire.ReadObserverEnvelope(&guestOutput)
	if err != nil {
		t.Fatal(err)
	}
	if finalHeartbeat.Kind != "collector.heartbeat" ||
		finalHeartbeat.CPU != sessionwire.ObserverControlCPU ||
		finalHeartbeat.Sequence != 2 ||
		finalHeartbeat.MonotonicNS != 9001 {
		t.Fatalf("final heartbeat=%+v", finalHeartbeat)
	}
	var finalCounters struct {
		LatestSequence uint64                        `json:"latestSequence"`
		KernelDropped  uint64                        `json:"kernelDropped"`
		RingDropped    uint64                        `json:"ringDropped"`
		Local          observerLocalDropCounters     `json:"local"`
		File           observerFileCollectorCounters `json:"file"`
		Final          bool                          `json:"final"`
	}
	if err := json.Unmarshal(finalHeartbeat.Payload, &finalCounters); err != nil {
		t.Fatal(err)
	}
	if finalCounters.LatestSequence != 2 ||
		finalCounters.KernelDropped != 2 || finalCounters.RingDropped != 5 ||
		finalCounters.Local.Process != 1 ||
		finalCounters.File.MatchedEvents != 57 ||
		finalCounters.File.ReservedEvents != 55 ||
		finalCounters.File.RingbufDrops != 2 ||
		finalCounters.File.StateDegradations != 6 ||
		!finalCounters.Final {
		t.Fatalf("final counters=%+v", finalCounters)
	}
	if guestOutput.Len() != 0 {
		t.Fatalf("unexpected trailing observer output: %d bytes", guestOutput.Len())
	}
}

func TestObserverSignalCancellationDrainsBeforeCancelingCollectorReads(t *testing.T) {
	config := observerTestConfig(t)
	collectors := newObserverTestCollectors()
	started := make(chan struct{})
	collectors.started = func() { close(started) }
	stopSawCanceled := false
	collectors.stop = func() {
		select {
		case <-collectors.startContext.Done():
			stopSawCanceled = true
		default:
		}
	}
	accepted := sessionwire.ObserverAccepted{
		Type: "observer.accepted", Schema: sessionwire.ObserverWireSchema,
		Owner: config.Binding.Owner, SessionID: config.Binding.SessionID,
		CgroupID: config.Binding.CgroupID, ObserverGeneration: config.Binding.ObserverGeneration,
		ExpectedNextSequence: 1, MaxFrameBytes: sessionwire.MaxObserverFrameSize,
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	writeResult := make(chan error, 1)
	go func() { writeResult <- sessionwire.WriteObserverAccepted(writer, accepted) }()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- runObserver(
			ctx,
			reader,
			io.Discard,
			config,
			observerDependencies{
				EUID:             func() int { return 0 },
				ExecutableDigest: func() (string, error) { return config.ExpectedDigest, nil },
				ValidateBoundary: func(string, uint64) error { return nil },
				OpenCollectors: func(observerCollectorConfig) (observerCollectorRuntime, error) {
					return collectors, nil
				},
				Now:         func() time.Time { return time.Unix(1, 0).UTC() },
				MonotonicNS: func() (uint64, error) { return 9001, nil },
			},
		)
	}()
	if err := <-writeResult; err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("observer collectors did not start")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("observer did not stop after signal cancellation")
	}
	if stopSawCanceled {
		t.Fatal("collector read context was canceled before the drain")
	}
}

func TestObserverRejectsNonRootDigestMismatchAndForeignAcceptance(t *testing.T) {
	config := observerTestConfig(t)
	dependencies := observerDependencies{
		EUID:             func() int { return 1000 },
		ExecutableDigest: func() (string, error) { return config.ExpectedDigest, nil },
		ValidateBoundary: func(string, uint64) error { return nil },
		OpenCollectors: func(
			observerCollectorConfig,
		) (observerCollectorRuntime, error) {
			return newObserverTestCollectors(), nil
		},
		Now:         func() time.Time { return time.Unix(1, 0).UTC() },
		MonotonicNS: func() (uint64, error) { return 1, nil },
	}
	if err := runObserver(context.Background(), bytes.NewReader(nil), io.Discard, config, dependencies); !errors.Is(err, sessionwire.ErrObserverTargetAuthority) {
		t.Fatalf("non-root error=%v", err)
	}

	dependencies.EUID = func() int { return 0 }
	dependencies.ExecutableDigest = func() (string, error) {
		return "sha256:" + strings.Repeat("b", 64), nil
	}
	if err := runObserver(context.Background(), bytes.NewReader(nil), io.Discard, config, dependencies); !errors.Is(err, sessionwire.ErrObserverAuthentication) {
		t.Fatalf("digest error=%v", err)
	}

	dependencies.ExecutableDigest = func() (string, error) { return config.ExpectedDigest, nil }
	foreign := sessionwire.ObserverAccepted{
		Type: "observer.accepted", Schema: sessionwire.ObserverWireSchema,
		Owner: config.Binding.Owner, SessionID: config.Binding.SessionID,
		CgroupID:             config.Binding.CgroupID + 1,
		ObserverGeneration:   config.Binding.ObserverGeneration,
		ExpectedNextSequence: 1, MaxFrameBytes: sessionwire.MaxObserverFrameSize,
	}
	var input bytes.Buffer
	if err := sessionwire.WriteObserverAccepted(&input, foreign); err != nil {
		t.Fatal(err)
	}
	if err := runObserver(context.Background(), &input, io.Discard, config, dependencies); !errors.Is(err, sessionwire.ErrObserverIdentity) {
		t.Fatalf("foreign acceptance error=%v", err)
	}
}

func TestObserverEmitterKeepsActivityAndControlSequencesContiguous(t *testing.T) {
	t.Parallel()

	config := observerTestConfig(t)
	var output bytes.Buffer
	emitter := &observerEmitter{
		writer:    &output,
		binding:   config.Binding,
		nextByCPU: make(map[uint64]uint64),
	}
	first := observerTestActivity(config, 100, "act_observeremit0001")
	second := observerTestActivity(config, 300, "act_observeremit0002")
	execution := observerTestExecution(t, config, 4242, 7, 20_000)
	if err := emitter.record(observerRecord{
		Record: first, CPU: 2, MonotonicNS: 10_000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := emitter.record(observerRecord{
		Execution: &execution, CPU: 2, MonotonicNS: 20_000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := emitter.record(observerRecord{
		Record: second, CPU: 2, MonotonicNS: 30_000,
	}); err != nil {
		t.Fatal(err)
	}
	if err := emitter.heartbeat(40_000, observerDropCounters{
		Kernel: 4,
		Ring:   5,
	}, false); err != nil {
		t.Fatal(err)
	}

	firstEnvelope, err := sessionwire.ReadObserverEnvelope(&output)
	if err != nil {
		t.Fatal(err)
	}
	executionEnvelope, err := sessionwire.ReadObserverEnvelope(&output)
	if err != nil {
		t.Fatal(err)
	}
	secondEnvelope, err := sessionwire.ReadObserverEnvelope(&output)
	if err != nil {
		t.Fatal(err)
	}
	heartbeat, err := sessionwire.ReadObserverEnvelope(&output)
	if err != nil {
		t.Fatal(err)
	}
	if firstEnvelope.CPU != 2 ||
		firstEnvelope.Sequence != 1 ||
		executionEnvelope.CPU != 2 ||
		executionEnvelope.Sequence != 2 ||
		executionEnvelope.Kind != "process.execution" ||
		secondEnvelope.CPU != 2 ||
		secondEnvelope.Sequence != 3 ||
		heartbeat.CPU != sessionwire.ObserverControlCPU ||
		heartbeat.Sequence != 1 {
		t.Fatalf(
			"outbound sequences first=%+v second=%+v heartbeat=%+v",
			firstEnvelope,
			secondEnvelope,
			heartbeat,
		)
	}
	var persistedExecution workloadtypes.Execution
	if err := json.Unmarshal(
		executionEnvelope.Payload,
		&persistedExecution,
	); err != nil {
		t.Fatal(err)
	}
	if persistedExecution.ID != execution.ID ||
		persistedExecution.StartedAtMonoNS != execution.StartedAtMonoNS {
		t.Fatalf(
			"execution snapshot was not preserved: %+v",
			persistedExecution,
		)
	}
	var persisted workloadtypes.ActivityRecord
	if err := json.Unmarshal(
		secondEnvelope.Payload,
		&persisted,
	); err != nil {
		t.Fatal(err)
	}
	if persisted.FirstSequence != 300 ||
		persisted.ID != second.ID {
		t.Fatalf("raw process ordering was not preserved: %+v", persisted)
	}
	var counters struct {
		LatestSequence uint64 `json:"latestSequence"`
		KernelDropped  uint64 `json:"kernelDropped"`
		RingDropped    uint64 `json:"ringDropped"`
	}
	if err := json.Unmarshal(heartbeat.Payload, &counters); err != nil {
		t.Fatal(err)
	}
	if counters.LatestSequence != 1 ||
		counters.KernelDropped != 4 ||
		counters.RingDropped != 5 {
		t.Fatalf("heartbeat counters=%+v", counters)
	}
}

func TestObserverConfigIsStrictAndBounded(t *testing.T) {
	config := observerTestConfig(t)
	ownerJSON, err := json.Marshal(config.Binding.Owner)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--owner", base64.RawURLEncoding.EncodeToString(ownerJSON),
		"--session", config.Binding.SessionID,
		"--environment", config.Binding.EnvironmentID,
		"--backend-incarnation", config.Binding.BackendIncarnationID,
		"--guest-boot", config.Binding.GuestBootID,
		"--cgroup-path", config.CgroupPath,
		"--cgroup-id", "3141",
		"--generation", "2",
		"--helper-digest", config.ExpectedDigest,
		"--heartbeat", "250ms",
		"--workspace-id", "wrk_" + strings.Repeat("a", 64),
		"--workspace-logical-root", "/workspace",
		"--workspace-physical-root", "/hideout/workspaces/wrk_" + strings.Repeat("a", 64),
	}
	parsed, err := parseObserverConfig(args)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Binding.CgroupID != 3141 || parsed.Heartbeat != 250*time.Millisecond ||
		parsed.Workspace == nil || parsed.Workspace.WorkspaceID != "wrk_"+strings.Repeat("a", 64) {
		t.Fatalf("parsed=%+v", parsed)
	}
	if _, err := parseObserverConfig(append(args, "target-controlled")); err == nil {
		t.Fatal("observer accepted a positional argument")
	}
	partial := append([]string(nil), args[:len(args)-4]...)
	if _, err := parseObserverConfig(partial); err == nil {
		t.Fatal("observer accepted a partial workspace identity")
	}
}

func TestCollectorFailureEvidenceIsAllowListedAndPathFree(t *testing.T) {
	err := errors.New(
		"attach workload observer tracepoint syscalls/sys_enter_execveat: " +
			"open /sys/kernel/tracing/events/syscalls/sys_enter_execveat/id: " +
			"operation not permitted",
	)
	got := collectorFailureEvidence("process", err)
	want := []string{
		"tracepoint.syscalls.sys_enter_execveat.attach-failed",
	}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("evidence=%q want=%q", got, want)
	}
	if strings.Contains(strings.Join(got, ","), "/sys/") {
		t.Fatalf("evidence leaked a kernel path: %q", got)
	}
	if got := collectorFailureEvidence(
		"process",
		errors.New("attach workload observer tracepoint attacker/value: /secret"),
	); len(got) != 0 {
		t.Fatalf("unknown attachment evidence=%q", got)
	}
}

type observerTestCollectors struct {
	capabilities sessionwire.ObserverCapabilities
	errs         chan error
	counters     observerDropCounters
	start        func(observerRecordSink)
	started      func()
	startContext context.Context
	stop         func()
}

func newObserverTestCollectors() *observerTestCollectors {
	unavailable := sessionwire.ObserverCapability{
		State:    workloadtypes.CoverageUnavailable,
		Reason:   "test-collector-unavailable",
		Evidence: []string{"test.fixture"},
	}
	return &observerTestCollectors{
		capabilities: sessionwire.ObserverCapabilities{
			Process: unavailable,
			File:    unavailable,
			Network: unavailable,
			DNS:     unavailable,
		},
		errs: make(chan error, 1),
	}
}

func (collectors *observerTestCollectors) Capabilities() sessionwire.ObserverCapabilities {
	return collectors.capabilities
}

func (collectors *observerTestCollectors) Start(
	ctx context.Context,
	sink observerRecordSink,
) {
	collectors.startContext = ctx
	if collectors.started != nil {
		collectors.started()
	}
	if collectors.start != nil {
		collectors.start(sink)
	}
}

func (collectors *observerTestCollectors) Errors() <-chan error {
	return collectors.errs
}

func (collectors *observerTestCollectors) Counters() (
	observerDropCounters,
	error,
) {
	return collectors.counters, nil
}

func (collectors *observerTestCollectors) Stop() error {
	if collectors.stop != nil {
		collectors.stop()
	}
	return nil
}

func (collectors *observerTestCollectors) Close() error {
	return nil
}

func observerTestActivity(
	config observerConfig,
	rawSequence uint64,
	id string,
) workloadtypes.ActivityRecord {
	at := time.Unix(1, int64(rawSequence)).UTC()
	executionID := "exec_" + strings.TrimPrefix(id, "act_")
	return workloadtypes.ActivityRecord{
		Schema:    workloadtypes.ActivityRecordSchema,
		ID:        id,
		Owner:     config.Binding.Owner,
		SessionID: config.Binding.SessionID,
		Kind:      workloadtypes.ActivityProcess,
		Operation: "exec",
		Subject: workloadtypes.ProcessSubject{
			Kind:        workloadtypes.ActivityProcess,
			ExecutionID: executionID,
			Executable:  "/usr/bin/true",
			Argv:        []string{"true"},
			GuestIdentity: workloadtypes.GuestIdentity{
				UID: 1000,
				GID: 1000,
			},
		},
		Outcome: workloadtypes.Outcome{
			Status: workloadtypes.OutcomeSucceeded,
		},
		Count:           1,
		FirstAt:         at,
		LastAt:          at,
		FirstSequence:   rawSequence,
		LastSequence:    rawSequence,
		Attribution:     workloadtypes.AttributionExact,
		CoverageID:      "cov_observeremit001",
		RedactionStatus: workloadtypes.RedactionPending,
	}
}

func observerTestExecution(
	t *testing.T,
	config observerConfig,
	pid uint32,
	execSequence, monotonicNS uint64,
) workloadtypes.Execution {
	t.Helper()
	id, err := workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner: config.Binding.Owner, SessionID: config.Binding.SessionID,
		GuestBootID:        config.Binding.GuestBootID,
		ObserverGeneration: config.Binding.ObserverGeneration,
		PID:                pid, ExecSequence: execSequence, StartedAtMonoNS: monotonicNS,
	})
	if err != nil {
		t.Fatal(err)
	}
	return workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: id,
		Owner: config.Binding.Owner, SessionID: config.Binding.SessionID,
		GuestBootID:        config.Binding.GuestBootID,
		ObserverGeneration: config.Binding.ObserverGeneration,
		PID:                pid, TID: pid, ExecSequence: execSequence,
		StartedAtMonoNS: monotonicNS,
		StartedAt:       time.Unix(1, int64(monotonicNS)).UTC(),
		Executable:      "/usr/bin/true",
		Argv:            []string{"true"},
		Cwd:             "/workspace",
		Identity: workloadtypes.GuestIdentity{
			UID: 1000, GID: 1000,
		},
	}
}

func observerTestConfig(t *testing.T) observerConfig {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	return observerConfig{
		Binding: sessionwire.ObserverBinding{
			Owner: owner, SessionID: "ses_20260729T120000Z_observer",
			EnvironmentID: "env_fixture", BackendIncarnationID: "incarnation-a",
			GuestBootID: "01234567-89ab-cdef-0123-456789abcdef",
			CgroupID:    3141, ObserverGeneration: 2,
		},
		CgroupPath:     "/sys/fs/cgroup/hideout/sessions/ses_20260729T120000Z_observer",
		ExpectedDigest: "sha256:" + strings.Repeat("a", 64),
		Heartbeat:      250 * time.Millisecond,
	}
}
