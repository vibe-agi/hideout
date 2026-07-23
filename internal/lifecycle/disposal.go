package lifecycle

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DisposalIntentSchema   = "hideout.disposal-intent/v1"
	DisposalAuthorityRunRM = "run-rm"

	DisposalStatePlanned          = "planned"
	DisposalStateBackendAbsent    = "backend-absent"
	DisposalStateMetadataCleaning = "metadata-cleaning"
	DisposalStateBlocked          = "blocked"
)

// DisposalIntent is durable coordination evidence. It binds previously
// validated --rm authority to one exact record digest and backend instance; it
// does not itself perform or broaden backend cleanup.
type DisposalIntent struct {
	Schema       string    `json:"schema"`
	Authority    string    `json:"authority"`
	Backend      string    `json:"backend"`
	InstanceName string    `json:"instanceName"`
	RecordDigest string    `json:"recordDigest"`
	Generation   uint64    `json:"generation"`
	State        string    `json:"state"`
	RequestedAt  time.Time `json:"requestedAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	ReasonCode   string    `json:"reasonCode,omitempty"`
}

func (intent DisposalIntent) Validate(journalGeneration uint64) error {
	if intent.Schema != DisposalIntentSchema || intent.Authority != DisposalAuthorityRunRM {
		return errors.New("lifecycle disposal authority is invalid")
	}
	if !idPattern.MatchString(intent.Backend) || !idPattern.MatchString(intent.InstanceName) {
		return errors.New("lifecycle disposal backend identity is invalid")
	}
	if !lowerHex(intent.RecordDigest, 64) {
		return errors.New("lifecycle disposal record digest is invalid")
	}
	if intent.Generation == 0 || intent.Generation != journalGeneration {
		return errors.New("lifecycle disposal generation mismatch")
	}
	if intent.RequestedAt.IsZero() || intent.UpdatedAt.IsZero() ||
		intent.RequestedAt.After(intent.UpdatedAt) ||
		!isUTC(intent.RequestedAt) || !isUTC(intent.UpdatedAt) {
		return errors.New("lifecycle disposal timestamps are invalid")
	}
	switch intent.State {
	case DisposalStatePlanned, DisposalStateBackendAbsent, DisposalStateMetadataCleaning:
		if intent.ReasonCode != "" {
			return errors.New("active lifecycle disposal intent cannot carry a reason")
		}
	case DisposalStateBlocked:
		if !idPattern.MatchString(intent.ReasonCode) {
			return errors.New("blocked lifecycle disposal intent requires a reason")
		}
	default:
		return errors.New("lifecycle disposal state is invalid")
	}
	return nil
}

func ValidateDisposalTransition(from, to string) error {
	allowed := map[string]map[string]bool{
		DisposalStatePlanned: {
			DisposalStatePlanned:       true,
			DisposalStateBackendAbsent: true,
			DisposalStateBlocked:       true,
		},
		DisposalStateBackendAbsent: {
			DisposalStateBackendAbsent:    true,
			DisposalStateMetadataCleaning: true,
			DisposalStateBlocked:          true,
		},
		DisposalStateMetadataCleaning: {
			DisposalStateMetadataCleaning: true,
			DisposalStateBlocked:          true,
		},
		DisposalStateBlocked: {
			DisposalStateBlocked: true,
			DisposalStatePlanned: true,
		},
	}
	if !allowed[from][to] {
		return fmt.Errorf("invalid lifecycle disposal transition %q -> %q", from, to)
	}
	return nil
}

func lowerHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range value {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func isUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}
