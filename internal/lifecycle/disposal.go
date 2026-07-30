package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	DisposalIntentSchema               = "hideout.disposal-intent/v1"
	DisposalAuthorityRunRM             = "run-rm"
	DisposalAuthorityEnvironmentClean  = "environment-clean"
	DisposalAuthorityEnvironmentDelete = "environment-delete"

	DisposalStatePlanned          = "planned"
	DisposalStateBackendAbsent    = "backend-absent"
	DisposalStateMetadataCleaning = "metadata-cleaning"
	DisposalStateBlocked          = "blocked"

	DisposalReasonRecordRetentionFailed       = "record-retention-failed"
	DisposalReasonBackendObservationUnproved  = "backend-observation-unproved"
	DisposalReasonBackendCleanupFailed        = "backend-cleanup-failed"
	DisposalReasonBackendAbsenceUnproved      = "backend-absence-unproved"
	DisposalReasonBackendCheckpointFailed     = "backend-absence-checkpoint-failed"
	DisposalReasonOwnerMetadataUnproved       = "owner-metadata-unprovable"
	DisposalReasonOwnerMetadataCleanupFailed  = "owner-metadata-cleanup-failed"
	DisposalReasonRuntimeCleanupFailed        = "runtime-cleanup-failed"
	DisposalReasonGatewayCleanupFailed        = "gateway-cleanup-failed"
	DisposalReasonActivityCleanupFailed       = "activity-cleanup-failed"
	DisposalReasonMetadataCheckpointFailed    = "metadata-checkpoint-failed"
	DisposalReasonRecordRemovalFailed         = "record-removal-failed"
	DisposalReasonJournalRemovalFailed        = "journal-removal-failed"
	DisposalReasonBackendProviderUnavailable  = "backend-provider-unavailable"
	DisposalReasonMissingRecordBackendPresent = "missing-record-backend-not-absent"
	DisposalReasonMissingRecordStateInvalid   = "missing-record-intent-state-invalid"
)

var disposalReasonCodes = []string{
	DisposalReasonRecordRetentionFailed,
	DisposalReasonBackendObservationUnproved,
	DisposalReasonBackendCleanupFailed,
	DisposalReasonBackendAbsenceUnproved,
	DisposalReasonBackendCheckpointFailed,
	DisposalReasonOwnerMetadataUnproved,
	DisposalReasonOwnerMetadataCleanupFailed,
	DisposalReasonRuntimeCleanupFailed,
	DisposalReasonGatewayCleanupFailed,
	DisposalReasonActivityCleanupFailed,
	DisposalReasonMetadataCheckpointFailed,
	DisposalReasonRecordRemovalFailed,
	DisposalReasonJournalRemovalFailed,
	DisposalReasonBackendProviderUnavailable,
	DisposalReasonMissingRecordBackendPresent,
	DisposalReasonMissingRecordStateInvalid,
}

// DisposalIntent is durable coordination evidence. It binds one previously
// validated removal authority to an exact record digest, backend instance, and
// optional activity-session identity. It does not itself perform or broaden
// backend cleanup.
type DisposalIntent struct {
	Schema            string    `json:"schema"`
	Authority         string    `json:"authority"`
	Backend           string    `json:"backend"`
	InstanceName      string    `json:"instanceName"`
	RecordDigest      string    `json:"recordDigest"`
	ActivitySessionID string    `json:"activitySessionId,omitempty"`
	Generation        uint64    `json:"generation"`
	State             string    `json:"state"`
	RequestedAt       time.Time `json:"requestedAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	ReasonCode        string    `json:"reasonCode,omitempty"`
}

// DisposalRequest carries only immutable identity proof. It deliberately has
// no backend handle or callback: lifecycle coordinates destructive admission,
// while Manager remains the cleanup authority.
type DisposalRequest struct {
	EnvironmentID     string
	Authority         string
	Backend           string
	InstanceName      string
	RecordDigest      string
	ActivitySessionID string
	Generation        uint64
}

// DisposalCoordinator is the narrow lifecycle protocol injected into Manager.
// Completion removes lifecycle metadata only; the environment record remains
// Manager-owned and is removed last.
type DisposalCoordinator interface {
	BeginDisposal(context.Context, DisposalRequest) (DisposalIntent, error)
	AdvanceDisposal(context.Context, string, string, string) error
	BlockDisposal(context.Context, string, string, string) error
	CompleteDisposalMetadata(context.Context, string, string) error
}

func (intent DisposalIntent) Validate(journalGeneration uint64) error {
	if intent.Schema != DisposalIntentSchema || !validDisposalAuthority(intent.Authority) {
		return errors.New("lifecycle disposal authority is invalid")
	}
	if !idPattern.MatchString(intent.Backend) || !idPattern.MatchString(intent.InstanceName) {
		return errors.New("lifecycle disposal backend identity is invalid")
	}
	if intent.ActivitySessionID != "" && !idPattern.MatchString(intent.ActivitySessionID) {
		return errors.New("lifecycle disposal activity identity is invalid")
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
		if !validDisposalReasonCode(intent.ReasonCode) {
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

func validDisposalReasonCode(value string) bool {
	for _, candidate := range disposalReasonCodes {
		if value == candidate {
			return true
		}
	}
	return false
}

func (request DisposalRequest) validate() error {
	if !idPattern.MatchString(request.EnvironmentID) ||
		!validDisposalAuthority(request.authority()) ||
		!idPattern.MatchString(request.Backend) ||
		!idPattern.MatchString(request.InstanceName) ||
		!lowerHex(request.RecordDigest, 64) ||
		request.ActivitySessionID != "" && !idPattern.MatchString(request.ActivitySessionID) {
		return errors.New("lifecycle disposal request identity is invalid")
	}
	return nil
}

func (request DisposalRequest) authority() string {
	if request.Authority == "" {
		return DisposalAuthorityRunRM
	}
	return request.Authority
}

func validDisposalAuthority(value string) bool {
	return value == DisposalAuthorityRunRM ||
		value == DisposalAuthorityEnvironmentClean ||
		value == DisposalAuthorityEnvironmentDelete
}
