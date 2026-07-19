package sessionwire

import (
	"errors"
	"fmt"
)

const (
	// Version is negotiated by the hello and supervisor-start controls. The
	// binary envelope stays fixed at one type byte and one uint32 length.
	Version = 1

	Protocol           = "hideout.session-wire/v1"
	SupervisorProtocol = "hideout.guest-supervisor/v1"

	HeaderSize            = 5
	MaxPayloadSize uint32 = 64 << 10
)

var (
	ErrInvalidDirection      = errors.New("session wire direction is invalid")
	ErrWrongDirection        = errors.New("session wire frame is invalid for direction")
	ErrUnknownMandatoryFrame = errors.New("session wire mandatory frame type is unknown")
	ErrPayloadTooLarge       = errors.New("session wire payload exceeds limit")
	ErrTruncatedFrame        = errors.New("session wire frame is truncated")
	ErrInvalidControl        = errors.New("session wire control payload is invalid")
)

type Direction uint8

const (
	ClientToDaemon Direction = iota + 1
	DaemonToClient
	DaemonToSupervisor
	SupervisorToDaemon
)

func (d Direction) String() string {
	switch d {
	case ClientToDaemon:
		return "client-to-daemon"
	case DaemonToClient:
		return "daemon-to-client"
	case DaemonToSupervisor:
		return "daemon-to-supervisor"
	case SupervisorToDaemon:
		return "supervisor-to-daemon"
	default:
		return fmt.Sprintf("direction(%d)", uint8(d))
	}
}

func (d Direction) valid() bool {
	return d >= ClientToDaemon && d <= SupervisorToDaemon
}

type Type byte

const (
	TypeHello Type = 0x01 + iota
	TypeHelloAccepted
	TypeRunRequest
	TypeConfirm
	TypeReview
	TypeStarted
	TypeStdin
	TypeStdinEOF
	TypeResize
	TypeSignal
	TypeCancel
	TypeRenew
	TypeTerminal
	TypeStdout
	TypeStderr
	TypeNotice
	TypeError
	TypeCompletion
	TypeSupervisorStart
	TypeSupervisorReady
	TypeSupervisorCommit
	TypeHeartbeat
	TypeSupervisorError
)

func (t Type) String() string {
	switch t {
	case TypeHello:
		return "hello"
	case TypeHelloAccepted:
		return "hello-accepted"
	case TypeRunRequest:
		return "run-request"
	case TypeConfirm:
		return "confirm"
	case TypeReview:
		return "review"
	case TypeStarted:
		return "started"
	case TypeStdin:
		return "stdin"
	case TypeStdinEOF:
		return "stdin-eof"
	case TypeResize:
		return "resize"
	case TypeSignal:
		return "signal"
	case TypeCancel:
		return "cancel"
	case TypeRenew:
		return "renew"
	case TypeTerminal:
		return "terminal"
	case TypeStdout:
		return "stdout"
	case TypeStderr:
		return "stderr"
	case TypeNotice:
		return "notice"
	case TypeError:
		return "error"
	case TypeCompletion:
		return "completion"
	case TypeSupervisorStart:
		return "supervisor-start"
	case TypeSupervisorReady:
		return "supervisor-ready"
	case TypeSupervisorCommit:
		return "supervisor-commit"
	case TypeHeartbeat:
		return "heartbeat"
	case TypeSupervisorError:
		return "supervisor-error"
	default:
		if t.IsExtension() {
			return fmt.Sprintf("extension-0x%02x", byte(t))
		}
		return fmt.Sprintf("mandatory-0x%02x", byte(t))
	}
}

func (t Type) IsExtension() bool {
	return byte(t)&0x80 != 0
}

func (t Type) IsKnown() bool {
	return t >= TypeHello && t <= TypeSupervisorError
}

func (t Type) IsData() bool {
	switch t {
	case TypeStdin, TypeTerminal, TypeStdout, TypeStderr:
		return true
	default:
		return false
	}
}

func (t Type) IsControl() bool {
	return t.IsKnown() && !t.IsData()
}

func (t Type) allowed(direction Direction) bool {
	switch t {
	case TypeHello, TypeRunRequest, TypeConfirm, TypeRenew:
		return direction == ClientToDaemon
	case TypeHelloAccepted, TypeReview, TypeStarted, TypeNotice, TypeError:
		return direction == DaemonToClient
	case TypeSupervisorStart, TypeSupervisorCommit, TypeHeartbeat:
		return direction == DaemonToSupervisor
	case TypeSupervisorReady, TypeSupervisorError:
		return direction == SupervisorToDaemon
	case TypeStdin, TypeStdinEOF, TypeResize, TypeSignal, TypeCancel:
		return direction == ClientToDaemon || direction == DaemonToSupervisor
	case TypeTerminal, TypeStdout, TypeStderr, TypeCompletion:
		return direction == DaemonToClient || direction == SupervisorToDaemon
	default:
		return false
	}
}

func ValidateDirection(direction Direction, frameType Type) error {
	if !direction.valid() {
		return fmt.Errorf("%w: %s", ErrInvalidDirection, direction)
	}
	if frameType.IsExtension() {
		return nil
	}
	if !frameType.IsKnown() {
		return fmt.Errorf("%w: 0x%02x", ErrUnknownMandatoryFrame, byte(frameType))
	}
	if !frameType.allowed(direction) {
		return fmt.Errorf("%w: %s cannot carry %s", ErrWrongDirection, direction, frameType)
	}
	return nil
}

type Frame struct {
	Type    Type
	Payload []byte
}

func validateFrame(direction Direction, frame Frame, limit uint32) error {
	if err := ValidateDirection(direction, frame.Type); err != nil {
		return err
	}
	if uint64(len(frame.Payload)) > uint64(limit) {
		return fmt.Errorf("%w: type=%s size=%d limit=%d", ErrPayloadTooLarge, frame.Type, len(frame.Payload), limit)
	}
	if frame.Type.IsExtension() || frame.Type.IsData() {
		return nil
	}
	if err := validateControlPayload(frame.Type, frame.Payload); err != nil {
		return fmt.Errorf("%w: type=%s: %v", ErrInvalidControl, frame.Type, err)
	}
	return nil
}
