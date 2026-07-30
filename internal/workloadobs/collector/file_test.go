package collector_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestFileNormalizerPreservesSupportedOperationsIdentityCountsAndBytes(t *testing.T) {
	boundary, actor := fileTestBoundary(t)
	normalizer, err := filecollector.NewNormalizer(boundary)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		kind        filecollector.EventKind
		operation   string
		bytes       uint64
		destructive bool
	}{
		{filecollector.EventOpen, "open", 0, false},
		{filecollector.EventRead, "read", 17, false},
		{filecollector.EventWrite, "write", 23, false},
		{filecollector.EventMmap, "mmap", 4096, false},
		{filecollector.EventCreate, "create", 0, false},
		{filecollector.EventTruncate, "truncate", 0, true},
		{filecollector.EventRename, "rename", 0, true},
		{filecollector.EventUnlink, "unlink", 0, true},
		{filecollector.EventMetadata, "metadata", 0, false},
		{filecollector.EventHardlink, "hardlink", 0, false},
		{filecollector.EventSymlink, "symlink", 0, false},
		{filecollector.EventMkdir, "mkdir", 0, false},
		{filecollector.EventRmdir, "rmdir", 0, true},
	}
	for index, testCase := range tests {
		t.Run(testCase.operation, func(t *testing.T) {
			event := filecollector.Event{
				Kind:  testCase.kind,
				Owner: boundary.Owner, SessionID: boundary.SessionID,
				CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
				Sequence: uint64(index + 1), At: at.Add(time.Duration(index) * time.Millisecond),
				Actor: actor, Path: "/workspace/input.txt",
				TargetPath: "/workspace/output.txt",
				PathState:  "resolved", PathClass: "workspace", FileType: "regular",
				Device: 8, Inode: 9001, MountID: 77, Bytes: testCase.bytes,
				Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
				CoverageID: "cov_file_fixture",
			}
			record, err := normalizer.Normalize(event)
			if err != nil {
				t.Fatal(err)
			}
			if record.Operation != testCase.operation || record.Count != 1 ||
				record.Bytes != testCase.bytes || record.Actor == nil ||
				record.Actor.ExecutionID != actor.ExecutionID {
				t.Fatalf("record=%+v", record)
			}
			subject, ok := record.Subject.(workloadtypes.FileSubject)
			if !ok {
				t.Fatalf("subject=%T want FileSubject", record.Subject)
			}
			if subject.Device != 8 || subject.Inode != 9001 || subject.MountID != 77 ||
				subject.Destructive != testCase.destructive {
				t.Fatalf("subject=%+v", subject)
			}
			if (testCase.kind == filecollector.EventRename ||
				testCase.kind == filecollector.EventHardlink ||
				testCase.kind == filecollector.EventSymlink) &&
				subject.TargetPath != event.TargetPath {
				t.Fatalf("target path was lost: %+v", subject)
			}
			if err := record.Validate(); err != nil {
				t.Fatalf("invalid file activity: %v\n%+v", err, record)
			}
			again, err := normalizer.Normalize(event)
			if err != nil || again.ID != record.ID {
				t.Fatalf("same evidence must normalize deterministically: first=%s second=%s err=%v", record.ID, again.ID, err)
			}
		})
	}
}

func TestFileNormalizerLabelsAliasesSymlinksAndPathRacesWithoutFabrication(t *testing.T) {
	boundary, actor := fileTestBoundary(t)
	normalizer, err := filecollector.NewNormalizer(boundary)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		kind        filecollector.EventKind
		path        string
		target      string
		state       string
		limitations []string
		wantState   string
		wantTarget  string
		forbidPath  string
	}{
		{
			name: "hard-link alias", kind: filecollector.EventHardlink,
			path: "/workspace/a", target: "/workspace/b", state: "aliased",
			wantState: "aliased", wantTarget: "/workspace/b",
		},
		{
			name: "symlink metadata only", kind: filecollector.EventSymlink,
			path: "/workspace/link", target: "../outside", state: "aliased",
			wantState: "aliased", wantTarget: "../outside",
		},
		{
			name: "rename path race", kind: filecollector.EventRename,
			path: "/workspace/old/../raced", target: "/workspace/new", state: "raced",
			wantState: "raced", wantTarget: "/workspace/new",
			forbidPath: "/workspace/raced",
		},
		{
			name: "unresolved deleted handle", kind: filecollector.EventUnlink,
			path: "", state: "unknown", limitations: []string{"path-unavailable"},
			wantState: "unknown",
		},
	}
	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			record, err := normalizer.Normalize(filecollector.Event{
				Kind:  testCase.kind,
				Owner: boundary.Owner, SessionID: boundary.SessionID,
				CgroupID: boundary.CgroupID, ObserverGeneration: boundary.ObserverGeneration,
				Sequence: uint64(index + 20), At: at,
				Actor: actor, Path: testCase.path, TargetPath: testCase.target,
				PathState: testCase.state, PathClass: "workspace", FileType: "regular",
				Device: 8, Inode: uint64(100 + index), MountID: 77,
				Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
				CoverageID: "cov_file_fixture", Limitations: testCase.limitations,
			})
			if err != nil {
				t.Fatal(err)
			}
			subject := record.Subject.(workloadtypes.FileSubject)
			if subject.PathState != testCase.wantState || subject.TargetPath != testCase.wantTarget {
				t.Fatalf("subject=%+v", subject)
			}
			if testCase.forbidPath != "" && subject.Path == testCase.forbidPath {
				t.Fatalf("path race was fabricated as canonical: %+v", subject)
			}
		})
	}
}

func TestFileNormalizerRejectsCrossBoundaryAndNeverRetainsContents(t *testing.T) {
	boundary, actor := fileTestBoundary(t)
	normalizer, err := filecollector.NewNormalizer(boundary)
	if err != nil {
		t.Fatal(err)
	}
	event := filecollector.Event{
		Kind:  filecollector.EventWrite,
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID: boundary.CgroupID + 1, ObserverGeneration: boundary.ObserverGeneration,
		Sequence: 1, At: time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Actor: actor, Path: "/workspace/secret.txt",
		PathState: "resolved", PathClass: "workspace", FileType: "regular",
		Device: 8, Inode: 12, MountID: 77, Bytes: 128,
		Outcome:    workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		CoverageID: "cov_file_fixture",
	}
	if _, err := normalizer.Normalize(event); !errors.Is(err, filecollector.ErrBoundaryMismatch) {
		t.Fatalf("wrong boundary error=%v want %v", err, filecollector.ErrBoundaryMismatch)
	}
	event.CgroupID = boundary.CgroupID
	record, err := normalizer.Normalize(event)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "file-content-canary") {
		t.Fatalf("file contents appeared in normalized metadata: %s", encoded)
	}
}

func TestKernelFileRecordConvertsExactActorPathBytesAndOutcome(t *testing.T) {
	boundary, actor := fileTestBoundary(t)
	anchor := filecollector.ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 1_000,
	}
	raw := observerbpf.RawFileEvent{
		Kind: observerbpf.FileEventWrite, CPU: 3,
		PID: 43, TID: 43, ExecutionPID: 42,
		UID: 1000, GID: 1001, FileType: observerbpf.FileTypeRegular,
		Result: 23, CgroupID: boundary.CgroupID, ObserverSequence: 9,
		ExecSequence: 1, MonotonicNS: 1_500, Bytes: 23,
		Device: 8, Inode: 9001, MountID: 77,
	}
	copy(raw.Path[:], "/workspace/project/main.go")
	event, err := filecollector.EventFromKernelRecord(
		boundary,
		anchor,
		"cov_file_fixture",
		raw,
		func(pid uint32, sequence uint64) (string, bool) {
			return actor.ExecutionID, pid == 42 && sequence == 1
		},
		filecollector.NewPathClassifier([]string{"/workspace"}, nil, nil, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != filecollector.EventWrite ||
		event.Actor.ExecutionID != actor.ExecutionID ||
		event.Actor.PID != 43 ||
		event.Path != "/workspace/project/main.go" ||
		event.PathState != "resolved" || event.PathClass != "workspace" ||
		event.Bytes != 23 ||
		event.Outcome.Status != workloadtypes.OutcomeSucceeded ||
		event.At != anchor.WallTime.Add(500*time.Nanosecond) {
		t.Fatalf("event=%+v", event)
	}
	normalizer, err := filecollector.NewNormalizer(boundary)
	if err != nil {
		t.Fatal(err)
	}
	record, err := normalizer.Normalize(event)
	if err != nil {
		t.Fatal(err)
	}
	if record.Bytes != 23 || record.Attribution != workloadtypes.AttributionExact {
		t.Fatalf("record=%+v", record)
	}
}

func TestKernelFileRecordPreservesRenameComponentsAndUnknownOutcome(t *testing.T) {
	boundary, actor := fileTestBoundary(t)
	raw := observerbpf.RawFileEvent{
		Kind: observerbpf.FileEventRename,
		PID:  42, TID: 42, ExecutionPID: 42,
		UID: 1000, GID: 1000,
		Flags: observerbpf.FileFlagOutcomeUnknown |
			observerbpf.FileFlagAuthorizationHook,
		FileType: observerbpf.FileTypeRegular,
		CgroupID: boundary.CgroupID, ObserverSequence: 2,
		ExecSequence: 1, MonotonicNS: 2_000,
		Device: 8, Inode: 99,
	}
	copy(raw.Path[:], "/workspace/old")
	copy(raw.PathName[:], "name.txt")
	copy(raw.TargetPath[:], "/workspace/new")
	copy(raw.TargetName[:], "renamed.txt")
	event, err := filecollector.EventFromKernelRecord(
		boundary,
		filecollector.ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			MonotonicNS: 1_000,
		},
		"cov_file_fixture",
		raw,
		func(uint32, uint64) (string, bool) { return actor.ExecutionID, true },
		filecollector.NewPathClassifier([]string{"/workspace"}, nil, nil, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.Path != "/workspace/old/name.txt" ||
		event.TargetPath != "/workspace/new/renamed.txt" ||
		event.Outcome.Status != workloadtypes.OutcomeUnknown ||
		len(event.Limitations) != 1 ||
		event.Limitations[0] != "outcome-unavailable" {
		t.Fatalf("event=%+v", event)
	}
	normalizer, _ := filecollector.NewNormalizer(boundary)
	record, err := normalizer.Normalize(event)
	if err != nil {
		t.Fatal(err)
	}
	subject := record.Subject.(workloadtypes.FileSubject)
	if !subject.Destructive || subject.TargetPath != event.TargetPath {
		t.Fatalf("subject=%+v", subject)
	}
}

func TestKernelFileRecordLabelsUnavailableEvidenceWithoutFabrication(t *testing.T) {
	boundary, _ := fileTestBoundary(t)
	raw := observerbpf.RawFileEvent{
		Kind: observerbpf.FileEventMmap,
		PID:  42, TID: 42,
		Flags: observerbpf.FileFlagPathUnavailable |
			observerbpf.FileFlagIdentityUnavailable |
			observerbpf.FileFlagBytesUnavailable |
			observerbpf.FileFlagStateUnavailable |
			observerbpf.FileFlagOutcomeUnknown |
			observerbpf.FileFlagAuthorizationHook,
		FileType: observerbpf.FileTypeUnknown,
		CgroupID: boundary.CgroupID, ObserverSequence: 3,
		MonotonicNS: 2_000,
	}
	event, err := filecollector.EventFromKernelRecord(
		boundary,
		filecollector.ClockAnchor{
			WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
			MonotonicNS: 1_000,
		},
		"cov_file_fixture",
		raw,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.Path != "" || event.PathState != "unknown" ||
		event.Attribution != workloadtypes.AttributionUnknown ||
		event.Actor != (workloadtypes.Actor{}) ||
		event.Bytes != 0 ||
		event.Outcome.Status != workloadtypes.OutcomeUnknown {
		t.Fatalf("event=%+v", event)
	}
	normalizer, _ := filecollector.NewNormalizer(boundary)
	record, err := normalizer.Normalize(event)
	if err != nil {
		t.Fatal(err)
	}
	if record.Actor != nil || record.Attribution != workloadtypes.AttributionUnknown {
		t.Fatalf("record=%+v", record)
	}
}

func TestKernelFileRecordRejectsHiddenTailAndCrossBoundary(t *testing.T) {
	boundary, actor := fileTestBoundary(t)
	raw := observerbpf.RawFileEvent{
		Kind: observerbpf.FileEventOpen,
		PID:  42, TID: 42, ExecutionPID: 42,
		FileType: observerbpf.FileTypeRegular,
		CgroupID: boundary.CgroupID, ObserverSequence: 1,
		ExecSequence: 1, MonotonicNS: 2_000,
		Device: 8, Inode: 9,
	}
	copy(raw.Path[:], "/workspace/file")
	raw.Path[len("/workspace/file")+1] = 'x'
	convert := func(value observerbpf.RawFileEvent) error {
		_, err := filecollector.EventFromKernelRecord(
			boundary,
			filecollector.ClockAnchor{
				WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
				MonotonicNS: 1_000,
			},
			"cov_file_fixture",
			value,
			func(uint32, uint64) (string, bool) { return actor.ExecutionID, true },
			nil,
		)
		return err
	}
	if err := convert(raw); !errors.Is(err, filecollector.ErrKernelFileMetadata) {
		t.Fatalf("hidden tail error=%v", err)
	}
	raw.Path[len("/workspace/file")+1] = 0
	raw.CgroupID++
	if err := convert(raw); !errors.Is(err, filecollector.ErrBoundaryMismatch) {
		t.Fatalf("cross-boundary error=%v", err)
	}

	classify := filecollector.NewPathClassifier([]string{"/workspace"}, nil, nil, nil)
	if got := classify("/workspace-other/file"); got != "external" {
		t.Fatalf("component-unsafe classification=%q", got)
	}
}

func TestKernelFileRecordRejectsImpossibleOutcomeAndUnexplainedEmptyPath(t *testing.T) {
	boundary, actor := fileTestBoundary(t)
	base := observerbpf.RawFileEvent{
		Kind: observerbpf.FileEventMmap,
		PID:  42, TID: 42, ExecutionPID: 42,
		FileType: observerbpf.FileTypeRegular,
		CgroupID: boundary.CgroupID, ObserverSequence: 1,
		ExecSequence: 1, MonotonicNS: 2_000,
		Bytes: 4096, Device: 8, Inode: 9,
	}
	copy(base.Path[:], "/workspace/file")
	convert := func(value observerbpf.RawFileEvent) error {
		_, err := filecollector.EventFromKernelRecord(
			boundary,
			filecollector.ClockAnchor{
				WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
				MonotonicNS: 1_000,
			},
			"cov_file_fixture",
			value,
			func(uint32, uint64) (string, bool) { return actor.ExecutionID, true },
			nil,
		)
		return err
	}

	positiveMmap := base
	positiveMmap.Result = 1
	if err := convert(positiveMmap); !errors.Is(err, filecollector.ErrKernelFileMetadata) {
		t.Fatalf("positive mmap result error=%v", err)
	}

	emptyPath := base
	emptyPath.Path = (observerbpf.RawFileEvent{}).Path
	if err := convert(emptyPath); !errors.Is(err, filecollector.ErrKernelFileMetadata) {
		t.Fatalf("unexplained empty path error=%v", err)
	}
}

func TestKernelFileRecordDistinguishesDirectoryCreateAndRemove(t *testing.T) {
	boundary, actor := fileTestBoundary(t)
	tests := []struct {
		name      string
		kind      uint32
		flags     uint32
		inode     uint64
		wantKind  filecollector.EventKind
		operation string
	}{
		{
			name: "mkdir", kind: observerbpf.FileEventMkdir,
			flags: observerbpf.FileFlagIdentityUnavailable |
				observerbpf.FileFlagOutcomeUnknown |
				observerbpf.FileFlagAuthorizationHook,
			wantKind: filecollector.EventMkdir, operation: "mkdir",
		},
		{
			name: "rmdir", kind: observerbpf.FileEventRmdir,
			flags: observerbpf.FileFlagOutcomeUnknown |
				observerbpf.FileFlagAuthorizationHook,
			inode: 99, wantKind: filecollector.EventRmdir, operation: "rmdir",
		},
	}
	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			raw := observerbpf.RawFileEvent{
				Kind: testCase.kind,
				PID:  42, TID: 42, ExecutionPID: 42,
				UID: 1000, GID: 1000, Flags: testCase.flags,
				FileType: observerbpf.FileTypeDirectory,
				CgroupID: boundary.CgroupID, ObserverSequence: uint64(index + 1),
				ExecSequence: 1, MonotonicNS: uint64(2_000 + index),
				Device: 8, Inode: testCase.inode,
			}
			if testCase.inode == 0 {
				raw.Device = 0
			}
			copy(raw.Path[:], "/workspace")
			copy(raw.PathName[:], "directory")
			event, err := filecollector.EventFromKernelRecord(
				boundary,
				filecollector.ClockAnchor{
					WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
					MonotonicNS: 1_000,
				},
				"cov_file_fixture",
				raw,
				func(uint32, uint64) (string, bool) { return actor.ExecutionID, true },
				filecollector.NewPathClassifier([]string{"/workspace"}, nil, nil, nil),
			)
			if err != nil {
				t.Fatal(err)
			}
			if event.Kind != testCase.wantKind ||
				event.Path != "/workspace/directory" ||
				event.FileType != "directory" {
				t.Fatalf("event=%+v", event)
			}
			normalizer, _ := filecollector.NewNormalizer(boundary)
			record, err := normalizer.Normalize(event)
			if err != nil {
				t.Fatal(err)
			}
			if record.Operation != testCase.operation {
				t.Fatalf("record=%+v", record)
			}
		})
	}
}

func TestKernelFileRecordKeepsPartialTargetAndDirectoryTypeOnEvidenceLoss(t *testing.T) {
	boundary, actor := fileTestBoundary(t)
	anchor := filecollector.ClockAnchor{
		WallTime:    time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		MonotonicNS: 1_000,
	}
	lookup := func(uint32, uint64) (string, bool) {
		return actor.ExecutionID, true
	}

	rename := observerbpf.RawFileEvent{
		Kind: observerbpf.FileEventRename,
		PID:  42, TID: 42, ExecutionPID: 42,
		Flags: observerbpf.FileFlagTargetUnavailable |
			observerbpf.FileFlagOutcomeUnknown |
			observerbpf.FileFlagAuthorizationHook,
		FileType: observerbpf.FileTypeRegular,
		CgroupID: boundary.CgroupID, ObserverSequence: 1,
		ExecSequence: 1, MonotonicNS: 2_000,
		Device: 8, Inode: 99,
	}
	copy(rename.Path[:], "/workspace")
	copy(rename.PathName[:], "old.txt")
	copy(rename.TargetName[:], "new.txt")
	event, err := filecollector.EventFromKernelRecord(
		boundary, anchor, "cov_file_fixture", rename, lookup, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.TargetPath != "new.txt" ||
		!slices.Contains(event.Limitations, "target-path-unavailable") {
		t.Fatalf("partial rename target was lost: %+v", event)
	}

	rmdir := observerbpf.RawFileEvent{
		Kind: observerbpf.FileEventRmdir,
		PID:  42, TID: 42, ExecutionPID: 42,
		Flags: observerbpf.FileFlagIdentityUnavailable |
			observerbpf.FileFlagOutcomeUnknown |
			observerbpf.FileFlagAuthorizationHook,
		FileType: observerbpf.FileTypeDirectory,
		CgroupID: boundary.CgroupID, ObserverSequence: 2,
		ExecSequence: 1, MonotonicNS: 2_001,
	}
	copy(rmdir.Path[:], "/workspace")
	copy(rmdir.PathName[:], "directory")
	event, err = filecollector.EventFromKernelRecord(
		boundary, anchor, "cov_file_fixture", rmdir, lookup, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != filecollector.EventRmdir ||
		event.FileType != "directory" ||
		!slices.Contains(event.Limitations, "identity-unavailable") {
		t.Fatalf("partial rmdir identity was lost: %+v", event)
	}
}

func fileTestBoundary(t *testing.T) (filecollector.Boundary, workloadtypes.Actor) {
	t.Helper()
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "ses_20260729T120000Z_file"
	executionID, err := workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner: owner, SessionID: sessionID,
		GuestBootID:        "01234567-89ab-cdef-0123-456789abcdef",
		ObserverGeneration: 1, PID: 42, ExecSequence: 1, StartedAtMonoNS: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return filecollector.Boundary{
		Owner: owner, SessionID: sessionID, CgroupID: 3141, ObserverGeneration: 1,
	}, workloadtypes.Actor{ExecutionID: executionID, PID: 42, UID: 1000, GID: 1000, User: "developer"}
}
