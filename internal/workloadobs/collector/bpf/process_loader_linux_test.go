//go:build linux

package bpf

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
	"golang.org/x/sys/unix"
)

func TestOpenProcessEventReaderRejectsZeroCgroup(t *testing.T) {
	if _, err := OpenProcessEventReader(0); !errors.Is(err, ErrProcessCollectorTarget) {
		t.Fatalf("error=%v want=%v", err, ErrProcessCollectorTarget)
	}
}

func TestOpenFileEventReaderRejectsInvalidBoundaryState(t *testing.T) {
	if _, err := OpenFileEventReader(0, nil); !errors.Is(err, ErrFileCollectorTarget) {
		t.Fatalf("zero target error=%v want=%v", err, ErrFileCollectorTarget)
	}
	if _, err := OpenFileEventReader(1, nil); !errors.Is(err, ErrFileCollectorProcessState) {
		t.Fatalf("missing process state error=%v want=%v", err, ErrFileCollectorProcessState)
	}
}

func TestProcessEventReaderRealKernel(t *testing.T) {
	if os.Getenv("HIDEOUT_TEST_BPF_ATTACH") != "1" {
		t.Skip("set HIDEOUT_TEST_BPF_ATTACH=1 in a privileged Linux test guest")
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		t.Fatalf("remove memlock limit: %v", err)
	}
	cgroup, cgroupID := newIsolatedTestCgroup(t)

	reader, err := OpenProcessEventReader(cgroupID)
	if err != nil {
		var verifier *ebpf.VerifierError
		if errors.As(err, &verifier) {
			t.Logf("verifier:\n%+v", verifier)
		}
		t.Fatalf("%+v", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	if hooks := reader.AttachedHooks(); len(hooks) != 5 {
		t.Fatalf("hooks=%q", hooks)
	}
	var configuredCgroupID uint64
	if err := reader.objects.TargetCgroupId.Get(&configuredCgroupID); err != nil {
		t.Fatal(err)
	}
	if configuredCgroupID != cgroupID {
		t.Fatalf("configured cgroup id=%d want=%d", configuredCgroupID, cgroupID)
	}
	reader.SetDeadline(time.Now().Add(5 * time.Second))
	type readResult struct {
		event RawProcessEvent
		err   error
	}
	results := make(chan readResult, 8)
	go func() {
		for {
			event, readErr := reader.ReadProcessEvent()
			results <- readResult{event: event, err: readErr}
			if readErr != nil {
				return
			}
		}
	}()
	// Ensure the consumer is polling before the first process event. This is
	// also the helper's production ordering: reader first, target second.
	time.Sleep(10 * time.Millisecond)

	command := exec.Command(os.Args[0], "-test.run=^TestProcessExecveatKernelHelper$")
	command.Env = append(os.Environ(), "HIDEOUT_EXECVEAT_HELPER=1")
	command.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    int(cgroup.Fd()),
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	namespacePID := uint32(command.Process.Pid)
	if err := command.Wait(); err != nil {
		t.Fatal(err)
	}
	t.Logf(
		"ring available=%d buffer=%d",
		reader.reader.AvailableBytes(),
		reader.reader.BufferSize(),
	)

	var sawExec, sawExit bool
	var kernelPID uint32
	var execSequence uint64
	for !(sawExec && sawExit) {
		result := <-results
		if result.err != nil {
			if counters, counterErr := reader.Counters(); counterErr == nil {
				t.Logf("process counters=%+v", counters)
			}
			t.Fatalf("read namespace-pid=%d exec=%t exit=%t: %v", namespacePID, sawExec, sawExit, result.err)
		}
		event := result.event
		switch event.Kind {
		case ProcessEventExec:
			if string(bytes.TrimRight(event.Executable[:], "\x00")) != "/bin/true" {
				continue
			}
			if event.Argc != 1 ||
				string(bytes.TrimRight(event.Argv[0][:], "\x00")) != "/bin/true" ||
				event.Flags != 0 {
				t.Fatalf("real exec metadata=%+v argv0=%q", event, event.Argv[0])
			}
			sawExec = event.ExecSequence != 0 &&
				event.ObserverSequence != 0 &&
				event.CgroupID == cgroupID
			kernelPID = event.PID
			execSequence = event.ExecSequence
		case ProcessEventExit:
			sawExit = sawExec && event.PID == kernelPID &&
				event.ExecSequence == execSequence &&
				event.ObserverSequence != 0 &&
				event.CgroupID == cgroupID
		}
	}
	counters, err := reader.Counters()
	if err != nil {
		t.Fatal(err)
	}
	if counters.MatchedEvents < 3 ||
		counters.ReservedEvents != counters.MatchedEvents ||
		counters.RingbufDrops != 0 ||
		counters.StateDrops != 0 {
		t.Fatalf("process counters=%+v", counters)
	}
}

func TestFileEventReaderRealKernel(t *testing.T) {
	if os.Getenv("HIDEOUT_TEST_BPF_ATTACH") != "1" {
		t.Skip("set HIDEOUT_TEST_BPF_ATTACH=1 in a privileged Linux test guest")
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		t.Fatalf("remove memlock limit: %v", err)
	}
	cgroup, cgroupID := newIsolatedTestCgroup(t)
	processReader, err := OpenProcessEventReader(cgroupID)
	if err != nil {
		t.Fatalf("open process state: %+v", err)
	}
	defer processReader.Close()

	reader, err := OpenFileEventReader(cgroupID, processReader)
	if err != nil {
		var verifier *ebpf.VerifierError
		if errors.As(err, &verifier) {
			t.Logf("verifier:\n%+v", verifier)
		}
		t.Fatalf("%+v", err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	if hooks := reader.AttachedHooks(); len(hooks) != 21 {
		t.Fatalf("hooks=%q", hooks)
	} else if !containsHook(hooks, "fexit/security_file_open") {
		t.Fatalf("compatible file-open hook is missing: %q", hooks)
	} else if containsHook(hooks, "fentry/security_file_permission") {
		t.Fatalf("per-I/O permission hook regressed: %q", hooks)
	}
	var configuredCgroupID uint64
	if err := reader.objects.FileTargetCgroupId.Get(&configuredCgroupID); err != nil {
		t.Fatal(err)
	}
	if configuredCgroupID != cgroupID {
		t.Fatalf("configured cgroup id=%d want=%d", configuredCgroupID, cgroupID)
	}
	counters, err := reader.Counters()
	if err != nil {
		t.Fatal(err)
	}
	if counters != (FileCollectorCounters{}) {
		t.Fatalf("initial counters=%+v", counters)
	}
	outsidePath := t.TempDir() + "/outside-target.txt"
	if err := os.WriteFile(outsidePath, []byte("outside-target"), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideFile, err := os.Open(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	var outsideByte [1]byte
	if _, err := outsideFile.Read(outsideByte[:]); err != nil {
		_ = outsideFile.Close()
		t.Fatal(err)
	}
	outsideInfo, err := outsideFile.Stat()
	if err != nil {
		_ = outsideFile.Close()
		t.Fatal(err)
	}
	outsideStat, ok := outsideInfo.Sys().(*syscall.Stat_t)
	if !ok {
		_ = outsideFile.Close()
		t.Fatal("outside-target file has no Linux stat identity")
	}
	present, err := observedFileMetadataContains(
		reader,
		uint64(outsideStat.Dev),
		outsideStat.Ino,
	)
	if closeErr := outsideFile.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if present {
		t.Fatal("non-target read populated the target file metadata cache")
	}

	reader.SetDeadline(time.Now().Add(10 * time.Second))
	type readResult struct {
		event RawFileEvent
		err   error
	}
	results := make(chan readResult, 2048)
	go func() {
		for {
			event, readErr := reader.ReadFileEvent()
			results <- readResult{event: event, err: readErr}
			if readErr != nil {
				return
			}
		}
	}()
	time.Sleep(10 * time.Millisecond)

	workdir := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestFileKernelHelper$")
	command.Env = append(
		os.Environ(),
		"HIDEOUT_FILE_HELPER=1",
		"HIDEOUT_FILE_HELPER_DIR="+workdir,
	)
	command.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    int(cgroup.Fd()),
	}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var helperStderr bytes.Buffer
	command.Stderr = &helperStderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	membership, membershipErr := os.ReadFile(fmt.Sprintf(
		"/proc/%d/cgroup",
		command.Process.Pid,
	))
	if membershipErr != nil {
		t.Fatalf("read file helper cgroup membership: %v", membershipErr)
	}
	t.Logf(
		"file helper pid=%d cgroup=%q",
		command.Process.Pid,
		strings.TrimSpace(string(membership)),
	)
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || ready != "ready\n" {
		t.Fatalf("file helper readiness: line=%q err=%v stderr=%s", ready, err, helperStderr.String())
	}
	resetFileCounters(t, reader)
	if _, err := stdin.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("file helper: %v\n%s", err, helperStderr.String())
	}

	expected := map[uint32]bool{
		FileEventOpen: false, FileEventRead: false, FileEventWrite: false,
		FileEventMmap: false, FileEventCreate: false, FileEventTruncate: false,
		FileEventRename: false, FileEventUnlink: false, FileEventMetadata: false,
		FileEventHardlink: false, FileEventSymlink: false,
		FileEventMkdir: false, FileEventRmdir: false,
	}
	payloadBytes := uint64(len("hideout-file-observation"))
	deadline := time.After(10 * time.Second)
	var samples []string
	for !allFileKindsObserved(expected) {
		select {
		case result := <-results:
			if result.err != nil {
				current, counterErr := reader.Counters()
				t.Fatalf(
					"read file events, observed=%v samples=%q counters=%+v counterErr=%v: %v",
					expected,
					samples,
					current,
					counterErr,
					result.err,
				)
			}
			event := result.event
			if len(samples) < 24 {
				samples = append(samples, fmt.Sprintf(
					"kind=%d flags=%x path=%q name=%q target=%q targetName=%q",
					event.Kind,
					event.Flags,
					string(bytes.TrimRight(event.Path[:], "\x00")),
					string(bytes.TrimRight(event.PathName[:], "\x00")),
					string(bytes.TrimRight(event.TargetPath[:], "\x00")),
					string(bytes.TrimRight(event.TargetName[:], "\x00")),
				))
			}
			if !rawFileEventStringsAreCanonical(event) {
				t.Fatalf("file event has hidden fixed-string bytes: %+v", event)
			}
			if !rawFileEventMatchesDirectory(event, workdir) {
				continue
			}
			if event.CgroupID != cgroupID ||
				event.ObserverSequence == 0 ||
				event.PID == 0 || event.ExecutionPID == 0 ||
				event.ExecSequence == 0 ||
				event.Flags&FileFlagStateUnavailable != 0 {
				t.Fatalf("unattributed file event=%+v", event)
			}
			switch event.Kind {
			case FileEventWrite, FileEventRead:
				if event.Bytes == payloadBytes {
					expected[event.Kind] = true
				}
			case FileEventMmap:
				if event.Bytes == payloadBytes {
					expected[event.Kind] = true
				}
			default:
				expected[event.Kind] = true
			}
		case <-deadline:
			t.Fatalf("timed out waiting for file kinds: %v", expected)
		}
	}
	counters, err = reader.Counters()
	if err != nil {
		t.Fatal(err)
	}
	if counters.MatchedEvents == 0 ||
		counters.ReservedEvents != counters.MatchedEvents ||
		counters.RingbufDrops != 0 ||
		counters.StateDrops != 0 ||
		counters.PathFailures != 0 {
		t.Fatalf("file counters=%+v", counters)
	}
}

func containsHook(hooks []string, want string) bool {
	for _, hook := range hooks {
		if hook == want {
			return true
		}
	}
	return false
}

func TestFileKernelHelper(t *testing.T) {
	if os.Getenv("HIDEOUT_FILE_HELPER") != "1" {
		t.Skip("file observer helper process")
	}
	workdir := os.Getenv("HIDEOUT_FILE_HELPER_DIR")
	if workdir == "" {
		t.Fatal("missing helper directory")
	}
	if _, err := os.Stdout.WriteString("ready\n"); err != nil {
		t.Fatal(err)
	}
	var proceed [1]byte
	if _, err := os.Stdin.Read(proceed[:]); err != nil {
		t.Fatal(err)
	}
	source := workdir + "/source.txt"
	renamed := workdir + "/renamed.txt"
	hardlink := workdir + "/hardlink.txt"
	symlink := workdir + "/symlink.txt"
	directory := workdir + "/directory"
	payload := []byte("hideout-file-observation")
	if err := os.WriteFile(source, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("read: %v data=%q", err, data)
	}
	file, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := unix.Mmap(
		int(file.Fd()), 0, len(payload),
		unix.PROT_READ, unix.MAP_PRIVATE,
	)
	if err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if !bytes.Equal(mapping, payload) {
		t.Fatalf("mmap=%q", mapping)
	}
	if err := unix.Munmap(mapping); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(source, int64(len(payload))); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, renamed); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(renamed, hardlink); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("renamed.txt", symlink); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(directory); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{hardlink, symlink, renamed} {
		if err := os.Remove(value); err != nil {
			t.Fatal(err)
		}
	}
}

func resetFileCounters(t *testing.T, reader *FileEventReader) {
	t.Helper()
	cpus, err := ebpf.PossibleCPU()
	if err != nil {
		t.Fatal(err)
	}
	values := make([]fileObserverFileCollectorCounters, cpus)
	if err := reader.objects.FileCounters.Put(uint32(0), values); err != nil {
		t.Fatal(err)
	}
}

func observedFileMetadataContains(
	reader *FileEventReader,
	device uint64,
	inode uint64,
) (bool, error) {
	if reader == nil || reader.objects.ObservedFiles == nil {
		return false, errors.New("observed file metadata map is unavailable")
	}
	iterator := reader.objects.ObservedFiles.Iterate()
	var key uint64
	var value fileObserverFileMetadata
	for iterator.Next(&key, &value) {
		if value.Device == device && value.Inode == inode {
			return true, nil
		}
	}
	return false, iterator.Err()
}

func allFileKindsObserved(values map[uint32]bool) bool {
	for _, observed := range values {
		if !observed {
			return false
		}
	}
	return true
}

func rawFileEventMatchesDirectory(event RawFileEvent, directory string) bool {
	values := []string{
		string(bytes.TrimRight(event.Path[:], "\x00")),
		string(bytes.TrimRight(event.PathName[:], "\x00")),
		string(bytes.TrimRight(event.TargetPath[:], "\x00")),
		string(bytes.TrimRight(event.TargetName[:], "\x00")),
	}
	for _, value := range values {
		if value == directory || strings.HasPrefix(value, directory+"/") ||
			value == "source.txt" || value == "renamed.txt" ||
			value == "hardlink.txt" || value == "symlink.txt" ||
			value == "directory" {
			return true
		}
	}
	return false
}

func rawFileEventStringsAreCanonical(event RawFileEvent) bool {
	values := []struct {
		value     []byte
		truncated bool
	}{
		{event.Path[:], event.Flags&FileFlagPathTruncated != 0},
		{event.PathName[:], event.Flags&FileFlagPathTruncated != 0},
		{event.TargetPath[:], event.Flags&FileFlagTargetTruncated != 0},
		{event.TargetName[:], event.Flags&FileFlagTargetTruncated != 0},
	}
	for _, candidate := range values {
		terminator := bytes.IndexByte(candidate.value, 0)
		if terminator < 0 {
			if !candidate.truncated {
				return false
			}
			continue
		}
		for _, current := range candidate.value[terminator+1:] {
			if current != 0 {
				return false
			}
		}
	}
	return true
}

func newIsolatedTestCgroup(t *testing.T) (*os.File, uint64) {
	t.Helper()
	cgroupPath := fmt.Sprintf(
		"/sys/fs/cgroup/hideout-bpf-test-%d-%d",
		os.Getpid(),
		time.Now().UnixNano(),
	)
	if err := os.Mkdir(cgroupPath, 0o755); err != nil {
		t.Fatalf("create isolated cgroup: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Remove(cgroupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Errorf("remove isolated cgroup: %v", err)
		}
	})
	cgroup, err := os.Open(cgroupPath)
	if err != nil {
		t.Fatalf("open isolated cgroup: %v", err)
	}
	t.Cleanup(func() { _ = cgroup.Close() })

	handle, _, err := unix.NameToHandleAt(unix.AT_FDCWD, cgroupPath, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(handle.Bytes()) != 8 {
		t.Fatalf("cgroup handle size=%d want=8", len(handle.Bytes()))
	}
	handleID := binary.NativeEndian.Uint64(handle.Bytes())
	cgroupID := probeCurrentCgroupID(t, int(cgroup.Fd()))
	t.Logf(
		"cgroup kernel id=%d handle id=%d handle=%x type=%d",
		cgroupID,
		handleID,
		handle.Bytes(),
		handle.Type(),
	)
	return cgroup, cgroupID
}

func TestProcessExecveatKernelHelper(t *testing.T) {
	if os.Getenv("HIDEOUT_EXECVEAT_HELPER") != "1" {
		t.Skip("execveat helper process")
	}
	filename, err := unix.BytePtrFromString("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	argument, err := unix.BytePtrFromString("/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	argv := []*byte{argument, nil}
	environment := []*byte{nil}
	dirFD := unix.AT_FDCWD
	_, _, errno := unix.Syscall6(
		unix.SYS_EXECVEAT,
		uintptr(dirFD),
		uintptr(unsafe.Pointer(filename)),
		uintptr(unsafe.Pointer(&argv[0])),
		uintptr(unsafe.Pointer(&environment[0])),
		0,
		0,
	)
	if errno != 0 {
		t.Fatalf("execveat: %v", errno)
	}
	t.Fatal("execveat returned without an error")
}

func probeCurrentCgroupID(
	t *testing.T,
	cgroupFD int,
) uint64 {
	t.Helper()
	values, err := ebpf.NewMap(&ebpf.MapSpec{
		Name: "hideout_test_cgroup_id", Type: ebpf.Hash,
		KeySize: 4, ValueSize: 8, MaxEntries: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer values.Close()
	program, err := ebpf.NewProgram(&ebpf.ProgramSpec{
		Name: "hideout_test_cgroup_id", Type: ebpf.TracePoint, License: "GPL",
		Instructions: asm.Instructions{
			asm.FnGetCurrentPidTgid.Call(),
			asm.Mov.Reg(asm.R6, asm.R0),
			asm.FnGetCurrentCgroupId.Call(),
			asm.Mov.Reg(asm.R7, asm.R0),
			asm.RSh.Imm(asm.R6, 32),
			asm.StoreMem(asm.RFP, -4, asm.R6, asm.Word),
			asm.StoreMem(asm.RFP, -16, asm.R7, asm.DWord),
			asm.LoadMapPtr(asm.R1, values.FD()),
			asm.Mov.Reg(asm.R2, asm.RFP),
			asm.Add.Imm(asm.R2, -4),
			asm.Mov.Reg(asm.R3, asm.RFP),
			asm.Add.Imm(asm.R3, -16),
			asm.Mov.Imm(asm.R4, 0),
			asm.FnMapUpdateElem.Call(),
			asm.Mov.Imm(asm.R0, 0),
			asm.Return(),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer program.Close()
	attached, err := link.Tracepoint("syscalls", "sys_enter_execve", program, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer attached.Close()
	command := exec.Command("/bin/true")
	command.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    cgroupFD,
	}
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	pid := uint32(command.Process.Pid)
	if pid == 0 {
		t.Fatal("kernel helper pid is zero")
	}
	var value uint64
	if err := values.Lookup(pid, &value); err != nil {
		t.Fatal(err)
	}
	if value == 0 {
		t.Fatal("kernel helper returned zero cgroup id")
	}
	return value
}
