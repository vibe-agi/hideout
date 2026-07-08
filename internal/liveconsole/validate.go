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
	case KindHostFSWrite:
		if err := require(ev.Payload.DecisionID, "hostfs-write.decisionId"); err != nil {
			return err
		}
		if err := require(ev.Payload.OperationID, "hostfs-write.operationId"); err != nil {
			return err
		}
		return require(ev.Payload.Status, "hostfs-write.status")
	case KindDecision:
		if err := require(ev.Payload.DecisionID, "decision.decisionId"); err != nil {
			return err
		}
		if err := require(ev.Payload.RecordKind, "decision.kind"); err != nil {
			return err
		}
		return require(ev.Payload.Status, "decision.status")
	case KindNotice:
		if err := require(ev.Payload.NoticeID, "notice.noticeId"); err != nil {
			return err
		}
		if err := require(ev.Payload.RecordKind, "notice.kind"); err != nil {
			return err
		}
		return require(ev.Payload.Status, "notice.status")
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
