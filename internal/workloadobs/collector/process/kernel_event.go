package process

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"unicode/utf8"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

var ErrKernelProcessMetadata = errors.New("workload observer kernel process metadata is unavailable")

// EventFromKernelRecord binds an untrusted fixed-width kernel record to the
// already-authenticated session boundary. It never infers another owner and
// never fabricates missing executable or argv text.
func EventFromKernelRecord(boundary Boundary, raw observerbpf.RawProcessEvent) (Event, error) {
	if err := boundary.Validate(); err != nil {
		return Event{}, err
	}
	if raw.CgroupID != boundary.CgroupID {
		return Event{}, ErrBoundaryMismatch
	}
	knownFlags := uint32(
		observerbpf.ProcessFlagExecutableTruncated |
			observerbpf.ProcessFlagArgvTruncated |
			observerbpf.ProcessFlagArgvUnavailable |
			observerbpf.ProcessFlagExecutableUnavailable |
			observerbpf.ProcessFlagStateUnavailable |
			observerbpf.ProcessFlagExitUnavailable,
	)
	if raw.Reserved != 0 || raw.Argc > observerbpf.ProcessMaxArguments ||
		raw.Flags & ^knownFlags != 0 {
		return Event{}, ErrInvalidEvent
	}
	event := Event{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		GuestBootID: boundary.GuestBootID, CgroupID: raw.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		PID:                raw.PID, TID: raw.TID, ParentPID: raw.ParentPID,
		ExecSequence: raw.ExecSequence, ParentExecSequence: raw.ParentExecSequence,
		CPU: uint64(raw.CPU), MonotonicNS: raw.MonotonicNS,
		Sequence: raw.ObserverSequence,
	}

	switch raw.Kind {
	case observerbpf.ProcessEventFork:
		if raw.Flags&^observerbpf.ProcessFlagStateUnavailable != 0 ||
			raw.Argc != 0 || raw.ExecSequence != 0 ||
			raw.ExitCode != 0 || raw.Signal != 0 ||
			!zeroKernelProcessText(raw) {
			return Event{}, ErrInvalidEvent
		}
		event.Kind = EventFork
		event.Limitations = processLimitations(raw.Flags)
	case observerbpf.ProcessEventExec:
		event.Kind = EventExec
		if raw.Flags&observerbpf.ProcessFlagExitUnavailable != 0 ||
			raw.ExitCode != 0 || raw.Signal != 0 {
			return Event{}, ErrInvalidEvent
		}
		if raw.Flags&observerbpf.ProcessFlagExecutableUnavailable != 0 {
			return Event{}, fmt.Errorf("%w: executable", ErrKernelProcessMetadata)
		}
		executable, terminated, clean := fixedKernelString(raw.Executable[:])
		if executable == "" ||
			(!terminated && raw.Flags&observerbpf.ProcessFlagExecutableTruncated == 0) ||
			!clean || !utf8.ValidString(executable) {
			return Event{}, fmt.Errorf("%w: executable", ErrKernelProcessMetadata)
		}
		event.Executable = executable
		for index := uint32(0); index < raw.Argc; index++ {
			argument, argumentTerminated, argumentClean := fixedKernelString(raw.Argv[index][:])
			if !argumentTerminated &&
				raw.Flags&observerbpf.ProcessFlagArgvTruncated == 0 {
				return Event{}, fmt.Errorf("%w: argv[%d]", ErrKernelProcessMetadata, index)
			}
			if !argumentClean || !utf8.ValidString(argument) {
				return Event{}, fmt.Errorf("%w: argv[%d]", ErrKernelProcessMetadata, index)
			}
			event.Argv = append(event.Argv, argument)
		}
		for index := raw.Argc; index < observerbpf.ProcessMaxArguments; index++ {
			if !allZero(raw.Argv[index][:]) {
				return Event{}, ErrInvalidEvent
			}
		}
		event.Identity = workloadtypes.GuestIdentity{UID: raw.UID, GID: raw.GID}
		event.Limitations = processLimitations(raw.Flags)
		// CWD cannot be read safely from /proc after ring-buffer delivery: a
		// short-lived PID may already have been reused. Keep it unknown until a
		// same-event kernel resolver can prove it.
		event.Limitations = append(event.Limitations, "cwd-unavailable")
		sort.Strings(event.Limitations)
	case observerbpf.ProcessEventExit:
		if raw.Flags & ^uint32(
			observerbpf.ProcessFlagStateUnavailable|
				observerbpf.ProcessFlagExitUnavailable,
		) != 0 || raw.Argc != 0 ||
			raw.ParentPID != 0 || raw.ParentExecSequence != 0 ||
			!zeroKernelProcessText(raw) {
			return Event{}, ErrInvalidEvent
		}
		event.Kind = EventExit
		event.Limitations = processLimitations(raw.Flags)
		if raw.Flags&observerbpf.ProcessFlagExitUnavailable != 0 {
			if raw.ExitCode != 0 || raw.Signal != 0 {
				return Event{}, ErrInvalidEvent
			}
		} else if raw.Signal != 0 {
			event.Signal = raw.Signal
		} else {
			code := int(raw.ExitCode)
			event.ExitCode = &code
		}
	default:
		return Event{}, ErrInvalidEvent
	}
	return event, nil
}

func fixedKernelString(value []byte) (string, bool, bool) {
	if index := bytes.IndexByte(value, 0); index >= 0 {
		return string(value[:index]), true, allZero(value[index+1:])
	}
	return string(value), false, true
}

func zeroKernelProcessText(raw observerbpf.RawProcessEvent) bool {
	if !allZero(raw.Executable[:]) {
		return false
	}
	for index := range raw.Argv {
		if !allZero(raw.Argv[index][:]) {
			return false
		}
	}
	return true
}

func allZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}

func processLimitations(flags uint32) []string {
	result := make([]string, 0, 3)
	if flags&observerbpf.ProcessFlagArgvTruncated != 0 {
		result = append(result, "argv-truncated")
	}
	if flags&observerbpf.ProcessFlagArgvUnavailable != 0 {
		result = append(result, "argv-unavailable")
	}
	if flags&observerbpf.ProcessFlagExecutableTruncated != 0 {
		result = append(result, "executable-truncated")
	}
	if flags&observerbpf.ProcessFlagExitUnavailable != 0 {
		result = append(result, "exit-unavailable")
	}
	if flags&observerbpf.ProcessFlagStateUnavailable != 0 {
		result = append(result, "state-unavailable")
	}
	sort.Strings(result)
	return result
}
