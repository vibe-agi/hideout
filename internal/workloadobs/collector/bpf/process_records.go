package bpf

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	ProcessEventFork uint32 = 1
	ProcessEventExec uint32 = 2
	ProcessEventExit uint32 = 3

	ProcessFlagExecutableTruncated   uint32 = 1 << 0
	ProcessFlagArgvTruncated         uint32 = 1 << 1
	ProcessFlagArgvUnavailable       uint32 = 1 << 2
	ProcessFlagExecutableUnavailable uint32 = 1 << 3
	ProcessFlagStateUnavailable      uint32 = 1 << 4
	ProcessFlagExitUnavailable       uint32 = 1 << 5

	ProcessExecutableBytes = 128
	ProcessMaxArguments    = 4
	ProcessArgumentBytes   = 64
	ProcessRecordSize      = 472
)

var ErrProcessRecord = errors.New("workload observer process record is invalid")

// RawProcessEvent is the fixed-width userspace representation of
// hideout_process_event in programs.c. Numeric fields are explicitly decoded
// as little endian instead of relying on Go struct padding or host byte order.
type RawProcessEvent struct {
	Kind      uint32
	CPU       uint32
	PID       uint32
	TID       uint32
	ParentPID uint32
	UID       uint32
	GID       uint32
	Argc      uint32
	ExitCode  uint32
	Signal    uint32
	Flags     uint32
	Reserved  uint32

	CgroupID           uint64
	ObserverSequence   uint64
	ExecSequence       uint64
	ParentExecSequence uint64
	MonotonicNS        uint64

	Executable [ProcessExecutableBytes]byte
	Argv       [ProcessMaxArguments][ProcessArgumentBytes]byte
}

func DecodeProcessEvent(record []byte) (RawProcessEvent, error) {
	if len(record) != ProcessRecordSize {
		return RawProcessEvent{}, fmt.Errorf(
			"%w: size=%d want=%d", ErrProcessRecord, len(record), ProcessRecordSize,
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
	event := RawProcessEvent{
		Kind: next32(), CPU: next32(), PID: next32(), TID: next32(),
		ParentPID: next32(), UID: next32(), GID: next32(), Argc: next32(),
		ExitCode: next32(), Signal: next32(), Flags: next32(), Reserved: next32(),
		CgroupID: next64(), ObserverSequence: next64(), ExecSequence: next64(),
		ParentExecSequence: next64(), MonotonicNS: next64(),
	}
	copy(event.Executable[:], record[offset:offset+ProcessExecutableBytes])
	offset += ProcessExecutableBytes
	for index := range event.Argv {
		copy(event.Argv[index][:], record[offset:offset+ProcessArgumentBytes])
		offset += ProcessArgumentBytes
	}
	if offset != len(record) ||
		event.Kind < ProcessEventFork || event.Kind > ProcessEventExit ||
		event.Argc > ProcessMaxArguments ||
		event.Reserved != 0 ||
		event.Flags & ^uint32(
			ProcessFlagExecutableTruncated|
				ProcessFlagArgvTruncated|
				ProcessFlagArgvUnavailable|
				ProcessFlagExecutableUnavailable|
				ProcessFlagStateUnavailable|
				ProcessFlagExitUnavailable,
		) != 0 {
		return RawProcessEvent{}, ErrProcessRecord
	}
	return event, nil
}
