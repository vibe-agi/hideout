package collector_test

import (
	"errors"
	"testing"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	processcollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/process"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestProcessNormalizerAttributesForkExecExitFastChildrenAndRefork(t *testing.T) {
	boundary := processTestBoundary(t)
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	normalizer, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime: base, MonotonicNS: 1_000,
	})
	if err != nil {
		t.Fatal(err)
	}

	events := []processcollector.Event{
		processExecEvent(boundary, 100, 100, 1, 1_100, 1, 0, 0, "/usr/bin/claude", []string{"claude", "--print"}, "/workspace"),
		processForkEvent(boundary, 100, 1, 101, 1_110, 2),
		processExecEvent(boundary, 101, 101, 2, 1_120, 3, 100, 1, "/bin/sh", []string{"sh", "-c", "true"}, "/workspace"),
		processForkEvent(boundary, 101, 2, 102, 1_125, 4),
		processExitEvent(boundary, 101, 2, 1_126, 5, 0, 0),
		processExecEvent(boundary, 102, 102, 3, 1_127, 6, 101, 2, "/usr/bin/true", []string{"true"}, "/workspace/sub"),
		processExitEvent(boundary, 102, 3, 1_128, 7, 0, 0),
	}
	for _, event := range events {
		if err := normalizer.Apply(event); err != nil {
			t.Fatalf("apply %+v: %v", event, err)
		}
	}

	executions := normalizer.Executions()
	if len(executions) != 3 {
		t.Fatalf("execution count=%d want 3: %+v", len(executions), executions)
	}
	root := executionBySequence(t, executions, 1)
	child := executionBySequence(t, executions, 2)
	grandchild := executionBySequence(t, executions, 3)
	if child.ParentExecutionID != root.ID || grandchild.ParentExecutionID != child.ID {
		t.Fatalf("ancestry root=%s child=%+v grandchild=%+v", root.ID, child, grandchild)
	}
	if child.Exit == nil || child.Exit.Code == nil || *child.Exit.Code != 0 ||
		grandchild.Exit == nil || grandchild.Exit.Code == nil || *grandchild.Exit.Code != 0 {
		t.Fatalf("fast exits were not retained: child=%+v grandchild=%+v", child.Exit, grandchild.Exit)
	}
	if child.Executable != "/bin/sh" || child.Cwd != "/workspace" ||
		len(child.Argv) != 3 || child.Argv[2] != "true" {
		t.Fatalf("exec metadata=%+v", child)
	}
	for _, execution := range executions {
		if err := execution.Validate(); err != nil {
			t.Fatalf("invalid normalized execution %+v: %v", execution, err)
		}
	}
}

func TestProcessNormalizerPIDReuseCreatesDistinctExecutionIdentity(t *testing.T) {
	boundary := processTestBoundary(t)
	normalizer, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []processcollector.Event{
		processExecEvent(boundary, 4242, 4242, 11, 20, 1, 0, 0, "/bin/first", []string{"first"}, "/workspace"),
		processExitEvent(boundary, 4242, 11, 21, 2, 0, 0),
		processExecEvent(boundary, 4242, 4242, 12, 30, 3, 0, 0, "/bin/second", []string{"second"}, "/workspace"),
	} {
		if err := normalizer.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	executions := normalizer.Executions()
	if len(executions) != 2 || executions[0].ID == executions[1].ID {
		t.Fatalf("PID reuse collapsed execution identities: %+v", executions)
	}
	if executions[0].PID != executions[1].PID ||
		executions[0].ExecSequence == executions[1].ExecSequence {
		t.Fatalf("PID reuse fixture malformed: %+v", executions)
	}
}

func TestProcessNormalizerLooksUpExactExecutionAndActorWithoutLeakingState(
	t *testing.T,
) {
	boundary := processTestBoundary(t)
	normalizer, err := processcollector.NewNormalizer(
		boundary,
		processcollector.ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			MonotonicNS: 10,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := normalizer.Apply(processExecEvent(
		boundary,
		4242,
		4242,
		11,
		20,
		1,
		0,
		0,
		"/bin/first",
		[]string{"first", "--flag"},
		"/workspace",
	)); err != nil {
		t.Fatal(err)
	}
	if err := normalizer.Apply(
		processExitEvent(boundary, 4242, 11, 21, 2, 0, 0),
	); err != nil {
		t.Fatal(err)
	}

	execution, ok := normalizer.LookupExecution(4242, 11)
	if !ok ||
		execution.PID != 4242 ||
		execution.ExecSequence != 11 ||
		execution.Exit == nil ||
		execution.Exit.Code == nil ||
		*execution.Exit.Code != 0 {
		t.Fatalf("execution=%+v ok=%v", execution, ok)
	}
	actor, ok := normalizer.LookupActor(4242, 11)
	if !ok ||
		actor.ExecutionID != execution.ID ||
		actor.PID != execution.PID ||
		actor.UID != execution.Identity.UID ||
		actor.GID != execution.Identity.GID ||
		actor.User != execution.Identity.User ||
		actor.Group != execution.Identity.Group {
		t.Fatalf("actor=%+v ok=%v execution=%+v", actor, ok, execution)
	}

	execution.Argv[0] = "mutated"
	execution.Limitations = append(execution.Limitations, "mutated")
	*execution.Exit.Code = 99
	second, ok := normalizer.LookupExecution(4242, 11)
	if !ok ||
		second.Argv[0] != "first" ||
		len(second.Limitations) != 0 ||
		second.Exit == nil ||
		second.Exit.Code == nil ||
		*second.Exit.Code != 0 {
		t.Fatalf("lookup exposed internal state: %+v", second)
	}

	for _, lookup := range [][2]uint64{{4242, 12}, {4243, 11}, {0, 11}} {
		if _, ok := normalizer.LookupExecution(
			uint32(lookup[0]),
			lookup[1],
		); ok {
			t.Fatalf("unexpected execution lookup hit for pid=%d sequence=%d", lookup[0], lookup[1])
		}
		if _, ok := normalizer.LookupActor(
			uint32(lookup[0]),
			lookup[1],
		); ok {
			t.Fatalf("unexpected actor lookup hit for pid=%d sequence=%d", lookup[0], lookup[1])
		}
	}
	var nilNormalizer *processcollector.Normalizer
	if _, ok := nilNormalizer.LookupExecution(4242, 11); ok {
		t.Fatal("nil normalizer returned an execution")
	}
	if _, ok := nilNormalizer.LookupActor(4242, 11); ok {
		t.Fatal("nil normalizer returned an actor")
	}
}

func TestProcessNormalizerMaterializesDeterministicExecActivity(t *testing.T) {
	t.Parallel()

	boundary := processTestBoundary(t)
	anchor := processcollector.ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 10,
	}
	normalizer, err := processcollector.NewNormalizer(boundary, anchor)
	if err != nil {
		t.Fatal(err)
	}
	event := processExecEvent(
		boundary,
		4242,
		4242,
		11,
		20,
		7,
		0,
		0,
		"/usr/bin/claude",
		[]string{"claude", "--print"},
		"",
	)
	event.Limitations = []string{"cwd-unavailable"}
	if _, err := normalizer.ExecActivity(
		event,
		"cov_processfixture1",
	); !errors.Is(err, processcollector.ErrInvalidEvent) {
		t.Fatalf("activity before Apply error=%v", err)
	}
	if err := normalizer.Apply(event); err != nil {
		t.Fatal(err)
	}
	record, err := normalizer.ExecActivity(
		event,
		"cov_processfixture1",
	)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := normalizer.ExecActivity(
		event,
		"cov_processfixture1",
	)
	if err != nil {
		t.Fatal(err)
	}
	subject, ok := record.Subject.(workloadtypes.ProcessSubject)
	if !ok ||
		record.ID != repeated.ID ||
		record.Kind != workloadtypes.ActivityProcess ||
		record.Operation != "exec" ||
		record.Actor == nil ||
		record.Actor.ExecutionID != subject.ExecutionID ||
		subject.Executable != "/usr/bin/claude" ||
		subject.Cwd != "" ||
		len(subject.Argv) != 2 ||
		record.FirstSequence != event.Sequence ||
		record.RedactionStatus != workloadtypes.RedactionPending ||
		len(record.Truncation) != 1 ||
		record.Truncation[0] != "cwd-unavailable" {
		t.Fatalf("record=%+v subject=%+v", record, subject)
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	exit := processExitEvent(
		boundary,
		event.PID,
		event.ExecSequence,
		21,
		8,
		0,
		0,
	)
	if _, err := normalizer.ExecActivity(
		exit,
		"cov_processfixture1",
	); !errors.Is(err, processcollector.ErrInvalidEvent) {
		t.Fatalf("exit activity error=%v", err)
	}
}

func TestProcessNormalizerClosesAndChainsRepeatedExecInSamePID(t *testing.T) {
	boundary := processTestBoundary(t)
	base := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	normalizer, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime: base, MonotonicNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []processcollector.Event{
		processExecEvent(boundary, 501, 501, 1, 20, 1, 0, 0, "/bin/sh", []string{"sh"}, "/workspace"),
		processExecEvent(boundary, 501, 501, 2, 30, 2, 0, 0, "/usr/bin/env", []string{"env", "true"}, "/workspace"),
		processExitEvent(boundary, 501, 2, 40, 3, 0, 0),
	} {
		if err := normalizer.Apply(event); err != nil {
			t.Fatalf("apply %+v: %v", event, err)
		}
	}
	executions := normalizer.Executions()
	first := executionBySequence(t, executions, 1)
	second := executionBySequence(t, executions, 2)
	if first.Exit == nil || first.Exit.UnknownReason != "replaced-by-exec" ||
		first.Exit.AtMonoNS != second.StartedAtMonoNS ||
		second.ParentExecutionID != first.ID {
		t.Fatalf("exec chain first=%+v second=%+v", first, second)
	}
	if second.Exit == nil || second.Exit.Code == nil || *second.Exit.Code != 0 {
		t.Fatalf("final exit=%+v", second.Exit)
	}

	both := processExitEvent(boundary, 501, 2, 50, 4, 1, 9)
	if err := normalizer.Apply(both); !errors.Is(err, processcollector.ErrInvalidEvent) {
		t.Fatalf("code+signal error=%v want=%v", err, processcollector.ErrInvalidEvent)
	}
}

func TestProcessNormalizerPreservesInheritedExecutionAcrossForksWithoutExec(t *testing.T) {
	boundary := processTestBoundary(t)
	normalizer, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, event := range []processcollector.Event{
		processExecEvent(boundary, 100, 100, 1, 100, 1, 0, 0, "/bin/root", []string{"root"}, "/workspace"),
		processForkEvent(boundary, 100, 1, 101, 110, 2),
		processExitEvent(boundary, 100, 1, 111, 3, 0, 0),
		// PID 101 has not exec'd. The kernel event therefore names PID 100's
		// inherited execution as the parent of PID 102.
		processForkEvent(boundary, 100, 1, 102, 112, 4),
		processExecEvent(boundary, 102, 102, 2, 113, 5, 100, 1, "/bin/grandchild", nil, ""),
		// An unexec'd child has no execution sequence, but its exit must clear
		// the pending fork so a later PID reuse cannot inherit stale ancestry.
		processExitEvent(boundary, 101, 0, 114, 6, 0, 0),
		processForkEvent(boundary, 102, 2, 101, 115, 7),
		processExecEvent(boundary, 101, 101, 3, 116, 8, 102, 2, "/bin/reused", []string{"reused"}, ""),
	} {
		if err := normalizer.Apply(event); err != nil {
			t.Fatalf("apply %+v: %v", event, err)
		}
	}

	executions := normalizer.Executions()
	if len(executions) != 3 {
		t.Fatalf("execution count=%d want 3: %+v", len(executions), executions)
	}
	root := executionBySequence(t, executions, 1)
	grandchild := executionBySequence(t, executions, 2)
	reused := executionBySequence(t, executions, 3)
	if grandchild.ParentExecutionID != root.ID || reused.ParentExecutionID != grandchild.ID {
		t.Fatalf("inherited ancestry root=%s grandchild=%+v reused=%+v", root.ID, grandchild, reused)
	}
	if len(grandchild.Argv) != 0 {
		t.Fatalf("missing kernel argv was fabricated: %q", grandchild.Argv)
	}
}

func TestProcessNormalizerRejectsCrossCgroupAndUnknownExitWithoutReassignment(t *testing.T) {
	boundary := processTestBoundary(t)
	normalizer, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	wrong := processExecEvent(boundary, 99, 99, 1, 20, 1, 0, 0, "/bin/noise", []string{"noise"}, "/")
	wrong.CgroupID++
	if err := normalizer.Apply(wrong); !errors.Is(err, processcollector.ErrBoundaryMismatch) {
		t.Fatalf("wrong cgroup error=%v want %v", err, processcollector.ErrBoundaryMismatch)
	}
	orphanExit := processExitEvent(boundary, 99, 77, 21, 2, 0, 0)
	if err := normalizer.Apply(orphanExit); !errors.Is(err, processcollector.ErrExecutionUnknown) {
		t.Fatalf("unknown exit error=%v want %v", err, processcollector.ErrExecutionUnknown)
	}
	if len(normalizer.Executions()) != 0 {
		t.Fatal("cross-cgroup or unknown exit created an execution")
	}
}

func TestProcessNormalizerOrdersSequencesPerCPU(t *testing.T) {
	boundary := processTestBoundary(t)
	normalizer, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	cpuZero := processExecEvent(
		boundary, 100, 100, 1, 100, 1, 0, 0,
		"/bin/cpu-zero", []string{"cpu-zero"}, "/workspace",
	)
	cpuZero.CPU = 0
	if err := normalizer.Apply(cpuZero); err != nil {
		t.Fatal(err)
	}

	// Ring-buffer delivery may interleave CPUs, so a lower timestamp and the
	// same sequence on another CPU are both valid.
	cpuOne := processExecEvent(
		boundary, 200, 200, 2, 90, 1, 0, 0,
		"/bin/cpu-one", []string{"cpu-one"}, "/workspace",
	)
	cpuOne.CPU = 1
	if err := normalizer.Apply(cpuOne); err != nil {
		t.Fatalf("cross-CPU interleave rejected: %v", err)
	}

	duplicate := processExitEvent(boundary, 100, 1, 110, 1, 0, 0)
	duplicate.CPU = 0
	if err := normalizer.Apply(duplicate); !errors.Is(err, processcollector.ErrEventOrder) {
		t.Fatalf("same-CPU duplicate error=%v want %v", err, processcollector.ErrEventOrder)
	}
}

func TestKernelProcessRecordConversionPreservesBoundsAndLimitations(t *testing.T) {
	boundary := processTestBoundary(t)
	raw := observerbpf.RawProcessEvent{
		Kind: observerbpf.ProcessEventExec, CPU: 3,
		PID: 201, TID: 201, ParentPID: 200, UID: 1000, GID: 1001,
		Argc: 2, Flags: observerbpf.ProcessFlagArgvTruncated,
		CgroupID: boundary.CgroupID, ObserverSequence: 7,
		ExecSequence: 4, ParentExecSequence: 3, MonotonicNS: 9_000,
	}
	copy(raw.Executable[:], "/usr/bin/claude")
	copy(raw.Argv[0][:], "claude")
	copy(raw.Argv[1][:], "--print")

	event, err := processcollector.EventFromKernelRecord(boundary, raw)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != processcollector.EventExec ||
		event.CPU != 3 || event.Sequence != 7 ||
		event.Executable != "/usr/bin/claude" ||
		len(event.Argv) != 2 || event.Argv[1] != "--print" ||
		event.Identity.UID != 1000 || event.Identity.GID != 1001 {
		t.Fatalf("event=%+v", event)
	}
	wantLimitations := []string{"argv-truncated", "cwd-unavailable"}
	if len(event.Limitations) != len(wantLimitations) {
		t.Fatalf("limitations=%q want=%q", event.Limitations, wantLimitations)
	}
	for index := range wantLimitations {
		if event.Limitations[index] != wantLimitations[index] {
			t.Fatalf("limitations=%q want=%q", event.Limitations, wantLimitations)
		}
	}

	wrongBoundary := raw
	wrongBoundary.CgroupID++
	if _, err := processcollector.EventFromKernelRecord(boundary, wrongBoundary); !errors.Is(err, processcollector.ErrBoundaryMismatch) {
		t.Fatalf("wrong cgroup error=%v", err)
	}
	unavailable := raw
	unavailable.Flags = observerbpf.ProcessFlagExecutableUnavailable
	if _, err := processcollector.EventFromKernelRecord(boundary, unavailable); !errors.Is(err, processcollector.ErrKernelProcessMetadata) {
		t.Fatalf("unavailable executable error=%v", err)
	}
}

func TestKernelExitUnavailableClosesExecutionWithoutFabricatingStatus(t *testing.T) {
	boundary := processTestBoundary(t)
	normalizer, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC), MonotonicNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := normalizer.Apply(processExecEvent(
		boundary, 301, 301, 8, 20, 1, 0, 0, "/bin/test", []string{"test"}, "",
	)); err != nil {
		t.Fatal(err)
	}
	event, err := processcollector.EventFromKernelRecord(
		boundary,
		observerbpf.RawProcessEvent{
			Kind: observerbpf.ProcessEventExit, PID: 301, TID: 301,
			Flags:    observerbpf.ProcessFlagExitUnavailable,
			CgroupID: boundary.CgroupID, ObserverSequence: 2,
			ExecSequence: 8, MonotonicNS: 30,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := normalizer.Apply(event); err != nil {
		t.Fatal(err)
	}
	execution := executionBySequence(t, normalizer.Executions(), 8)
	if execution.Exit == nil || execution.Exit.Code != nil ||
		execution.Exit.Signal != 0 ||
		execution.Exit.UnknownReason != "exit-unavailable" ||
		len(execution.Limitations) != 2 ||
		execution.Limitations[0] != "cwd-unavailable" ||
		execution.Limitations[1] != "exit-unavailable" {
		t.Fatalf("execution=%+v", execution)
	}
}

func TestProcessNormalizerUsesExactParentExecutionAndRejectsMismatches(t *testing.T) {
	boundary := processTestBoundary(t)
	normalizer, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC), MonotonicNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []processcollector.Event{
		processExecEvent(boundary, 100, 100, 1, 20, 1, 0, 0, "/bin/parent-v1", nil, "/workspace"),
		processExecEvent(boundary, 100, 100, 2, 30, 2, 0, 0, "/bin/parent-v2", nil, "/workspace"),
		// The fork ring record was lost, but the kernel state retained the
		// exact execution reference from before the parent's re-exec.
		processExecEvent(boundary, 101, 101, 3, 40, 3, 100, 1, "/bin/child", nil, "/workspace"),
	} {
		if err := normalizer.Apply(event); err != nil {
			t.Fatalf("apply %+v: %v", event, err)
		}
	}
	executions := normalizer.Executions()
	parentV1 := executionBySequence(t, executions, 1)
	child := executionBySequence(t, executions, 3)
	if child.ParentExecutionID != parentV1.ID {
		t.Fatalf("child parent=%q want exact prior execution %q", child.ParentExecutionID, parentV1.ID)
	}
	unknownParent := processExecEvent(
		boundary, 102, 102, 4, 45, 4, 100, 0, "/bin/partial-child", nil, "/workspace",
	)
	unknownParent.Limitations = []string{"state-unavailable"}
	if err := normalizer.Apply(unknownParent); err != nil {
		t.Fatalf("honestly unavailable parent was rejected: %v", err)
	}
	partialChild := executionBySequence(t, normalizer.Executions(), 4)
	if partialChild.ParentExecutionID != "" ||
		len(partialChild.Limitations) != 1 ||
		partialChild.Limitations[0] != "state-unavailable" {
		t.Fatalf("partial child=%+v", partialChild)
	}

	withFork, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC), MonotonicNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := withFork.Apply(processExecEvent(
		boundary, 200, 200, 4, 50, 1, 0, 0, "/bin/root", nil, "/workspace",
	)); err != nil {
		t.Fatal(err)
	}
	if err := withFork.Apply(processForkEvent(boundary, 200, 4, 201, 60, 2)); err != nil {
		t.Fatal(err)
	}
	mismatch := processExecEvent(
		boundary, 201, 201, 5, 70, 3, 200, 99, "/bin/child", nil, "/workspace",
	)
	if err := withFork.Apply(mismatch); !errors.Is(err, processcollector.ErrInvalidEvent) {
		t.Fatalf("mismatched parent error=%v want=%v", err, processcollector.ErrInvalidEvent)
	}
	mismatch.ParentExecSequence = 4
	if err := withFork.Apply(mismatch); err != nil {
		t.Fatalf("corrected parent tuple was not accepted transactionally: %v", err)
	}
}

func TestProcessNormalizerRequiresExplicitUnknownCWD(t *testing.T) {
	boundary := processTestBoundary(t)
	normalizer, err := processcollector.NewNormalizer(boundary, processcollector.ClockAnchor{
		WallTime: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC), MonotonicNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	event := processExecEvent(
		boundary, 400, 400, 1, 20, 1, 0, 0, "/bin/test", nil, "",
	)
	event.Limitations = nil
	if err := normalizer.Apply(event); !errors.Is(err, processcollector.ErrInvalidEvent) {
		t.Fatalf("unmarked empty cwd error=%v want=%v", err, processcollector.ErrInvalidEvent)
	}
	event.Limitations = []string{"cwd-unavailable"}
	if err := normalizer.Apply(event); err != nil {
		t.Fatalf("explicit unknown cwd was rejected: %v", err)
	}
}

func processTestBoundary(t *testing.T) processcollector.Boundary {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	return processcollector.Boundary{
		Owner: owner, SessionID: "ses_20260729T120000Z_process",
		GuestBootID: "01234567-89ab-cdef-0123-456789abcdef",
		CgroupID:    3141, ObserverGeneration: 1,
	}
}

func processExecEvent(
	boundary processcollector.Boundary,
	pid, tid uint32,
	execSequence, monotonicNS, sequence uint64,
	parentPID uint32,
	parentExecSequence uint64,
	executable string,
	argv []string,
	cwd string,
) processcollector.Event {
	event := processcollector.Event{
		Kind:  processcollector.EventExec,
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		GuestBootID: boundary.GuestBootID, CgroupID: boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		PID:                pid, TID: tid, ParentPID: parentPID,
		ExecSequence: execSequence, ParentExecSequence: parentExecSequence,
		MonotonicNS: monotonicNS, Sequence: sequence,
		Executable: executable, Argv: argv, Cwd: cwd,
		Identity: workloadtypes.GuestIdentity{UID: 1000, GID: 1000, User: "developer"},
	}
	if cwd == "" {
		event.Limitations = []string{"cwd-unavailable"}
	}
	return event
}

func processForkEvent(
	boundary processcollector.Boundary,
	parentPID uint32,
	parentExecSequence uint64,
	childPID uint32,
	monotonicNS, sequence uint64,
) processcollector.Event {
	return processcollector.Event{
		Kind:  processcollector.EventFork,
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		GuestBootID: boundary.GuestBootID, CgroupID: boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		PID:                childPID, TID: childPID, ParentPID: parentPID,
		ParentExecSequence: parentExecSequence,
		MonotonicNS:        monotonicNS, Sequence: sequence,
	}
}

func processExitEvent(
	boundary processcollector.Boundary,
	pid uint32,
	execSequence, monotonicNS, sequence uint64,
	code int,
	signal uint32,
) processcollector.Event {
	return processcollector.Event{
		Kind:  processcollector.EventExit,
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		GuestBootID: boundary.GuestBootID, CgroupID: boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		PID:                pid, TID: pid, ExecSequence: execSequence,
		MonotonicNS: monotonicNS, Sequence: sequence,
		ExitCode: &code, Signal: signal,
	}
}

func executionBySequence(
	t *testing.T,
	values []workloadtypes.Execution,
	sequence uint64,
) workloadtypes.Execution {
	t.Helper()
	for _, value := range values {
		if value.ExecSequence == sequence {
			return value
		}
	}
	t.Fatalf("execution sequence %d not found in %+v", sequence, values)
	return workloadtypes.Execution{}
}
