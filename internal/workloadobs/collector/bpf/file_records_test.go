package bpf

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestDecodeFileEventUsesExactFixedLayout(t *testing.T) {
	record := make([]byte, FileRecordSize)
	offset := 0
	put32 := func(value uint32) {
		binary.LittleEndian.PutUint32(record[offset:offset+4], value)
		offset += 4
	}
	put64 := func(value uint64) {
		binary.LittleEndian.PutUint64(record[offset:offset+8], value)
		offset += 8
	}
	for _, value := range []uint32{
		FileEventWrite, 3, 41, 42, 40, 1000, 1001,
		FileFlagPathTruncated, FileTypeRegular, 0,
	} {
		put32(value)
	}
	negativeResult := int64(-13)
	put64(uint64(negativeResult))
	for _, value := range []uint64{
		3141, 9, 7, 12_345, 23, 8, 9001, 77, 0xfeed,
	} {
		put64(value)
	}
	copy(record[offset:offset+FilePathBytes], "/workspace/input.txt")
	copy(record[offset+256:offset+FilePathBytes], "/workspace/input.txt")
	offset += FilePathBytes
	copy(record[offset:offset+FileNameBytes], "component")
	offset += FileNameBytes
	copy(record[offset:offset+FilePathBytes], "/workspace/output.txt")
	offset += FilePathBytes
	copy(record[offset:offset+FileNameBytes], "target")
	offset += FileNameBytes
	if offset != FileRecordSize {
		t.Fatalf("fixture size=%d want=%d", offset, FileRecordSize)
	}

	event, err := DecodeFileEvent(record)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != FileEventWrite || event.CPU != 3 ||
		event.PID != 41 || event.TID != 42 ||
		event.ExecutionPID != 40 || event.ExecSequence != 7 ||
		event.Result != -13 || event.Bytes != 23 ||
		event.Device != 8 || event.Inode != 9001 || event.MountID != 77 ||
		event.FileKey != 0xfeed || event.Compact || event.Cached ||
		event.Path[0] != '/' || event.Path[256] != 0 ||
		event.TargetName[0] != 't' {
		t.Fatalf("event=%+v", event)
	}
}

func TestDecodeFileEventAcceptsCachedPathLayout(t *testing.T) {
	for _, kind := range []uint32{
		FileEventOpen,
		FileEventRead,
		FileEventWrite,
		FileEventMmap,
		FileEventCreate,
		FileEventTruncate,
	} {
		record := make([]byte, FileCachedRecordSize)
		binary.LittleEndian.PutUint32(record[0:4], kind)
		binary.LittleEndian.PutUint32(record[32:36], FileTypeRegular)
		binary.LittleEndian.PutUint64(record[112:120], 0x1234)
		copy(record[FileCompactRecordSize:], "/workspace/input.txt")
		event, err := DecodeFileEvent(record)
		if err != nil {
			t.Fatalf("kind=%d: %v", kind, err)
		}
		if event.Compact || !event.Cached || event.Kind != kind ||
			event.FileKey != 0x1234 ||
			string(event.Path[:20]) != "/workspace/input.txt" ||
			event.PathName != [FileNameBytes]byte{} ||
			event.TargetPath != [FilePathBytes]byte{} ||
			event.TargetName != [FileNameBytes]byte{} {
			t.Fatalf("event=%+v", event)
		}
	}
}

func TestDecodeFileEventAcceptsCompactReadWriteLayout(t *testing.T) {
	for _, kind := range []uint32{FileEventRead, FileEventWrite} {
		record := make([]byte, FileCompactRecordSize)
		binary.LittleEndian.PutUint32(record[0:4], kind)
		binary.LittleEndian.PutUint32(record[32:36], FileTypeRegular)
		binary.LittleEndian.PutUint64(record[112:120], 0x1234)
		event, err := DecodeFileEvent(record)
		if err != nil {
			t.Fatalf("kind=%d: %v", kind, err)
		}
		if !event.Compact || event.Kind != kind ||
			event.Cached || event.FileKey != 0x1234 {
			t.Fatalf("event=%+v", event)
		}
	}
}

func TestDecodeFileEventRejectsSizeEnumsReservedAndFlags(t *testing.T) {
	valid := make([]byte, FileRecordSize)
	binary.LittleEndian.PutUint32(valid[0:4], FileEventOpen)

	for name, mutate := range map[string]func([]byte) []byte{
		"short": func(value []byte) []byte { return value[:len(value)-1] },
		"kind": func(value []byte) []byte {
			binary.LittleEndian.PutUint32(value[0:4], FileEventRmdir+1)
			return value
		},
		"type": func(value []byte) []byte {
			binary.LittleEndian.PutUint32(value[32:36], FileTypeDevice+1)
			return value
		},
		"reserved": func(value []byte) []byte {
			binary.LittleEndian.PutUint32(value[36:40], 1)
			return value
		},
		"flags": func(value []byte) []byte {
			binary.LittleEndian.PutUint32(value[28:32], 1<<31)
			return value
		},
		"compact-open": func(value []byte) []byte {
			binary.LittleEndian.PutUint64(
				value[112:120],
				1,
			)
			return value[:FileCompactRecordSize]
		},
		"cached-mutation": func(value []byte) []byte {
			binary.LittleEndian.PutUint32(value[0:4], FileEventRename)
			binary.LittleEndian.PutUint64(value[112:120], 1)
			return value[:FileCachedRecordSize]
		},
		"cached-without-key": func(value []byte) []byte {
			return value[:FileCachedRecordSize]
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := append([]byte(nil), valid...)
			if _, err := DecodeFileEvent(mutate(fixture)); !errors.Is(err, ErrFileRecord) {
				t.Fatalf("error=%v want=%v", err, ErrFileRecord)
			}
		})
	}
}
