package sessionwire

import (
	"errors"
	"testing"
)

func TestFrameCatalogDirections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		frameType Type
		allowed   []Direction
	}{
		{TypeHello, []Direction{ClientToDaemon}},
		{TypeHelloAccepted, []Direction{DaemonToClient}},
		{TypeRunRequest, []Direction{ClientToDaemon}},
		{TypeConfirm, []Direction{ClientToDaemon}},
		{TypeReview, []Direction{DaemonToClient}},
		{TypeStarted, []Direction{DaemonToClient}},
		{TypeStdin, []Direction{ClientToDaemon, DaemonToSupervisor}},
		{TypeStdinEOF, []Direction{ClientToDaemon, DaemonToSupervisor}},
		{TypeResize, []Direction{ClientToDaemon, DaemonToSupervisor}},
		{TypeSignal, []Direction{ClientToDaemon, DaemonToSupervisor}},
		{TypeCancel, []Direction{ClientToDaemon, DaemonToSupervisor}},
		{TypeRenew, []Direction{ClientToDaemon}},
		{TypeTerminal, []Direction{DaemonToClient, SupervisorToDaemon}},
		{TypeStdout, []Direction{DaemonToClient, SupervisorToDaemon}},
		{TypeStderr, []Direction{DaemonToClient, SupervisorToDaemon}},
		{TypeNotice, []Direction{DaemonToClient}},
		{TypeError, []Direction{DaemonToClient}},
		{TypeCompletion, []Direction{DaemonToClient, SupervisorToDaemon}},
		{TypeSupervisorStart, []Direction{DaemonToSupervisor}},
		{TypeSupervisorReady, []Direction{SupervisorToDaemon}},
		{TypeSupervisorCommit, []Direction{DaemonToSupervisor}},
		{TypeHeartbeat, []Direction{DaemonToSupervisor}},
		{TypeSupervisorError, []Direction{SupervisorToDaemon}},
	}
	allDirections := []Direction{ClientToDaemon, DaemonToClient, DaemonToSupervisor, SupervisorToDaemon}
	for _, test := range tests {
		for _, direction := range allDirections {
			err := ValidateDirection(direction, test.frameType)
			wantAllowed := containsDirection(test.allowed, direction)
			if wantAllowed && err != nil {
				t.Errorf("%s in %s: unexpected error: %v", test.frameType, direction, err)
			}
			if !wantAllowed && !errors.Is(err, ErrWrongDirection) {
				t.Errorf("%s in %s: error=%v, want ErrWrongDirection", test.frameType, direction, err)
			}
		}
	}
}

func TestUnknownFrameClassification(t *testing.T) {
	t.Parallel()

	if err := ValidateDirection(ClientToDaemon, Type(0x7f)); !errors.Is(err, ErrUnknownMandatoryFrame) {
		t.Fatalf("unknown mandatory error=%v", err)
	}
	if err := ValidateDirection(ClientToDaemon, Type(0x80)); err != nil {
		t.Fatalf("optional extension rejected: %v", err)
	}
	if !Type(0xff).IsExtension() {
		t.Fatal("high-bit frame was not classified as an extension")
	}
}

func TestInvalidDirectionValueFailsClosed(t *testing.T) {
	t.Parallel()

	if err := ValidateDirection(Direction(0), TypeHello); !errors.Is(err, ErrInvalidDirection) {
		t.Fatalf("error=%v, want ErrInvalidDirection", err)
	}
}

func containsDirection(directions []Direction, want Direction) bool {
	for _, direction := range directions {
		if direction == want {
			return true
		}
	}
	return false
}
