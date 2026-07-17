package backend

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

type LifecycleState string

const (
	LifecycleRunning LifecycleState = "running"
	LifecycleStopped LifecycleState = "stopped"
	LifecycleAbsent  LifecycleState = "absent"
	LifecycleUnknown LifecycleState = "unknown"
)

var lifecycleBootIDPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type LifecycleObservation struct {
	State        LifecycleState `json:"state"`
	InstanceName string         `json:"instanceName,omitempty"`
	BootID       string         `json:"bootId,omitempty"`
	ObservedAt   time.Time      `json:"observedAt"`
	ReasonCode   string         `json:"reasonCode,omitempty"`
}

func (o LifecycleObservation) Validate() error {
	if o.ObservedAt.IsZero() {
		return errors.New("lifecycle observation time is required")
	}
	switch o.State {
	case LifecycleRunning:
		if strings.TrimSpace(o.InstanceName) == "" || !lifecycleBootIDPattern.MatchString(o.BootID) || o.ReasonCode != "" {
			return errors.New("running lifecycle observation is invalid")
		}
	case LifecycleStopped, LifecycleAbsent:
		if strings.TrimSpace(o.InstanceName) == "" || o.BootID != "" || o.ReasonCode != "" {
			return errors.New("terminal lifecycle observation is invalid")
		}
	case LifecycleUnknown:
		if strings.TrimSpace(o.ReasonCode) == "" || len(o.ReasonCode) > 128 || o.BootID != "" {
			return errors.New("unknown lifecycle observation is invalid")
		}
	default:
		return errors.New("unknown lifecycle observation state")
	}
	return nil
}

type LifecycleObserver interface {
	ObserveLifecycle(ctx context.Context, instanceName string) LifecycleObservation
}

// SessionAbsenceProver independently proves that no target process carrying
// the exact session identity remains in a running backend incarnation. It is
// intentionally optional: callers must stay fail closed when a backend cannot
// provide this proof.
type SessionAbsenceProver interface {
	ProveSessionAbsent(ctx context.Context, instanceName, sessionID string) error
}
