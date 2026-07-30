//go:build linux

package fanotify

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
	"golang.org/x/sys/unix"
)

func TestDecodeMetadataBatchRejectsMalformedAndDefersUnsupportedMasks(t *testing.T) {
	valid := fanotifyMetadataFixture(MaskOpen, 7, 42)
	records, err := decodeMetadataBatch(valid)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 ||
		records[0].Mask != MaskOpen ||
		records[0].FD != 7 ||
		records[0].PID != 42 {
		t.Fatalf("records=%+v", records)
	}
	overflow := fanotifyMetadataFixture(
		MaskQueueOverflow,
		unix.FAN_NOFD,
		0,
	)
	records, err = decodeMetadataBatch(append(valid, overflow...))
	if err != nil || len(records) != 2 || !records[1].Overflow {
		t.Fatalf("overflow records=%+v err=%v", records, err)
	}
	unknown := fanotifyMetadataFixture(1<<63, 8, 43)
	records, err = decodeMetadataBatch(unknown)
	if err != nil ||
		len(records) != 1 ||
		records[0].Mask != 1<<63 ||
		records[0].PID != 43 {
		t.Fatalf("unknown-mask records=%+v err=%v", records, err)
	}

	for name, mutate := range map[string]func([]byte){
		"short": func(value []byte) {
			binary.NativeEndian.PutUint32(value[0:4], metadataSize-1)
		},
		"version": func(value []byte) {
			value[4] = fanotifyMetadataVersion + 1
		},
		"reserved": func(value []byte) {
			value[5] = 1
		},
		"metadata length": func(value []byte) {
			binary.NativeEndian.PutUint16(value[6:8], uint16(metadataSize-1))
		},
		"ordinary no fd": func(value []byte) {
			binary.NativeEndian.PutUint32(value[16:20], ^uint32(0))
		},
		"ordinary no pid": func(value []byte) {
			binary.NativeEndian.PutUint32(value[20:24], 0)
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := append([]byte(nil), valid...)
			mutate(fixture)
			if _, err := decodeMetadataBatch(fixture); !errors.Is(err, ErrMetadata) {
				t.Fatalf("error=%v want=%v", err, ErrMetadata)
			}
		})
	}
	if _, err := decodeMetadataBatch(valid[:len(valid)-1]); !errors.Is(err, ErrMetadata) {
		t.Fatalf("trailing short error=%v want=%v", err, ErrMetadata)
	}
}

func TestMaterializeFiltersBeforeMaskValidationAndClosesEveryFD(t *testing.T) {
	now := func() time.Time {
		return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	}
	openFD := func() int {
		t.Helper()
		fd, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		return fd
	}
	assertClosed := func(fd int) {
		t.Helper()
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); !errors.Is(err, unix.EBADF) {
			t.Fatalf("fd %d remains open: %v", fd, err)
		}
	}

	unrelatedFD := openFD()
	unrelated := &Source{
		fd: -1, wakeFD: -1,
		matchPID: func(uint32) (bool, error) { return false, nil },
		now:      now,
	}
	observations, err := unrelated.materialize(metadataRecord{
		Mask: 1 << 63, FD: int32(unrelatedFD), PID: 42,
	})
	if err != nil || len(observations) != 0 {
		t.Fatalf("unrelated observations=%+v err=%v", observations, err)
	}
	assertClosed(unrelatedFD)
	if counters := unrelated.Counters(); counters.Filtered != 1 ||
		counters.UnsupportedMasks != 0 {
		t.Fatalf("unrelated counters=%+v", counters)
	}

	targetFD := openFD()
	target := &Source{
		fd: -1, wakeFD: -1,
		matchPID: func(uint32) (bool, error) { return true, nil },
		now:      now,
	}
	observations, err = target.materialize(metadataRecord{
		Mask: 1 << 63, FD: int32(targetFD), PID: 42,
	})
	if err != nil ||
		len(observations) != 1 ||
		observations[0].Loss == nil ||
		observations[0].Loss.Kind != LossUnsupportedMask ||
		observations[0].Loss.DroppedLowerBound != 1 {
		t.Fatalf("target observations=%+v err=%v", observations, err)
	}
	assertClosed(targetFD)

	filterFD := openFD()
	filterFailure := &Source{
		fd: -1, wakeFD: -1,
		matchPID: func(uint32) (bool, error) {
			return false, ErrMembershipFilter
		},
		now: now,
	}
	observations, err = filterFailure.materialize(metadataRecord{
		Mask: MaskOpen, FD: int32(filterFD), PID: 42,
	})
	if err != nil ||
		len(observations) != 1 ||
		observations[0].Loss == nil ||
		observations[0].Loss.Kind != LossFilterUnresolved ||
		observations[0].Loss.DroppedLowerBound != 0 {
		t.Fatalf("filter observations=%+v err=%v", observations, err)
	}
	assertClosed(filterFD)

	overflow := &Source{fd: -1, wakeFD: -1, now: now}
	observations, err = overflow.materialize(metadataRecord{
		Mask: MaskQueueOverflow, FD: unix.FAN_NOFD, Overflow: true,
	})
	if err != nil ||
		len(observations) != 1 ||
		observations[0].Loss == nil ||
		observations[0].Loss.Kind != LossQueueOverflow ||
		observations[0].Loss.DroppedLowerBound != 0 {
		t.Fatalf("overflow observations=%+v err=%v", observations, err)
	}

	firstFD := openFD()
	secondFD := openFD()
	invalidActor := &Source{
		fd: -1, wakeFD: -1,
		matchPID: func(uint32) (bool, error) { return true, nil },
		resolveActor: func(pid uint32) (workloadtypes.Actor, bool) {
			return workloadtypes.Actor{PID: pid}, true
		},
		now: now,
	}
	_, err = invalidActor.materializeRecords([]metadataRecord{
		{Mask: MaskOpen, FD: int32(firstFD), PID: 42},
		{Mask: MaskOpen, FD: int32(secondFD), PID: 42},
	})
	if !errors.Is(err, ErrActorResolution) {
		t.Fatalf("batch error=%v want=%v", err, ErrActorResolution)
	}
	assertClosed(firstFD)
	assertClosed(secondFD)
}

func TestSourcePreservesValidPrefixBeforeMalformedMetadata(t *testing.T) {
	var pipeFDs [2]int
	if err := unix.Pipe2(
		pipeFDs[:],
		unix.O_CLOEXEC|unix.O_NONBLOCK,
	); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pipeFDs[1])
	wakeFD, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		unix.Close(pipeFDs[0])
		t.Fatal(err)
	}
	source := &Source{
		fd: pipeFDs[0], wakeFD: wakeFD,
		closeDone: make(chan struct{}),
		matchPID:  func(uint32) (bool, error) { return true, nil },
		now: func() time.Time {
			return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
		},
	}
	defer source.Close()
	source.SetDeadline(time.Now().Add(time.Second))

	eventFD, err := unix.Open("/dev/null", unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	valid := fanotifyMetadataFixture(MaskOpen, int32(eventFD), 42)
	batch := append(append([]byte(nil), valid...), valid[:1]...)
	if _, err := unix.Write(pipeFDs[1], batch); err != nil {
		unix.Close(eventFD)
		t.Fatal(err)
	}

	observation, err := source.Read()
	if err != nil ||
		observation.File == nil ||
		observation.File.Kind != filecollector.EventOpen ||
		observation.Loss != nil {
		t.Fatalf("prefix observation=%+v err=%v", observation, err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(eventFD, &stat); !errors.Is(err, unix.EBADF) {
		t.Fatalf("event fd remains open: %v", err)
	}
	observation, err = source.Read()
	if err != nil ||
		observation.File != nil ||
		observation.Loss == nil ||
		observation.Loss.Kind != LossDecodeFailure ||
		observation.Loss.DroppedLowerBound != 0 {
		t.Fatalf("decode observation=%+v err=%v", observation, err)
	}
	if counters := source.Counters(); counters.Received != 1 ||
		counters.DecodeFailures != 1 ||
		counters.Emitted != 2 {
		t.Fatalf("counters=%+v", counters)
	}
}

func TestSourceDeadlineAndCloseWakeBlockedRead(t *testing.T) {
	dataFD, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	wakeFD, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		unix.Close(dataFD)
		t.Fatal(err)
	}
	source := &Source{
		fd: dataFD, wakeFD: wakeFD,
		closeDone: make(chan struct{}),
		matchPID:  func(uint32) (bool, error) { return true, nil },
		now:       time.Now,
	}
	defer source.Close()

	readResult := make(chan error, 1)
	go func() {
		_, readErr := source.Read()
		readResult <- readErr
	}()
	time.Sleep(20 * time.Millisecond)
	source.SetDeadline(time.Now().Add(20 * time.Millisecond))
	select {
	case readErr := <-readResult:
		if !errors.Is(readErr, os.ErrDeadlineExceeded) {
			t.Fatalf("deadline read error=%v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("setting a deadline did not wake the blocked read")
	}

	source.SetDeadline(time.Time{})
	go func() {
		_, readErr := source.Read()
		readResult <- readErr
	}()
	time.Sleep(20 * time.Millisecond)
	closeResult := make(chan error, 1)
	go func() {
		closeResult <- source.Close()
	}()
	select {
	case readErr := <-readResult:
		if !errors.Is(readErr, ErrSourceClosed) {
			t.Fatalf("closed read error=%v", readErr)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wake the blocked read")
	}
	select {
	case closeErr := <-closeResult:
		if closeErr != nil {
			t.Fatalf("close error=%v", closeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not finish")
	}
	source.pending = []Observation{{Loss: &Loss{
		Kind: LossDecodeFailure, Sequence: 99, At: time.Now(),
	}}}
	if _, readErr := source.Read(); !errors.Is(readErr, ErrSourceClosed) {
		t.Fatalf("read after close error=%v", readErr)
	}
}

func TestResolveFanotifyFileReportsDeletedPathRace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deleted.txt")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	resolved, pathOK, identityOK := resolveFanotifyFile(int(file.Fd()))
	if !pathOK ||
		!identityOK ||
		resolved.path != path ||
		resolved.pathState != "raced" ||
		resolved.fileType != "regular" ||
		resolved.inode == 0 {
		t.Fatalf(
			"resolved=%+v pathOK=%t identityOK=%t",
			resolved,
			pathOK,
			identityOK,
		)
	}
}

func TestOpenSourceRejectsUnboundedConfiguration(t *testing.T) {
	if _, err := OpenSource(SourceConfig{}); !errors.Is(err, ErrSourceConfig) {
		t.Fatalf("empty config error=%v want=%v", err, ErrSourceConfig)
	}
	if _, err := OpenSource(SourceConfig{
		MountPaths: []string{"relative"},
		MatchPID:   func(uint32) (bool, error) { return true, nil },
	}); !errors.Is(err, ErrSourceConfig) {
		t.Fatalf("relative mount config error=%v want=%v", err, ErrSourceConfig)
	}
}

func TestCgroupMatcherUsesExactComponentBoundariesAndRejectsAmbiguity(t *testing.T) {
	procRoot := t.TempDir()
	writeCgroup := func(pid string, value string) {
		t.Helper()
		pidRoot := filepath.Join(procRoot, pid)
		if err := os.MkdirAll(pidRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(pidRoot, "cgroup"),
			[]byte(value),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	writeCgroup("42", "0::/hideout/session-a\n")
	writeCgroup("43", "0::/hideout/session-a/child\n")
	writeCgroup("44", "0::/hideout/session-ab\n")
	writeCgroup("45", "malformed\n")

	matcher, err := newCgroupMatcher(
		procRoot,
		"/hideout/session-a",
	)
	if err != nil {
		t.Fatal(err)
	}
	for pid, want := range map[uint32]bool{42: true, 43: true, 44: false} {
		got, err := matcher.Match(pid)
		if err != nil || got != want {
			t.Fatalf("pid=%d got=%t want=%t err=%v", pid, got, want, err)
		}
	}
	for _, pid := range []uint32{45, 46, 0, 4194305} {
		if _, err := matcher.Match(pid); !errors.Is(err, ErrMembershipFilter) {
			t.Fatalf("pid=%d error=%v want=%v", pid, err, ErrMembershipFilter)
		}
	}
	for _, target := range []string{"", "/", "relative", "/hideout/../neighbor"} {
		if _, err := newCgroupMatcher(procRoot, target); !errors.Is(err, ErrSourceConfig) {
			t.Fatalf("target=%q error=%v want=%v", target, err, ErrSourceConfig)
		}
	}
}

func TestSourceRealKernel(t *testing.T) {
	if os.Getenv("HIDEOUT_TEST_FANOTIFY") != "1" {
		t.Skip("set HIDEOUT_TEST_FANOTIFY=1 in a privileged Linux test guest")
	}
	workdir := t.TempDir()
	source, err := OpenSource(SourceConfig{
		MountPaths: []string{workdir},
		MatchPID: func(pid uint32) (bool, error) {
			return pid == uint32(os.Getpid()), nil
		},
		ResolveActor: func(pid uint32) (workloadtypes.Actor, bool) {
			return workloadtypes.Actor{
				ExecutionID: "exec_fanotify_fixture",
				PID:         pid,
				UID:         uint32(os.Getuid()),
				GID:         uint32(os.Getgid()),
			}, true
		},
		Now: func() time.Time {
			return time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	source.SetDeadline(time.Now().Add(5 * time.Second))
	boundary, _ := fanotifyTestBoundary(t)
	normalizer, err := NewNormalizer(
		boundary,
		"cov_fanotify_fixture",
		filecollector.NewPathClassifier([]string{workdir}, nil, nil, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	fileNormalizer, err := filecollector.NewNormalizer(boundary)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(workdir, "observed.txt")
	if err := os.WriteFile(path, []byte("fanotify-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	seen := map[filecollector.EventKind]bool{
		filecollector.EventOpen:  false,
		filecollector.EventWrite: false,
	}
	for !seen[filecollector.EventOpen] || !seen[filecollector.EventWrite] {
		observation, err := source.Read()
		if err != nil {
			t.Fatalf("read observed=%v: %v", seen, err)
		}
		if observation.Loss != nil {
			continue
		}
		if observation.File.Path == path {
			seen[observation.File.Kind] = true
			if !observation.File.ActorResolved ||
				observation.File.Inode == 0 ||
				observation.File.PathState != "resolved" {
				t.Fatalf("observation=%+v", observation)
			}
			event, err := normalizer.Normalize(*observation.File)
			if err != nil {
				t.Fatal(err)
			}
			record, err := fileNormalizer.Normalize(event)
			if err != nil {
				t.Fatal(err)
			}
			if record.Attribution != workloadtypes.AttributionInferred ||
				record.Actor == nil ||
				!slices.Contains(record.Truncation, "fanotify-merged") ||
				!slices.Contains(record.Truncation, "mmap-unavailable") {
				t.Fatalf("record=%+v", record)
			}
		}
	}
	counters := source.Counters()
	if counters.Received == 0 ||
		counters.Emitted == 0 ||
		counters.QueueOverflows != 0 ||
		counters.FilterFailures != 0 ||
		counters.DecodeFailures != 0 {
		t.Fatalf("counters=%+v", counters)
	}
}

func fanotifyMetadataFixture(mask uint64, fd, pid int32) []byte {
	value := make([]byte, metadataSize)
	binary.NativeEndian.PutUint32(value[0:4], metadataSize)
	value[4] = fanotifyMetadataVersion
	binary.NativeEndian.PutUint16(value[6:8], uint16(metadataSize))
	binary.NativeEndian.PutUint64(value[8:16], mask)
	binary.NativeEndian.PutUint32(value[16:20], uint32(fd))
	binary.NativeEndian.PutUint32(value[20:24], uint32(pid))
	return value
}
