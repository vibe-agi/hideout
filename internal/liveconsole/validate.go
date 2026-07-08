package liveconsole

import (
	"errors"
	"fmt"
)

func ValidateEvent(ev Event) error {
	if ev.Version != EventVersion {
		return fmt.Errorf("unsupported event version %q", ev.Version)
	}
	if ev.Seq < 0 {
		return errors.New("event seq must be non-negative")
	}
	switch ev.Kind {
	case KindEnvironment:
		return require(ev.Payload.ID, "environment.id")
	case KindSession:
		return require(ev.Payload.ID, "session.id")
	case KindBackground:
		if err := require(ev.Payload.ID, "background.id"); err != nil {
			return err
		}
		if err := require(ev.Payload.Op, "background.op"); err != nil {
			return err
		}
		return require(ev.Payload.Status, "background.status")
	case KindAudit:
		if err := require(ev.Payload.Action, "audit.action"); err != nil {
			return err
		}
		return require(ev.Payload.Decision, "audit.decision")
	case KindExport:
		return require(ev.Payload.Status, "export.status")
	case KindCleanup:
		return require(ev.Payload.Status, "cleanup.status")
	case KindTerminal:
		return require(ev.Payload.Reason, "terminal.reason")
	default:
		return nil
	}
}

func require(value, field string) error {
	if value == "" {
		return fmt.Errorf("missing required field %s", field)
	}
	return nil
}
