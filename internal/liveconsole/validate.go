package liveconsole

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/manager"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

var (
	daemonInstancePattern = regexp.MustCompile(`^daemon_[A-Za-z0-9_-]{1,124}$`)
	projectionCodePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	riskIDPattern         = regexp.MustCompile(`^risk_[A-Za-z0-9_-]{8,124}$`)
	activityRefPattern    = regexp.MustCompile(`^act_[A-Za-z0-9_-]{8,124}$`)
	operationRefPattern   = regexp.MustCompile(`^op_[A-Za-z0-9_-]{8,124}$`)
)

func ValidateEvent(ev Event) error {
	switch ev.Version {
	case EventVersion:
		return validateEventV1(ev)
	case EventVersionV2:
		return validateEventV2(ev)
	default:
		return fmt.Errorf("unsupported event version %q", ev.Version)
	}
}

func validateEventV1(ev Event) error {
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

func validateEventV2(ev Event) error {
	if !daemonInstancePattern.MatchString(ev.InstanceID) {
		return errors.New("event instanceId is invalid")
	}
	if ev.CredentialGeneration == 0 {
		return errors.New("event credentialGeneration must be positive")
	}
	if !projectionCodePattern.MatchString(ev.Kind) {
		return errors.New("event kind is invalid")
	}
	if ev.Phase != "" && !projectionCodePattern.MatchString(ev.Phase) {
		return errors.New("event phase is invalid")
	}
	if ev.Kind == KindTerminal {
		if ev.Seq != 0 {
			return errors.New("terminal event must not consume broadcast sequence")
		}
	} else if ev.Seq <= 0 {
		return errors.New("broadcast event seq must be positive")
	}
	if ev.Entity.Kind != "" && !projectionCodePattern.MatchString(ev.Entity.Kind) {
		return errors.New("event entity kind is invalid")
	}
	for field, value := range map[string]string{
		"entity.id":      ev.Entity.ID,
		"entity.profile": ev.Entity.Profile,
		"entity.session": ev.Entity.Session,
	} {
		if len(value) > 128 || strings.IndexByte(value, 0) >= 0 {
			return fmt.Errorf("%s is invalid", field)
		}
	}

	if !knownEventKind(ev.Kind) {
		if ev.Optional {
			return nil
		}
		return fmt.Errorf("unknown required event kind %q", ev.Kind)
	}
	for _, field := range RequiredPayloadFields(ev.Kind) {
		if err := require(payloadField(ev.Payload, field), ev.Kind+"."+field); err != nil {
			return err
		}
	}
	switch ev.Kind {
	case KindProfile:
		return validateProfileProjection(ev.Payload.ProfileProjection)
	case KindTransition:
		return validateTransitionProjection(ev.Payload.TransitionProjection)
	case KindOperation:
		if ev.Payload.OperationProjection == nil {
			return errors.New("missing operation projection")
		}
		return ev.Payload.OperationProjection.Validate()
	case KindActivity:
		return validateActivityDelta(ev.Payload.ActivityProjection)
	case KindCoverage:
		if len(ev.Payload.CoverageProjection) == 0 || len(ev.Payload.CoverageProjection) > 64 {
			return errors.New("coverage projection is empty or too large")
		}
		for _, interval := range ev.Payload.CoverageProjection {
			if err := interval.Validate(); err != nil {
				return err
			}
		}
	case KindRisk:
		return validateRiskFinding(ev.Payload.RiskProjection)
	case KindCapability:
		return validateCapabilityProjection(ev.Payload.CapabilityProjection)
	}
	return nil
}

func knownEventKind(kind string) bool {
	switch kind {
	case KindProfile, KindTransition, KindOperation, KindEnvironment, KindSession,
		KindWorkspaceView, KindActivity, KindCoverage, KindRisk, KindCapability,
		KindBackground, KindAudit, KindExport, KindCleanup, KindHostFSWrite,
		KindDecision, KindNotice, KindLifecycle, KindTerminal:
		return true
	default:
		return false
	}
}

func validateProfileProjection(projection *manager.ProfileProjection) error {
	if projection == nil {
		return errors.New("missing profile projection")
	}
	if err := projection.Validate(); err != nil {
		return fmt.Errorf("profile projection is invalid: %w", err)
	}
	return nil
}

func validateTransitionProjection(projection *TransitionProjection) error {
	if projection == nil || projection.Profile == "" || len(projection.Profile) > 128 ||
		!operationRefPattern.MatchString(projection.Transition.OperationID) ||
		!projectionCodePattern.MatchString(projection.Transition.Kind) ||
		!projectionCodePattern.MatchString(projection.Transition.Phase) ||
		projection.Transition.StartedAt.IsZero() ||
		len(projection.Transition.Blockers) > 64 {
		return errors.New("transition projection is invalid")
	}
	for _, blocker := range projection.Transition.Blockers {
		if !projectionCodePattern.MatchString(blocker) {
			return errors.New("transition blocker is invalid")
		}
	}
	return nil
}

func validateActivityDelta(projection *ActivityProjectionDelta) error {
	if projection == nil || len(projection.Cursor) > 4096 ||
		len(projection.Profile) > 128 || len(projection.Session) > 128 ||
		len(projection.Counts) > 64 {
		return errors.New("activity projection is invalid")
	}
	seen := make(map[string]struct{}, len(projection.Counts))
	for _, count := range projection.Counts {
		if !projectionCodePattern.MatchString(count.Kind) {
			return errors.New("activity count kind is invalid")
		}
		if _, exists := seen[count.Kind]; exists {
			return errors.New("activity count kind is duplicated")
		}
		seen[count.Kind] = struct{}{}
	}
	return nil
}

func validateRiskFinding(finding *RiskFinding) error {
	if finding == nil ||
		!riskIDPattern.MatchString(finding.ID) ||
		!projectionCodePattern.MatchString(finding.RuleID) ||
		len(finding.RuleVersion) == 0 || len(finding.RuleVersion) > 64 ||
		strings.TrimSpace(finding.RuleVersion) != finding.RuleVersion ||
		strings.IndexByte(finding.RuleVersion, 0) >= 0 ||
		len(finding.Title) == 0 || len(finding.Title) > 256 ||
		len(finding.Explanation) == 0 || len(finding.Explanation) > 2048 ||
		finding.FirstAt.IsZero() || finding.LastAt.Before(finding.FirstAt) ||
		finding.Count == 0 || len(finding.EvidenceRefs) > 256 {
		return errors.New("risk projection is invalid")
	}
	switch finding.Severity {
	case "info", "low", "medium", "high", "critical":
	default:
		return errors.New("risk severity is invalid")
	}
	switch finding.Confidence {
	case "exact", "inferred", "limited":
	default:
		return errors.New("risk confidence is invalid")
	}
	switch finding.PolicyStatus {
	case "allowed", "denied", "not-evaluated":
	default:
		return errors.New("risk policy status is invalid")
	}
	for _, ref := range finding.EvidenceRefs {
		if !activityRefPattern.MatchString(ref) {
			return errors.New("risk evidence reference is invalid")
		}
	}
	if finding.NextAction != "" && !projectionCodePattern.MatchString(finding.NextAction) {
		return errors.New("risk next action is invalid")
	}
	return nil
}

func validateCapabilityProjection(capability *CapabilityProjection) error {
	if capability == nil || !projectionCodePattern.MatchString(capability.ID) ||
		len(capability.Provider) > 128 || len(capability.Reason) > 1024 ||
		len(capability.ActionRefs) > 64 {
		return errors.New("capability projection is invalid")
	}
	switch capability.Status {
	case workloadtypes.CoverageAvailable, workloadtypes.CoveragePartial, workloadtypes.CoverageUnavailable:
	default:
		return errors.New("capability status is invalid")
	}
	for _, action := range capability.ActionRefs {
		if !projectionCodePattern.MatchString(action) {
			return errors.New("capability action reference is invalid")
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
	case "attachmentId":
		return payload.AttachmentID
	case "session":
		return payload.Session
	case "environmentId":
		return payload.EnvironmentID
	case "workspaceId":
		return payload.WorkspaceID
	case "workspaceLabel":
		return payload.WorkspaceLabel
	case "guestWorkspace":
		return payload.GuestWorkspace
	case "workspaceTransport":
		return payload.WorkspaceTransport
	case "workspaceViewState":
		return string(payload.WorkspaceViewState)
	case "lifecycle":
		if payload.Lifecycle != nil && payload.Lifecycle.Schema == lifecycle.StatusSchema && payload.Lifecycle.EnvironmentID != "" {
			return payload.Lifecycle.EnvironmentID
		}
		return ""
	default:
		return ""
	}
}
