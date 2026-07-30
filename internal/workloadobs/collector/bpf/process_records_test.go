package bpf

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestDecodeProcessEventUsesExactStableLayout(t *testing.T) {
	record := make([]byte, ProcessRecordSize)
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
		ProcessEventExec, 7, 101, 101, 100, 1000, 1001, 2, 0, 0,
		ProcessFlagArgvTruncated, 0,
	} {
		put32(value)
	}
	for _, value := range []uint64{3141, 44, 12, 11, 99_000} {
		put64(value)
	}
	copy(record[offset:offset+ProcessExecutableBytes], "/usr/bin/claude\x00ignored")
	offset += ProcessExecutableBytes
	copy(record[offset:offset+ProcessArgumentBytes], "claude\x00")
	offset += ProcessArgumentBytes
	copy(record[offset:offset+ProcessArgumentBytes], "--print\x00")

	event, err := DecodeProcessEvent(record)
	if err != nil {
		t.Fatal(err)
	}
	if event.Kind != ProcessEventExec || event.CPU != 7 ||
		event.PID != 101 || event.ParentPID != 100 ||
		event.CgroupID != 3141 || event.ObserverSequence != 44 ||
		event.ExecSequence != 12 || event.ParentExecSequence != 11 ||
		event.MonotonicNS != 99_000 || event.Argc != 2 ||
		event.Flags != ProcessFlagArgvTruncated {
		t.Fatalf("event=%+v", event)
	}
	if got := string(event.Executable[:16]); got != "/usr/bin/claude\x00" {
		t.Fatalf("executable prefix=%q", got)
	}
	if got := string(event.Argv[1][:8]); got != "--print\x00" {
		t.Fatalf("argv[1]=%q", got)
	}
}

func TestDecodeProcessEventRejectsMalformedKernelRecords(t *testing.T) {
	valid := make([]byte, ProcessRecordSize)
	binary.LittleEndian.PutUint32(valid[0:4], ProcessEventFork)

	fixtures := [][]byte{
		valid[:len(valid)-1],
		append(append([]byte(nil), valid...), 0),
		mutateProcessRecord(valid, 0, 99),
		mutateProcessRecord(valid, 28, ProcessMaxArguments+1),
		mutateProcessRecord(valid, 40, 1<<31),
		mutateProcessRecord(valid, 44, 1),
	}
	for index, fixture := range fixtures {
		if _, err := DecodeProcessEvent(fixture); !errors.Is(err, ErrProcessRecord) {
			t.Fatalf("fixture %d error=%v", index, err)
		}
	}
}

func mutateProcessRecord(source []byte, offset int, value uint32) []byte {
	result := append([]byte(nil), source...)
	binary.LittleEndian.PutUint32(result[offset:offset+4], value)
	return result
}
