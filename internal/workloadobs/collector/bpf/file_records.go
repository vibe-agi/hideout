package bpf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	FileEventOpen     uint32 = 1
	FileEventRead     uint32 = 2
	FileEventWrite    uint32 = 3
	FileEventMmap     uint32 = 4
	FileEventCreate   uint32 = 5
	FileEventTruncate uint32 = 6
	FileEventRename   uint32 = 7
	FileEventUnlink   uint32 = 8
	FileEventMetadata uint32 = 9
	FileEventHardlink uint32 = 10
	FileEventSymlink  uint32 = 11
	FileEventMkdir    uint32 = 12
	FileEventRmdir    uint32 = 13

	FileFlagPathTruncated       uint32 = 1 << 0
	FileFlagPathUnavailable     uint32 = 1 << 1
	FileFlagPathAliased         uint32 = 1 << 2
	FileFlagTargetTruncated     uint32 = 1 << 3
	FileFlagTargetUnavailable   uint32 = 1 << 4
	FileFlagIdentityUnavailable uint32 = 1 << 5
	FileFlagBytesUnavailable    uint32 = 1 << 6
	FileFlagStateUnavailable    uint32 = 1 << 7
	FileFlagOutcomeUnknown      uint32 = 1 << 8
	FileFlagAuthorizationHook   uint32 = 1 << 9

	FileTypeUnknown   uint32 = 0
	FileTypeRegular   uint32 = 1
	FileTypeDirectory uint32 = 2
	FileTypeSymlink   uint32 = 3
	FileTypeSocket    uint32 = 4
	FileTypeFIFO      uint32 = 5
	FileTypeDevice    uint32 = 6

	FilePathBytes         = 512
	FileNameBytes         = 128
	FileCompactRecordSize = 120
	FileCachedRecordSize  = FileCompactRecordSize + FilePathBytes
	FileRecordSize        = 1400
)

var ErrFileRecord = errors.New("workload observer file record is invalid")

type RawFileEvent struct {
	Kind         uint32
	CPU          uint32
	PID          uint32
	TID          uint32
	ExecutionPID uint32
	UID          uint32
	GID          uint32
	Flags        uint32
	FileType     uint32
	Reserved     uint32
	Result       int64

	CgroupID         uint64
	ObserverSequence uint64
	ExecSequence     uint64
	MonotonicNS      uint64
	Bytes            uint64
	Device           uint64
	Inode            uint64
	MountID          uint64
	FileKey          uint64

	Path       [FilePathBytes]byte
	PathName   [FileNameBytes]byte
	TargetPath [FilePathBytes]byte
	TargetName [FileNameBytes]byte

	Compact bool
	Cached  bool
}

func DecodeFileEvent(record []byte) (RawFileEvent, error) {
	compact := len(record) == FileCompactRecordSize
	cached := len(record) == FileCachedRecordSize
	if !compact && !cached && len(record) != FileRecordSize {
		return RawFileEvent{}, fmt.Errorf(
			"%w: size=%d want=%d-or-%d-or-%d",
			ErrFileRecord,
			len(record),
			FileCompactRecordSize,
			FileCachedRecordSize,
			FileRecordSize,
		)
	}
	offset := 0
	next32 := func() uint32 {
		value := binary.LittleEndian.Uint32(record[offset : offset+4])
		offset += 4
		return value
	}
	next64 := func() uint64 {
		value := binary.LittleEndian.Uint64(record[offset : offset+8])
		offset += 8
		return value
	}
	event := RawFileEvent{
		Kind: next32(), CPU: next32(), PID: next32(), TID: next32(),
		ExecutionPID: next32(), UID: next32(), GID: next32(),
		Flags: next32(), FileType: next32(), Reserved: next32(),
		Result:   int64(next64()),
		CgroupID: next64(), ObserverSequence: next64(),
		ExecSequence: next64(), MonotonicNS: next64(),
		Bytes: next64(), Device: next64(), Inode: next64(),
		MountID: next64(), FileKey: next64(),
		Compact: compact,
		Cached:  cached,
	}
	if !compact {
		copy(event.Path[:], record[offset:offset+FilePathBytes])
		sanitizeFileString(event.Path[:])
		offset += FilePathBytes
	}
	if !compact && !cached {
		copy(event.PathName[:], record[offset:offset+FileNameBytes])
		sanitizeFileString(event.PathName[:])
		offset += FileNameBytes
		copy(event.TargetPath[:], record[offset:offset+FilePathBytes])
		sanitizeFileString(event.TargetPath[:])
		offset += FilePathBytes
		copy(event.TargetName[:], record[offset:offset+FileNameBytes])
		sanitizeFileString(event.TargetName[:])
		offset += FileNameBytes
	}

	if offset != len(record) ||
		event.Kind < FileEventOpen || event.Kind > FileEventRmdir ||
		event.FileType > FileTypeDevice ||
		event.Reserved != 0 ||
		event.Flags&^knownFileFlags() != 0 ||
		(cached &&
			(event.FileKey == 0 ||
				!cachedFileEventKind(event.Kind))) ||
		(compact &&
			(event.Kind != FileEventRead &&
				event.Kind != FileEventWrite ||
				event.FileKey == 0)) {
		return RawFileEvent{}, ErrFileRecord
	}
	return event, nil
}

func sanitizeFileString(value []byte) {
	if index := bytes.IndexByte(value, 0); index >= 0 {
		clear(value[index:])
	}
}

func cachedFileEventKind(kind uint32) bool {
	switch kind {
	case FileEventOpen, FileEventRead, FileEventWrite, FileEventMmap,
		FileEventCreate, FileEventTruncate:
		return true
	default:
		return false
	}
}

func knownFileFlags() uint32 {
	return FileFlagPathTruncated |
		FileFlagPathUnavailable |
		FileFlagPathAliased |
		FileFlagTargetTruncated |
		FileFlagTargetUnavailable |
		FileFlagIdentityUnavailable |
		FileFlagBytesUnavailable |
		FileFlagStateUnavailable |
		FileFlagOutcomeUnknown |
		FileFlagAuthorizationHook
}
