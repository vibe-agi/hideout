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
	links    []link.Link
	hooks    []string
	metadata map[uint64]fileEventMetadata

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
	MatchedEvents    uint64
	ReservedEvents   uint64
	RingbufDrops     uint64
	StateDrops       uint64
	PathFailures     uint64
	IdentityFailures uint64
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
	if reader == nil || reader.reader == nil {
		return RawFileEvent{}, ringbuf.ErrClosed
	}
	record, err := reader.reader.Read()
	if err != nil {
		return RawFileEvent{}, err
	}
	event, err := DecodeFileEvent(record.RawSample)
	if err != nil {
		return RawFileEvent{}, err
	}
	return reader.resolveFileMetadata(event), nil
}

func (reader *FileEventReader) resolveFileMetadata(
	event RawFileEvent,
) RawFileEvent {
	if reader == nil || event.FileKey == 0 {
		return event
	}
	if !event.Compact {
		reader.rememberFileMetadata(event.FileKey, fileEventMetadata{
			Device: event.Device, Inode: event.Inode,
			FileType: event.FileType,
			Flags:    event.Flags & fileMetadataFlags(),
			Path:     event.Path,
		})
		return event
	}
	metadata, ok := reader.metadata[event.FileKey]
	if !ok || !fileMetadataMatchesEvent(metadata, event) {
		metadata, ok = reader.lookupFileMetadata(event)
		if ok {
			reader.rememberFileMetadata(event.FileKey, metadata)
		}
	}
	if !ok || !fileMetadataMatchesEvent(metadata, event) {
		event.Flags |= FileFlagPathUnavailable | FileFlagStateUnavailable
		clear(event.Path[:])
		return event
	}
	event.Flags |= metadata.Flags
	event.Path = metadata.Path
	return event
}

func (reader *FileEventReader) lookupFileMetadata(
	event RawFileEvent,
) (fileEventMetadata, bool) {
	if reader == nil || reader.objects.ObservedFiles == nil ||
		event.FileKey == 0 {
		return fileEventMetadata{}, false
	}
	var raw fileObserverFileMetadata
	if err := reader.objects.ObservedFiles.Lookup(event.FileKey, &raw); err != nil ||
		raw.Announced > 1 ||
		raw.Flags&^fileMetadataFlags() != 0 {
		return fileEventMetadata{}, false
	}
	metadata := fileEventMetadata{
		Device: raw.Device, Inode: raw.Inode,
		FileType: raw.FileType, Flags: raw.Flags,
	}
	for index, value := range raw.Path {
		metadata.Path[index] = byte(value)
	}
	sanitizeFileString(metadata.Path[:])
	return metadata, fileMetadataMatchesEvent(metadata, event)
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
	event RawFileEvent,
) bool {
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
		if reader.reader != nil {
			reader.closeErr = errors.Join(reader.closeErr, reader.reader.Close())
		}
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
