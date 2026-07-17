package liveconsole

import (
	"errors"
	"fmt"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

func ValidateEvent(ev Event) error {
	if ev.Version != EventVersion {
		return fmt.Errorf("unsupported event version %q", ev.Version)
	}
	if ev.Seq < 0 {
		return errors.New("event seq must be non-negative")
	}
	for _, field := range RequiredPayloadFields(ev.Kind) {
		if err := require(payloadField(ev.Payload, field), ev.Kind+"."+field); err != nil {
			return err
		}
	}
	return nil
}

func require(value, field string) error {
	if value == "" {
		return fmt.Errorf("missing required field %s", field)
	}
	return nil
}

func payloadField(payload EventPayload, field string) string {
	switch field {
	case "id":
		return payload.ID
	case "op":
		return payload.Op
	case "status":
		return payload.Status
	case "action":
		return payload.Action
	case "decision":
		return payload.Decision
	case "reason":
		return payload.Reason
	case "decisionId":
		return payload.DecisionID
	case "operationId":
		return payload.OperationID
	case "recordKind":
		return payload.RecordKind
	case "noticeId":
		return payload.NoticeID
	case "lifecycle":
		if payload.Lifecycle != nil && payload.Lifecycle.Schema == lifecycle.StatusSchema && payload.Lifecycle.EnvironmentID != "" {
			return payload.Lifecycle.EnvironmentID
		}
		return ""
	default:
		return ""
	}
}
