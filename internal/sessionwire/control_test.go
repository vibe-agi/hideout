package sessionwire

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestStrictControlRoundTrip(t *testing.T) {
	t.Parallel()

	original := &SupervisorStart{
		Protocol:       SupervisorProtocol,
		SessionID:      "ses_wire_test",
		TargetUser:     "developer",
		GuestWork:      "/workspace",
		Argv:           []string{"sh", "-c", "printf '\\0binary'"},
		Env:            map[string]string{"COLORTERM": "", "TERM_PROGRAM": "hideout-test"},
		Terminal:       TerminalDescriptor{Mode: TerminalPTY, Rows: 24, Columns: 80, Term: DefaultTERM},
		ExpectedBootID: "boot-id-test",
		SessionSource:  "/hideout/runtime/sessions/ses_wire_test",
	}
	payload, err := MarshalControl(original)
	if err != nil {
		t.Fatal(err)
	}
	decodedControl, err := DecodeControl(TypeSupervisorStart, payload)
	if err != nil {
		t.Fatal(err)
	}
	decoded, ok := decodedControl.(*SupervisorStart)
	if !ok {
		t.Fatalf("decoded type=%T", decodedControl)
	}
	if decoded.SessionID != original.SessionID || decoded.Terminal != original.Terminal || decoded.Argv[2] != original.Argv[2] {
		t.Fatalf("decoded=%+v, want=%+v", decoded, original)
	}
	if value, ok := decoded.Env["COLORTERM"]; !ok || value != "" {
		t.Fatalf("decoded empty environment value=(%q, %t), want present empty value", value, ok)
	}
}

func TestSupervisorStartRejectsUnsafeEnvironmentValues(t *testing.T) {
	t.Parallel()

	start := &SupervisorStart{
		Protocol:       SupervisorProtocol,
		SessionID:      "ses_wire_env_test",
		TargetUser:     "developer",
		GuestWork:      "/workspace",
		Argv:           []string{"true"},
		Env:            map[string]string{"VALUE": "contains\x00nul"},
		Terminal:       TerminalDescriptor{Mode: TerminalNone},
		ExpectedBootID: "boot-id-test",
		SessionSource:  "/hideout/runtime/sessions/ses_wire_env_test",
	}
	if err := start.Validate(); err == nil || !strings.Contains(err.Error(), "NUL-free") {
		t.Fatalf("NUL environment value error=%v", err)
	}
	start.Env["VALUE"] = strings.Repeat("x", 8193)
	if err := start.Validate(); err == nil || !strings.Contains(err.Error(), "bounded") {
		t.Fatalf("oversized environment value error=%v", err)
	}
}

func TestSupervisorProjectionReadinessRoundTripIsStrict(t *testing.T) {
	t.Parallel()
	start := &SupervisorStart{
		Protocol: SupervisorProtocol, SessionID: "ses_projection_wire", TargetUser: "developer",
		GuestWork: "/workspace", Argv: []string{"code", "."},
		Terminal: TerminalDescriptor{Mode: TerminalNone}, ExpectedBootID: "boot-id-test",
		SessionSource: "/hideout/runtime/sessions/ses_projection_wire",
		ProjectionReadiness: &SupervisorProjectionReadinessExpectation{
			EnvironmentID: "env_projection", SessionSnapshotID: "sha256:" + strings.Repeat("a", 64),
			CatalogDigest: "sha256:" + strings.Repeat("b", 64), ExpectedEntries: 3, TargetProjected: true,
		},
	}
	payload, err := MarshalControl(start)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeControl(TypeSupervisorStart, payload); err != nil {
		t.Fatal(err)
	}
	ready := &SupervisorReady{
		Protocol: SupervisorProtocol, SessionID: start.SessionID, Terminal: start.Terminal,
		ProjectionReadiness: &SupervisorProjectionReadinessReady{
			Status: "ready", EnvironmentID: "env_projection",
			SessionSnapshotID: "sha256:" + strings.Repeat("a", 64),
			CatalogDigest:     "sha256:" + strings.Repeat("b", 64),
			ExpectedEntries:   3, ObservedEntries: 3, DurationMillis: 17, TargetProjected: true,
		},
	}
	payload, err = MarshalControl(ready)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeControl(TypeSupervisorReady, payload); err != nil {
		t.Fatal(err)
	}
	ready.ProjectionReadiness.ObservedEntries = 2
	if _, err := MarshalControl(ready); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("incomplete readiness error=%v", err)
	}
	ready.ProjectionReadiness.ObservedEntries = 3
	ready.ProjectionReadiness.DurationMillis = 2001
	if _, err := MarshalControl(ready); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("unbounded readiness error=%v", err)
	}
}

func TestSupervisorActivityLifecycleRoundTripBindsExactAuthority(t *testing.T) {
	t.Parallel()

	owner, err := workloadtypes.NewDisposableOwner(
		"ses_activity_wire",
		"lima",
		"instance-default-generation-7",
	)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("c", 64)
	start := &SupervisorStart{
		Protocol:       SupervisorProtocol,
		SessionID:      "ses_activity_wire",
		TargetUser:     "developer",
		GuestWork:      "/workspace",
		Argv:           []string{"claude"},
		Terminal:       TerminalDescriptor{Mode: TerminalPTY, Rows: 24, Columns: 80, Term: DefaultTERM},
		ExpectedBootID: "01234567-89ab-cdef-0123-456789abcdef",
		SessionSource:  "/hideout/runtime/sessions/ses_activity_wire",
		Activity: &SupervisorActivityExpectation{
			Owner:                owner,
			ObserverGeneration:   4,
			ObserverHelperDigest: digest,
			ObserverStreamToken:  ObserverStreamToken{1},
		},
	}
	payload, err := MarshalControl(start)
	if err != nil {
		t.Fatal(err)
	}
	decodedStartControl, err := DecodeControl(TypeSupervisorStart, payload)
	if err != nil {
		t.Fatal(err)
	}
	decodedStart := decodedStartControl.(*SupervisorStart)
	if decodedStart.Activity == nil ||
		!decodedStart.Activity.Owner.Equal(owner) ||
		decodedStart.Activity.ObserverGeneration != 4 {
		t.Fatalf("decoded activity expectation=%+v", decodedStart.Activity)
	}

	ready := &SupervisorReady{
		Protocol:  SupervisorProtocol,
		SessionID: start.SessionID,
		Terminal:  start.Terminal,
		Activity: &SupervisorActivityReady{
			Boundary: workloadtypes.WorkloadBoundary{
				Schema:             workloadtypes.WorkloadBoundarySchema,
				Owner:              owner,
				SessionID:          start.SessionID,
				CgroupPath:         "/sys/fs/cgroup/hideout/sessions/ses_activity_wire",
				CgroupID:           3141,
				TargetUser:         start.TargetUser,
				State:              workloadtypes.BoundaryReady,
				ObserverGeneration: start.Activity.ObserverGeneration,
				GuestBootID:        start.ExpectedBootID,
				CreatedAtMonoNS:    9001,
			},
			ObserverHelperDigest: digest,
			Coverage:             supervisorCoverageFixture(),
		},
	}
	if err := ready.Activity.ValidateExpectation(start.SessionID, start.Activity); err != nil {
		t.Fatal(err)
	}
	payload, err = MarshalControl(ready)
	if err != nil {
		t.Fatal(err)
	}
	decodedReadyControl, err := DecodeControl(TypeSupervisorReady, payload)
	if err != nil {
		t.Fatal(err)
	}
	decodedReady := decodedReadyControl.(*SupervisorReady)
	if decodedReady.Activity == nil || decodedReady.Activity.Boundary.CgroupID != 3141 {
		t.Fatalf("decoded activity readiness=%+v", decodedReady.Activity)
	}

	completion := &Completion{
		Kind:             CompletionExit,
		ExitCode:         0,
		TargetCompleted:  true,
		CleanupCompleted: true,
		SessionID:        start.SessionID,
		Activity: &SupervisorActivityCompletion{
			Owner:              owner,
			SessionID:          start.SessionID,
			CgroupID:           ready.Activity.Boundary.CgroupID,
			ObserverGeneration: ready.Activity.Boundary.ObserverGeneration,
			BoundaryState:      workloadtypes.BoundaryRemoved,
			Coverage:           supervisorCoverageFixture(),
			CleanupProved:      true,
		},
	}
	if err := completion.Activity.ValidateReady(start.SessionID, ready.Activity); err != nil {
		t.Fatal(err)
	}
	payload, err = MarshalControl(completion)
	if err != nil {
		t.Fatal(err)
	}
	decodedCompletionControl, err := DecodeControl(TypeCompletion, payload)
	if err != nil {
		t.Fatal(err)
	}
	decodedCompletion := decodedCompletionControl.(*Completion)
	if decodedCompletion.Activity == nil ||
		decodedCompletion.Activity.BoundaryState != workloadtypes.BoundaryRemoved {
		t.Fatalf("decoded activity completion=%+v", decodedCompletion.Activity)
	}
}

func TestSupervisorActivityLifecycleRejectsMismatchedOrFalseProof(t *testing.T) {
	t.Parallel()

	owner, err := workloadtypes.NewDisposableOwner(
		"ses_activity_tamper",
		"lima",
		"instance-default-generation-8",
	)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:" + strings.Repeat("d", 64)
	expectation := &SupervisorActivityExpectation{
		Owner: owner, ObserverGeneration: 2, ObserverHelperDigest: digest,
		ObserverStreamToken: ObserverStreamToken{1},
	}
	ready := &SupervisorActivityReady{
		Boundary: workloadtypes.WorkloadBoundary{
			Schema: workloadtypes.WorkloadBoundarySchema, Owner: owner,
			SessionID: "ses_activity_tamper", CgroupPath: "/sys/fs/cgroup/hideout/sessions/ses_activity_tamper",
			CgroupID: 2718, TargetUser: "developer", State: workloadtypes.BoundaryReady,
			ObserverGeneration: 2, GuestBootID: "boot-a", CreatedAtMonoNS: 10,
		},
		ObserverHelperDigest: digest,
		Coverage:             supervisorCoverageFixture(),
	}
	ready.Boundary.CgroupID++
	if err := ready.ValidateExpectation("ses_activity_tamper", expectation); err != nil {
		t.Fatalf("new cgroup identity should be supplied by ready: %v", err)
	}
	ready.Boundary.ObserverGeneration++
	if err := ready.ValidateExpectation("ses_activity_tamper", expectation); err == nil {
		t.Fatal("ready accepted a different observer generation")
	}
	ready.Boundary.ObserverGeneration = expectation.ObserverGeneration
	ready.ObserverHelperDigest = "sha256:" + strings.Repeat("e", 64)
	if err := ready.ValidateExpectation("ses_activity_tamper", expectation); err == nil {
		t.Fatal("ready accepted a different observer helper digest")
	}
	ready.ObserverHelperDigest = digest
	expectation.ObserverStreamToken.Destroy()
	if err := ready.ValidateExpectation("ses_activity_tamper", expectation); !errors.Is(err, ErrObserverAuthentication) {
		t.Fatalf("missing observer stream authority error=%v want %v", err, ErrObserverAuthentication)
	}
	expectation.ObserverStreamToken = ObserverStreamToken{1}

	completion := &Completion{
		Kind: CompletionCleanupError, ExitCode: 125, TargetCompleted: true,
		CleanupCompleted: true, SessionID: "ses_activity_tamper",
		Activity: &SupervisorActivityCompletion{
			Owner: owner, SessionID: "ses_activity_tamper", CgroupID: ready.Boundary.CgroupID,
			ObserverGeneration: 2, BoundaryState: workloadtypes.BoundaryUnproved,
			Coverage: supervisorCoverageFixture(), CleanupProved: false,
		},
	}
	if err := completion.Validate(); err == nil {
		t.Fatal("completion accepted top-level cleanup without activity proof")
	}
	completion.CleanupCompleted = false
	completion.Activity.CgroupID++
	if err := completion.Activity.ValidateReady(completion.SessionID, ready); err == nil {
		t.Fatal("completion accepted cleanup for a different cgroup")
	}

	ready.Coverage[0].DroppedEventCount = 1
	if err := ready.Validate("ses_activity_tamper"); !errors.Is(err, workloadtypes.ErrFalseAvailableCoverage) {
		t.Fatalf("false Available coverage error=%v", err)
	}
}

func supervisorCoverageFixture() []SupervisorCoverageSummary {
	return []SupervisorCoverageSummary{
		{Subsystem: workloadtypes.SubsystemProcess, State: workloadtypes.CoverageAvailable, Reason: "collector-ready", Evidence: []string{"tracepoint.exec"}},
		{Subsystem: workloadtypes.SubsystemFile, State: workloadtypes.CoveragePartial, Reason: "fanotify-fallback", Evidence: []string{"fanotify"}},
		{Subsystem: workloadtypes.SubsystemNetwork, State: workloadtypes.CoverageAvailable, Reason: "collector-ready", Evidence: []string{"cgroup.connect4"}},
		{Subsystem: workloadtypes.SubsystemDNS, State: workloadtypes.CoverageUnavailable, Reason: "encrypted-dns", Evidence: []string{"encrypted-dns"}},
	}
}

func TestStrictControlRejectsUnknownAndTrailingJSON(t *testing.T) {
	t.Parallel()

	unknown := []byte(`{"rows":24,"columns":80,"ambientTheme":"dark"}`)
	if _, err := DecodeControl(TypeResize, unknown); !errors.Is(err, ErrInvalidControl) || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error=%v", err)
	}

	trailing := []byte(`{"rows":24,"columns":80} {"rows":25,"columns":81}`)
	if _, err := DecodeControl(TypeResize, trailing); !errors.Is(err, ErrInvalidControl) || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing-data error=%v", err)
	}
}

func TestStrictControlRejectsInvalidResizeAndUnsafeText(t *testing.T) {
	t.Parallel()

	invalidResize, err := json.Marshal(Resize{Rows: 0, Columns: 80})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeControl(TypeResize, invalidResize); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("resize error=%v", err)
	}

	unsafeNotice, err := json.Marshal(Notice{Code: "session.notice", Summary: "safe\x1b[2Jnot-safe"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeControl(TypeNotice, unsafeNotice); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("unsafe notice error=%v", err)
	}
}

func TestEmptyControlMustHaveNoPayload(t *testing.T) {
	t.Parallel()

	if _, err := DecodeControl(TypeCancel, nil); err != nil {
		t.Fatalf("empty cancel rejected: %v", err)
	}
	if _, err := DecodeControl(TypeCancel, []byte(`{}`)); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("non-empty cancel error=%v", err)
	}
}

func TestRunRequestMetadataRequiresOneJSONObject(t *testing.T) {
	t.Parallel()

	request := &RunRequestMetadata{
		Schema:    RunRequestSchema,
		RequestID: "req_test",
		Request:   json.RawMessage(`{"profile":"privacy","argv":["true"]}`),
	}
	if _, err := MarshalControl(request); err != nil {
		t.Fatal(err)
	}
	request.Request = json.RawMessage(`["not-an-object"]`)
	if _, err := MarshalControl(request); !errors.Is(err, ErrInvalidControl) {
		t.Fatalf("non-object request error=%v", err)
	}
}
