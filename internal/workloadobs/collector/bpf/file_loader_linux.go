//go:build linux

package bpf

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
)

var (
	ErrFileCollectorTarget       = errors.New("workload observer file collector target is invalid")
	ErrFileCollectorProcessState = errors.New("workload observer file collector process state is unavailable")
)

type FileEventReader struct {
	manifest ArtifactManifest
	objects  fileObserverObjects
	reader   *ringbuf.Reader
	record   ringbuf.Record
	links    []link.Link
	hooks    []string
	metadata map[uint64]fileEventMetadata

	stopOnce  sync.Once
	stopErr   error
	closeOnce sync.Once
	closeErr  error
}

type fileEventMetadata struct {
	Device   uint64
	Inode    uint64
	FileType uint32
	Flags    uint32
	Path     [FilePathBytes]byte
}

type FileCollectorCounters struct {
	MatchedEvents     uint64
	ReservedEvents    uint64
	RingbufDrops      uint64
	StateDrops        uint64
	StateDegradations uint64
	PathFailures      uint64
	IdentityFailures  uint64
}

func OpenFileEventReader(
	targetCgroupID uint64,
	processReader *ProcessEventReader,
) (*FileEventReader, error) {
	if targetCgroupID == 0 {
		return nil, ErrFileCollectorTarget
	}
	if processReader == nil ||
		processReader.objects.ExecSequences == nil ||
		processReader.objects.ObserverSequences == nil ||
		processReader.objects.TargetCgroupId == nil {
		return nil, ErrFileCollectorProcessState
	}
	var processTarget uint64
	if err := processReader.objects.TargetCgroupId.Get(&processTarget); err != nil {
		return nil, errors.Join(ErrFileCollectorProcessState, err)
	}
	if processTarget != targetCgroupID {
		return nil, ErrFileCollectorTarget
	}

	spec, manifest, err := LoadEmbeddedFileSpec()
	if err != nil {
		return nil, err
	}
	target := spec.Variables[fileObserverVarFileTargetCgroupId]
	if target == nil || !target.Constant() {
		return nil, errors.New("workload observer file target cgroup constant is missing")
	}
	if err := target.Set(targetCgroupID); err != nil {
		return nil, fmt.Errorf("set workload observer file cgroup target: %w", err)
	}

	result := &FileEventReader{
		manifest: manifest,
		metadata: make(map[uint64]fileEventMetadata),
	}
	options := ebpf.CollectionOptions{
		MapReplacements: map[string]*ebpf.Map{
			fileObserverMapExecSequences:     processReader.objects.ExecSequences,
			fileObserverMapObserverSequences: processReader.objects.ObserverSequences,
		},
	}
	if err := spec.LoadAndAssign(&result.objects, &options); err != nil {
		return nil, errors.Join(
			fmt.Errorf("load workload observer file programs: %w", err),
			closeFileObserverObjects(&result.objects),
		)
	}
	result.reader, err = ringbuf.NewReader(result.objects.FileObservationEvents)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("open workload observer file ring: %w", err),
			closeFileObserverObjects(&result.objects),
		)
	}

	type hook struct {
		name    string
		program *ebpf.Program
		attach  func(*ebpf.Program) (link.Link, error)
	}
	tracepoint := func(group, name string) func(*ebpf.Program) (link.Link, error) {
		return func(program *ebpf.Program) (link.Link, error) {
			return link.Tracepoint(group, name, program, nil)
		}
	}
	tracing := func(program *ebpf.Program) (link.Link, error) {
		return link.AttachTracing(link.TracingOptions{Program: program})
	}
	hooks := []hook{
		{name: "fentry/security_file_free", program: result.objects.HideoutForgetFile, attach: tracing},
		{name: "fexit/security_file_open", program: result.objects.HideoutObserveFileOpen, attach: tracing},
		{name: "fexit/vfs_read", program: result.objects.HideoutObserveVfsRead, attach: tracing},
		{name: "fexit/vfs_write", program: result.objects.HideoutObserveVfsWrite, attach: tracing},
		{name: "fexit/vfs_readv", program: result.objects.HideoutObserveVfsReadv, attach: tracing},
		{name: "fexit/vfs_writev", program: result.objects.HideoutObserveVfsWritev, attach: tracing},
		{name: "fexit/vfs_copy_file_range", program: result.objects.HideoutObserveCopyFileRange, attach: tracing},
		{name: "syscalls/sys_enter_mmap", program: result.objects.HideoutCaptureMmapLength, attach: tracepoint("syscalls", "sys_enter_mmap")},
		{name: "syscalls/sys_exit_mmap", program: result.objects.HideoutForgetMmapLength, attach: tracepoint("syscalls", "sys_exit_mmap")},
		{name: "fexit/security_mmap_file", program: result.objects.HideoutObserveMmapFile, attach: tracing},
		{name: "fexit/security_path_truncate", program: result.objects.HideoutObservePathTruncate, attach: tracing},
		{name: "fexit/security_file_truncate", program: result.objects.HideoutObserveFileTruncate, attach: tracing},
		{name: "fexit/security_path_unlink", program: result.objects.HideoutObservePathUnlink, attach: tracing},
		{name: "fexit/security_path_rename", program: result.objects.HideoutObservePathRename, attach: tracing},
		{name: "fexit/security_path_link", program: result.objects.HideoutObservePathLink, attach: tracing},
		{name: "fexit/security_path_symlink", program: result.objects.HideoutObservePathSymlink, attach: tracing},
		{name: "fexit/security_path_chmod", program: result.objects.HideoutObservePathChmod, attach: tracing},
		{name: "fexit/security_path_chown", program: result.objects.HideoutObservePathChown, attach: tracing},
		{name: "fexit/security_path_mkdir", program: result.objects.HideoutObservePathMkdir, attach: tracing},
		{name: "fexit/security_path_rmdir", program: result.objects.HideoutObservePathRmdir, attach: tracing},
		{name: "sched/sched_process_exit", program: result.objects.HideoutCleanupFileThread, attach: tracepoint("sched", "sched_process_exit")},
	}
	for _, hook := range hooks {
		if hook.program == nil {
			return nil, errors.Join(
				fmt.Errorf("workload observer file hook %s program is missing", hook.name),
				result.Close(),
			)
		}
		attached, attachErr := hook.attach(hook.program)
		if attachErr != nil {
			return nil, errors.Join(
				fmt.Errorf("attach workload observer file hook %s: %w", hook.name, attachErr),
				result.Close(),
			)
		}
		result.links = append(result.links, attached)
		result.hooks = append(result.hooks, hook.name)
	}
	return result, nil
}

func (reader *FileEventReader) ReadFileEvent() (RawFileEvent, error) {
	var event RawFileEvent
	if err := reader.ReadFileEventInto(&event); err != nil {
		return RawFileEvent{}, err
	}
	return event, nil
}

// ReadFileEventInto reads and resolves one event into caller-owned storage.
// Callers may reuse that storage only after they have finished consuming the
// current event.
func (reader *FileEventReader) ReadFileEventInto(event *RawFileEvent) error {
	if reader == nil || reader.reader == nil {
		return ringbuf.ErrClosed
	}
	if event == nil {
		return ErrFileRecord
	}
	// ReadInto reuses the largest sample buffer seen by this single-consumer
	// reader. File-heavy workloads otherwise allocate one RawSample for every
	// open/read/write event before decoding it into the fixed record shape.
	if err := reader.reader.ReadInto(&reader.record); err != nil {
		return err
	}
	if err := DecodeFileEventInto(reader.record.RawSample, event); err != nil {
		return err
	}
	reader.resolveFileMetadata(event)
	return nil
}

func (reader *FileEventReader) resolveFileMetadata(
	event *RawFileEvent,
) {
	if reader == nil || event == nil || event.FileKey == 0 {
		return
	}
	if !event.Compact {
		reader.rememberFileMetadata(event.FileKey, fileEventMetadata{
			Device: event.Device, Inode: event.Inode,
			FileType: event.FileType,
			Flags:    event.Flags & fileMetadataFlags(),
			Path:     event.Path,
		})
		return
	}
	metadata, ok := reader.metadata[event.FileKey]
	if !ok || !fileMetadataMatchesEvent(metadata, event) {
		// The kernel cache intentionally retains only identity/state. A compact
		// event whose preceding full-path record is absent cannot safely recover
		// a path from that map, so expose the coverage gap instead of inventing
		// or reusing stale metadata.
		event.Flags |= FileFlagPathUnavailable | FileFlagStateUnavailable
		clear(event.Path[:])
		return
	}
	event.Flags |= metadata.Flags
	event.Path = metadata.Path
}

func (reader *FileEventReader) rememberFileMetadata(
	key uint64,
	metadata fileEventMetadata,
) {
	if reader == nil || key == 0 || reader.metadata == nil {
		return
	}
	const maximumEntries = 65536
	if _, exists := reader.metadata[key]; !exists &&
		len(reader.metadata) >= maximumEntries {
		for candidate := range reader.metadata {
			delete(reader.metadata, candidate)
			break
		}
	}
	reader.metadata[key] = metadata
}

func fileMetadataMatchesEvent(
	metadata fileEventMetadata,
	event *RawFileEvent,
) bool {
	if event == nil {
		return false
	}
	return metadata.Device == event.Device &&
		metadata.Inode == event.Inode &&
		metadata.FileType == event.FileType
}

func fileMetadataFlags() uint32 {
	return FileFlagPathTruncated |
		FileFlagPathUnavailable |
		FileFlagIdentityUnavailable |
		FileFlagStateUnavailable
}

func (reader *FileEventReader) SetDeadline(deadline time.Time) {
	if reader != nil && reader.reader != nil {
		reader.reader.SetDeadline(deadline)
	}
}

// FlushPending drains every record visible at the call boundary and then
// interrupts ReadFileEventInto with ringbuf.ErrFlushed. It may be called while
// the collector goroutine is blocked in the ring reader.
func (reader *FileEventReader) FlushPending() error {
	if reader == nil || reader.reader == nil {
		return ringbuf.ErrClosed
	}
	return reader.reader.Flush()
}

func (reader *FileEventReader) AttachedHooks() []string {
	if reader == nil {
		return nil
	}
	return append([]string(nil), reader.hooks...)
}

func (reader *FileEventReader) ArtifactManifest() ArtifactManifest {
	if reader == nil {
		return ArtifactManifest{}
	}
	return reader.manifest
}

func (reader *FileEventReader) Counters() (FileCollectorCounters, error) {
	if reader == nil || reader.objects.FileCounters == nil {
		return FileCollectorCounters{}, ringbuf.ErrClosed
	}
	cpus, err := ebpf.PossibleCPU()
	if err != nil {
		return FileCollectorCounters{}, err
	}
	values := make([]fileObserverFileCollectorCounters, cpus)
	if err := reader.objects.FileCounters.Lookup(uint32(0), &values); err != nil {
		return FileCollectorCounters{}, err
	}
	var result FileCollectorCounters
	for _, value := range values {
		additions := []struct {
			target *uint64
			value  uint64
		}{
			{&result.MatchedEvents, value.MatchedEvents},
			{&result.ReservedEvents, value.ReservedEvents},
			{&result.RingbufDrops, value.RingbufDrops},
			{&result.StateDrops, value.StateDrops},
			{&result.StateDegradations, value.StateDegradations},
			{&result.PathFailures, value.PathFailures},
			{&result.IdentityFailures, value.IdentityFailures},
		}
		for _, addition := range additions {
			if math.MaxUint64-*addition.target < addition.value {
				return FileCollectorCounters{}, errors.New("workload observer file counters overflow")
			}
			*addition.target += addition.value
		}
	}
	return result, nil
}

func (reader *FileEventReader) Close() error {
	if reader == nil {
		return nil
	}
	reader.closeOnce.Do(func() {
		reader.closeErr = errors.Join(reader.closeErr, reader.Stop())
		for index := len(reader.links) - 1; index >= 0; index-- {
			reader.closeErr = errors.Join(reader.closeErr, reader.links[index].Close())
		}
		reader.closeErr = errors.Join(
			reader.closeErr,
			closeFileObserverObjects(&reader.objects),
		)
	})
	return reader.closeErr
}

// Stop interrupts the userspace ring reader without closing the BPF maps used
// for the final matched/reserved/drop counter receipt.
func (reader *FileEventReader) Stop() error {
	if reader == nil {
		return nil
	}
	reader.stopOnce.Do(func() {
		if reader.reader != nil {
			reader.stopErr = errors.Join(reader.stopErr, reader.reader.Close())
		}
	})
	return reader.stopErr
}

func closeFileObserverObjects(objects *fileObserverObjects) error {
	if objects == nil {
		return nil
	}
	var result error
	programs := []*ebpf.Program{
		objects.HideoutCaptureMmapLength,
		objects.HideoutCleanupFileThread,
		objects.HideoutForgetFile,
		objects.HideoutForgetMmapLength,
		objects.HideoutObserveCopyFileRange,
		objects.HideoutObserveFileOpen,
		objects.HideoutObserveFileTruncate,
		objects.HideoutObserveMmapFile,
		objects.HideoutObservePathChmod,
		objects.HideoutObservePathChown,
		objects.HideoutObservePathLink,
		objects.HideoutObservePathMkdir,
		objects.HideoutObservePathRename,
		objects.HideoutObservePathRmdir,
		objects.HideoutObservePathSymlink,
		objects.HideoutObservePathTruncate,
		objects.HideoutObservePathUnlink,
		objects.HideoutObserveVfsRead,
		objects.HideoutObserveVfsReadv,
		objects.HideoutObserveVfsWrite,
		objects.HideoutObserveVfsWritev,
	}
	for _, program := range programs {
		if program != nil {
			result = errors.Join(result, program.Close())
		}
	}
	maps := []*ebpf.Map{
		objects.ExecSequences,
		objects.FileCounters,
		objects.FileMetadataScratch,
		objects.FileObservationEvents,
		objects.MmapLengths,
		objects.ObservedFiles,
		objects.ObserverSequences,
	}
	for _, currentMap := range maps {
		if currentMap != nil {
			result = errors.Join(result, currentMap.Close())
		}
	}
	return result
}
