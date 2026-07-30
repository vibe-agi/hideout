package types

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	OwnerReusableEnvironment = "reusable-environment"
	OwnerDisposableSession   = "disposable-session"

	ActivityRecordSchema   = "hideout.activity-record.v1"
	CoverageIntervalSchema = "hideout.coverage-interval.v1"
	WorkloadBoundarySchema = "hideout.workload-boundary.v1"
	ExecutionSchema        = "hideout.execution.v1"

	ActivityProcess    = "process"
	ActivityFile       = "file"
	ActivityConnection = "connection"
	ActivityDNS        = "dns"
	ActivityRisk       = "risk"
	ActivityCoverage   = "coverage"

	AttributionExact    = "exact"
	AttributionInferred = "inferred"
	AttributionMediated = "mediated"
	AttributionUnknown  = "unknown"

	OutcomeSucceeded = "succeeded"
	OutcomeFailed    = "failed"
	OutcomeDenied    = "denied"
	OutcomeCancelled = "cancelled"
	OutcomeUnknown   = "unknown"

	RedactionPending = "pending"
	RedactionPassed  = "passed"

	SubsystemProcess = "process"
	SubsystemFile    = "file"
	SubsystemNetwork = "network"
	SubsystemDNS     = "dns"

	CoverageAvailable   = "Available"
	CoveragePartial     = "Partial"
	CoverageUnavailable = "Unavailable"

	BoundaryCreating  = "creating"
	BoundaryObserving = "observing"
	BoundaryReady     = "ready"
	BoundaryDraining  = "draining"
	BoundaryEmpty     = "empty"
	BoundaryRemoved   = "removed"
	BoundaryUnproved  = "unproved"
)

var (
	ErrInvalidActivityOwner     = errors.New("activity owner is invalid")
	ErrInvalidExecutionIdentity = errors.New("execution identity is invalid")
	ErrOwnerMismatch            = errors.New("activity owner does not match requested owner")
	ErrInvalidActivity          = errors.New("activity record is invalid")
	ErrInvalidCoverage          = errors.New("coverage interval is invalid")
	ErrFalseAvailableCoverage   = errors.New("coverage cannot be Available across known loss")

	environmentIDPattern = regexp.MustCompile(`^env_[A-Za-z0-9_-]{1,124}$`)
	sessionIDPattern     = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{1,124}$`)
	activityIDPattern    = regexp.MustCompile(`^act_[A-Za-z0-9_-]{8,124}$`)
	executionIDPattern   = regexp.MustCompile(`^exec_[A-Za-z0-9_-]{8,124}$`)
	coverageIDPattern    = regexp.MustCompile(`^cov_[A-Za-z0-9_-]{8,124}$`)
	operationPattern     = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
)

type ActivityOwner struct {
	Kind                 string `json:"kind"`
	EnvironmentID        string `json:"environmentId,omitempty"`
	SessionID            string `json:"sessionId,omitempty"`
	Backend              string `json:"backend"`
	BackendIncarnationID string `json:"backendIncarnationId"`

	// GuestBootID is deliberately never serialized and must remain empty. Guest
	// boot identity belongs to an execution/collector generation, not retention
	// ownership. Keeping this guard catches accidental coupling in Go callers.
	GuestBootID string `json:"-"`
}

func NewReusableOwner(environmentID, backend, backendIncarnationID string) (ActivityOwner, error) {
	owner := ActivityOwner{
		Kind:                 OwnerReusableEnvironment,
		EnvironmentID:        environmentID,
		Backend:              backend,
		BackendIncarnationID: backendIncarnationID,
	}
	return owner, owner.Validate()
}

func NewDisposableOwner(sessionID, backend, backendIncarnationID string) (ActivityOwner, error) {
	owner := ActivityOwner{
		Kind:                 OwnerDisposableSession,
		SessionID:            sessionID,
		Backend:              backend,
		BackendIncarnationID: backendIncarnationID,
	}
	return owner, owner.Validate()
}

func (owner ActivityOwner) Validate() error {
	if owner.GuestBootID != "" ||
		!boundedPrintable(owner.Backend, 1, 32) ||
		!boundedPrintable(owner.BackendIncarnationID, 1, 256) {
		return ErrInvalidActivityOwner
	}
	switch owner.Kind {
	case OwnerReusableEnvironment:
		if !environmentIDPattern.MatchString(owner.EnvironmentID) || owner.SessionID != "" {
			return ErrInvalidActivityOwner
		}
	case OwnerDisposableSession:
		if !sessionIDPattern.MatchString(owner.SessionID) || owner.EnvironmentID != "" {
			return ErrInvalidActivityOwner
		}
	default:
		return ErrInvalidActivityOwner
	}
	return nil
}

func (owner ActivityOwner) Equal(other ActivityOwner) bool {
	return owner.Kind == other.Kind &&
		owner.EnvironmentID == other.EnvironmentID &&
		owner.SessionID == other.SessionID &&
		owner.Backend == other.Backend &&
		owner.BackendIncarnationID == other.BackendIncarnationID &&
		owner.GuestBootID == "" && other.GuestBootID == ""
}

func (owner ActivityOwner) Key() string {
	if err := owner.Validate(); err != nil {
		return ""
	}
	data, err := json.Marshal(owner)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(append([]byte("hideout.activity-owner/v1\x00"), data...))
	return "owner_" + base64.RawURLEncoding.EncodeToString(sum[:18])
}

type ExecutionIdentityInput struct {
	Owner              ActivityOwner
	SessionID          string
	GuestBootID        string
	ObserverGeneration uint64
	PID                uint32
	ExecSequence       uint64
	StartedAtMonoNS    uint64
}

func NewExecutionID(input ExecutionIdentityInput) (string, error) {
	if err := input.Owner.Validate(); err != nil ||
		!sessionIDPattern.MatchString(input.SessionID) ||
		!boundedPrintable(input.GuestBootID, 1, 128) ||
		input.ObserverGeneration == 0 ||
		input.PID == 0 || input.PID > 4194304 ||
		input.ExecSequence == 0 ||
		input.StartedAtMonoNS == 0 {
		return "", ErrInvalidExecutionIdentity
	}
	payload := struct {
		Owner              ActivityOwner `json:"owner"`
		SessionID          string        `json:"sessionId"`
		GuestBootID        string        `json:"guestBootId"`
		ObserverGeneration uint64        `json:"observerGeneration"`
		PID                uint32        `json:"pid"`
		ExecSequence       uint64        `json:"execSequence"`
		StartedAtMonoNS    uint64        `json:"startedAtMonoNs"`
	}{
		Owner: input.Owner, SessionID: input.SessionID,
		GuestBootID: input.GuestBootID, ObserverGeneration: input.ObserverGeneration,
		PID: input.PID, ExecSequence: input.ExecSequence,
		StartedAtMonoNS: input.StartedAtMonoNS,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("hideout.execution-identity/v1\x00"), data...))
	return "exec_" + base64.RawURLEncoding.EncodeToString(sum[:18]), nil
}

type WorkloadBoundary struct {
	Schema             string        `json:"schema"`
	Owner              ActivityOwner `json:"owner"`
	SessionID          string        `json:"sessionId"`
	CgroupPath         string        `json:"cgroupPath"`
	CgroupID           uint64        `json:"cgroupId"`
	TargetUser         string        `json:"targetUser"`
	State              string        `json:"state"`
	ObserverGeneration uint64        `json:"observerGeneration"`
	GuestBootID        string        `json:"guestBootId"`
	CreatedAtMonoNS    uint64        `json:"createdAtMonoNs"`
}

func (boundary WorkloadBoundary) Validate() error {
	if boundary.Schema != WorkloadBoundarySchema ||
		boundary.Owner.Validate() != nil ||
		!sessionIDPattern.MatchString(boundary.SessionID) ||
		!filepath.IsAbs(boundary.CgroupPath) ||
		!strings.HasPrefix(filepath.Clean(boundary.CgroupPath), "/sys/fs/cgroup/") ||
		boundary.CgroupID == 0 ||
		!boundedPrintable(boundary.TargetUser, 1, 64) ||
		boundary.ObserverGeneration == 0 ||
		!boundedPrintable(boundary.GuestBootID, 1, 128) ||
		boundary.CreatedAtMonoNS == 0 {
		return ErrInvalidExecutionIdentity
	}
	switch boundary.State {
	case BoundaryCreating, BoundaryObserving, BoundaryReady, BoundaryDraining,
		BoundaryEmpty, BoundaryRemoved, BoundaryUnproved:
		return nil
	default:
		return ErrInvalidExecutionIdentity
	}
}

type GuestIdentity struct {
	UID   uint32 `json:"uid"`
	GID   uint32 `json:"gid"`
	User  string `json:"user,omitempty"`
	Group string `json:"group,omitempty"`
}

func (identity GuestIdentity) Validate() error {
	if len(identity.User) > 64 || len(identity.Group) > 64 ||
		containsControl(identity.User) || containsControl(identity.Group) {
		return ErrInvalidActivity
	}
	return nil
}

type Execution struct {
	Schema             string           `json:"schema"`
	ID                 string           `json:"id"`
	Owner              ActivityOwner    `json:"owner"`
	SessionID          string           `json:"sessionId"`
	ParentExecutionID  string           `json:"parentExecutionId,omitempty"`
	GuestBootID        string           `json:"guestBootId"`
	ObserverGeneration uint64           `json:"observerGeneration"`
	PID                uint32           `json:"pid"`
	TID                uint32           `json:"tid"`
	ExecSequence       uint64           `json:"execSequence"`
	StartedAtMonoNS    uint64           `json:"startedAtMonoNs"`
	StartedAt          time.Time        `json:"startedAt"`
	Executable         string           `json:"executable"`
	Argv               []string         `json:"argv"`
	Cwd                string           `json:"cwd,omitempty"`
	Identity           GuestIdentity    `json:"guestIdentity"`
	Limitations        []string         `json:"limitations,omitempty"`
	Exit               *ExitObservation `json:"exit,omitempty"`
}

type ExitObservation struct {
	Code          *int      `json:"code,omitempty"`
	Signal        uint32    `json:"signal,omitempty"`
	AtMonoNS      uint64    `json:"atMonoNs"`
	At            time.Time `json:"at"`
	UnknownReason string    `json:"unknownReason,omitempty"`
}

func (observation ExitObservation) Validate(startedAtMonoNS uint64, startedAt time.Time) error {
	if observation.AtMonoNS < startedAtMonoNS || observation.At.IsZero() ||
		observation.At.Before(startedAt) ||
		(observation.Code == nil && observation.Signal == 0 &&
			!operationPattern.MatchString(observation.UnknownReason)) ||
		(observation.Code != nil && observation.Signal != 0) ||
		(observation.Code != nil && (*observation.Code < 0 || *observation.Code > 255)) ||
		observation.Signal > 128 ||
		(observation.UnknownReason != "" &&
			!operationPattern.MatchString(observation.UnknownReason)) {
		return ErrInvalidExecutionIdentity
	}
	return nil
}

func (execution Execution) Validate() error {
	if execution.Schema != ExecutionSchema ||
		!executionIDPattern.MatchString(execution.ID) ||
		execution.Owner.Validate() != nil ||
		!sessionIDPattern.MatchString(execution.SessionID) ||
		(execution.ParentExecutionID != "" && !executionIDPattern.MatchString(execution.ParentExecutionID)) ||
		!boundedPrintable(execution.GuestBootID, 1, 128) ||
		execution.ObserverGeneration == 0 ||
		execution.PID == 0 || execution.PID > 4194304 ||
		execution.TID == 0 || execution.TID > 4194304 ||
		execution.ExecSequence == 0 || execution.StartedAtMonoNS == 0 ||
		execution.StartedAt.IsZero() ||
		!boundedPrintable(execution.Executable, 1, 4096) ||
		len(execution.Argv) > 1024 ||
		len(execution.Cwd) > 4096 ||
		containsControl(execution.Cwd) ||
		execution.Identity.Validate() != nil {
		return ErrInvalidExecutionIdentity
	}
	if execution.Exit != nil {
		if err := execution.Exit.Validate(execution.StartedAtMonoNS, execution.StartedAt); err != nil {
			return err
		}
	}
	for _, argument := range execution.Argv {
		if len(argument) > 8192 || strings.IndexByte(argument, 0) >= 0 {
			return ErrInvalidExecutionIdentity
		}
	}
	if len(execution.Limitations) > 16 {
		return ErrInvalidExecutionIdentity
	}
	previousLimitation := ""
	for _, limitation := range execution.Limitations {
		if !operationPattern.MatchString(limitation) ||
			limitation <= previousLimitation {
			return ErrInvalidExecutionIdentity
		}
		previousLimitation = limitation
	}
	expected, err := NewExecutionID(ExecutionIdentityInput{
		Owner: execution.Owner, SessionID: execution.SessionID,
		GuestBootID: execution.GuestBootID, ObserverGeneration: execution.ObserverGeneration,
		PID: execution.PID, ExecSequence: execution.ExecSequence,
		StartedAtMonoNS: execution.StartedAtMonoNS,
	})
	if err != nil || expected != execution.ID {
		return ErrInvalidExecutionIdentity
	}
	return nil
}

type ActivityRecord struct {
	Schema          string        `json:"schema"`
	ID              string        `json:"id"`
	Owner           ActivityOwner `json:"owner"`
	SessionID       string        `json:"sessionId"`
	Actor           *Actor        `json:"actor,omitempty"`
	Mediator        *Mediator     `json:"mediator,omitempty"`
	Kind            string        `json:"kind"`
	Operation       string        `json:"operation"`
	Subject         any           `json:"subject"`
	Outcome         Outcome       `json:"outcome"`
	Count           uint64        `json:"count"`
	Bytes           uint64        `json:"bytes,omitempty"`
	FirstAt         time.Time     `json:"firstAt"`
	LastAt          time.Time     `json:"lastAt"`
	FirstSequence   uint64        `json:"firstSequence"`
	LastSequence    uint64        `json:"lastSequence"`
	Attribution     string        `json:"attribution"`
	Truncation      []string      `json:"truncation,omitempty"`
	CoverageID      string        `json:"coverageId"`
	RedactionStatus string        `json:"redactionStatus"`
}

// UnmarshalJSON restores the closed activity-subject union. Without a custom
// decoder, encoding/json materializes Subject as map[string]any, which cannot
// pass validation and makes otherwise valid Manager API responses unusable by
// CLI/TUI clients.
func (record *ActivityRecord) UnmarshalJSON(data []byte) error {
	if record == nil {
		return ErrInvalidActivity
	}
	var wire struct {
		Schema          string          `json:"schema"`
		ID              string          `json:"id"`
		Owner           ActivityOwner   `json:"owner"`
		SessionID       string          `json:"sessionId"`
		Actor           *Actor          `json:"actor,omitempty"`
		Mediator        *Mediator       `json:"mediator,omitempty"`
		Kind            string          `json:"kind"`
		Operation       string          `json:"operation"`
		Subject         json.RawMessage `json:"subject"`
		Outcome         Outcome         `json:"outcome"`
		Count           uint64          `json:"count"`
		Bytes           uint64          `json:"bytes,omitempty"`
		FirstAt         time.Time       `json:"firstAt"`
		LastAt          time.Time       `json:"lastAt"`
		FirstSequence   uint64          `json:"firstSequence"`
		LastSequence    uint64          `json:"lastSequence"`
		Attribution     string          `json:"attribution"`
		Truncation      []string        `json:"truncation,omitempty"`
		CoverageID      string          `json:"coverageId"`
		RedactionStatus string          `json:"redactionStatus"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var (
		subject any
		err     error
	)
	switch wire.Kind {
	case ActivityProcess:
		var value ProcessSubject
		err = json.Unmarshal(wire.Subject, &value)
		subject = value
	case ActivityFile:
		var value FileSubject
		err = json.Unmarshal(wire.Subject, &value)
		subject = value
	case ActivityConnection:
		var value NetworkSubject
		err = json.Unmarshal(wire.Subject, &value)
		subject = value
	case ActivityDNS:
		var value DNSSubject
		err = json.Unmarshal(wire.Subject, &value)
		subject = value
	case ActivityRisk, ActivityCoverage:
		var value GenericSubject
		err = json.Unmarshal(wire.Subject, &value)
		subject = value
	default:
		return ErrInvalidActivity
	}
	if len(wire.Subject) == 0 ||
		string(wire.Subject) == "null" ||
		err != nil {
		return ErrInvalidActivity
	}
	*record = ActivityRecord{
		Schema: wire.Schema, ID: wire.ID, Owner: wire.Owner,
		SessionID: wire.SessionID, Actor: wire.Actor, Mediator: wire.Mediator,
		Kind: wire.Kind, Operation: wire.Operation, Subject: subject,
		Outcome: wire.Outcome, Count: wire.Count, Bytes: wire.Bytes,
		FirstAt: wire.FirstAt, LastAt: wire.LastAt,
		FirstSequence: wire.FirstSequence, LastSequence: wire.LastSequence,
		Attribution: wire.Attribution,
		Truncation:  append([]string(nil), wire.Truncation...),
		CoverageID:  wire.CoverageID, RedactionStatus: wire.RedactionStatus,
	}
	return nil
}

type Actor struct {
	ExecutionID string `json:"executionId"`
	PID         uint32 `json:"pid"`
	UID         uint32 `json:"uid"`
	GID         uint32 `json:"gid"`
	User        string `json:"user,omitempty"`
	Group       string `json:"group,omitempty"`
}

type Mediator struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	ExecutionID string `json:"executionId,omitempty"`
	Attribution string `json:"attribution"`
	Reason      string `json:"reason,omitempty"`
}

type Outcome struct {
	Status string `json:"status"`
	Code   *int   `json:"code,omitempty"`
	Signal uint32 `json:"signal,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ProcessSubject struct {
	Kind              string        `json:"kind"`
	ExecutionID       string        `json:"executionId"`
	ParentExecutionID string        `json:"parentExecutionId,omitempty"`
	Executable        string        `json:"executable"`
	Argv              []string      `json:"argv"`
	Cwd               string        `json:"cwd,omitempty"`
	GuestIdentity     GuestIdentity `json:"guestIdentity"`
	DurationNS        uint64        `json:"durationNs,omitempty"`
}

type FileSubject struct {
	Kind        string `json:"kind"`
	Path        string `json:"path,omitempty"`
	TargetPath  string `json:"targetPath,omitempty"`
	PathState   string `json:"pathState"`
	PathClass   string `json:"pathClass"`
	FileType    string `json:"fileType"`
	Device      uint64 `json:"device,omitempty"`
	Inode       uint64 `json:"inode,omitempty"`
	MountID     uint64 `json:"mountId,omitempty"`
	Destructive bool   `json:"destructive"`
}

type NetworkSubject struct {
	Kind              string `json:"kind"`
	Protocol          string `json:"protocol"`
	IP                string `json:"ip"`
	Port              uint16 `json:"port"`
	TargetIP          string `json:"targetIp,omitempty"`
	TargetPort        uint16 `json:"targetPort,omitempty"`
	Domain            string `json:"domain,omitempty"`
	DomainAttribution string `json:"domainAttribution"`
	CorrelationReason string `json:"correlationReason"`
	Route             string `json:"route"`
	Direction         string `json:"direction"`
	SocketCookie      uint64 `json:"socketCookie,omitempty"`
}

type DNSSubject struct {
	Kind         string   `json:"kind"`
	Query        string   `json:"query"`
	QueryType    string   `json:"queryType"`
	Answers      []string `json:"answers"`
	TTLSeconds   uint32   `json:"ttlSeconds,omitempty"`
	ResponseCode string   `json:"responseCode"`
	Resolver     string   `json:"resolver,omitempty"`
}

type GenericSubject struct {
	Kind    string `json:"kind"`
	Code    string `json:"code"`
	Summary string `json:"summary,omitempty"`
}

func (record ActivityRecord) Validate() error {
	if record.Schema != ActivityRecordSchema ||
		!activityIDPattern.MatchString(record.ID) ||
		record.Owner.Validate() != nil ||
		!sessionIDPattern.MatchString(record.SessionID) ||
		!validActivityKind(record.Kind) ||
		!operationPattern.MatchString(record.Operation) ||
		record.Count == 0 ||
		record.FirstAt.IsZero() || record.LastAt.IsZero() || record.LastAt.Before(record.FirstAt) ||
		record.LastSequence < record.FirstSequence ||
		!validAttribution(record.Attribution) ||
		!coverageIDPattern.MatchString(record.CoverageID) ||
		(record.RedactionStatus != RedactionPending && record.RedactionStatus != RedactionPassed) ||
		len(record.Truncation) > 32 {
		return ErrInvalidActivity
	}
	if record.Actor != nil && record.Actor.Validate() != nil {
		return ErrInvalidActivity
	}
	if record.Mediator != nil && record.Mediator.Validate() != nil {
		return ErrInvalidActivity
	}
	if err := record.Outcome.Validate(); err != nil {
		return err
	}
	if err := validateActivitySubject(record.Kind, record.Subject); err != nil {
		return err
	}
	seenTruncation := make(map[string]struct{}, len(record.Truncation))
	for _, reason := range record.Truncation {
		if !operationPattern.MatchString(reason) {
			return ErrInvalidActivity
		}
		if _, exists := seenTruncation[reason]; exists {
			return ErrInvalidActivity
		}
		seenTruncation[reason] = struct{}{}
	}
	return nil
}

func (record ActivityRecord) ValidatePersistable() error {
	if err := record.Validate(); err != nil {
		return err
	}
	if record.RedactionStatus != RedactionPassed {
		return errors.New("activity record has not passed redaction")
	}
	return nil
}

func (record ActivityRecord) ValidateForOwner(owner ActivityOwner) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	if !record.Owner.Equal(owner) {
		return ErrOwnerMismatch
	}
	return record.Validate()
}

func (actor Actor) Validate() error {
	if !executionIDPattern.MatchString(actor.ExecutionID) ||
		actor.PID == 0 || actor.PID > 4194304 ||
		len(actor.User) > 64 || len(actor.Group) > 64 ||
		containsControl(actor.User) || containsControl(actor.Group) {
		return ErrInvalidActivity
	}
	return nil
}

func (mediator Mediator) Validate() error {
	switch mediator.Kind {
	case "hostfs", "workspace-portal", "broker", "proxy", "resolver", "guest-service", "unknown":
	default:
		return ErrInvalidActivity
	}
	if !boundedPrintable(mediator.ID, 1, 128) ||
		(mediator.ExecutionID != "" && !executionIDPattern.MatchString(mediator.ExecutionID)) {
		return ErrInvalidActivity
	}
	switch mediator.Attribution {
	case AttributionExact, AttributionInferred, AttributionUnknown:
	default:
		return ErrInvalidActivity
	}
	if mediator.Reason != "" && !operationPattern.MatchString(mediator.Reason) {
		return ErrInvalidActivity
	}
	return nil
}

func (outcome Outcome) Validate() error {
	switch outcome.Status {
	case OutcomeSucceeded, OutcomeFailed, OutcomeDenied, OutcomeCancelled, OutcomeUnknown:
	default:
		return ErrInvalidActivity
	}
	if outcome.Signal > 0 && outcome.Signal > 128 {
		return ErrInvalidActivity
	}
	if outcome.Reason != "" && !operationPattern.MatchString(outcome.Reason) {
		return ErrInvalidActivity
	}
	return nil
}

func (subject ProcessSubject) Validate() error {
	if subject.Kind != ActivityProcess ||
		!executionIDPattern.MatchString(subject.ExecutionID) ||
		(subject.ParentExecutionID != "" && !executionIDPattern.MatchString(subject.ParentExecutionID)) ||
		!boundedPrintable(subject.Executable, 1, 4096) ||
		len(subject.Argv) > 1024 ||
		len(subject.Cwd) > 4096 || strings.IndexByte(subject.Cwd, 0) >= 0 ||
		subject.GuestIdentity.Validate() != nil {
		return ErrInvalidActivity
	}
	for _, argument := range subject.Argv {
		if len(argument) > 8192 || strings.IndexByte(argument, 0) >= 0 {
			return ErrInvalidActivity
		}
	}
	return nil
}

func (subject FileSubject) Validate() error {
	if subject.Kind != ActivityFile ||
		len(subject.Path) > 4096 || strings.IndexByte(subject.Path, 0) >= 0 ||
		!utf8.ValidString(subject.Path) ||
		len(subject.TargetPath) > 4096 || strings.IndexByte(subject.TargetPath, 0) >= 0 ||
		!utf8.ValidString(subject.TargetPath) {
		return ErrInvalidActivity
	}
	switch subject.PathState {
	case "resolved":
		if subject.Path == "" || !filepath.IsAbs(subject.Path) {
			return ErrInvalidActivity
		}
	case "aliased", "raced", "truncated":
		if subject.Path == "" {
			return ErrInvalidActivity
		}
	case "unknown":
		if subject.Path != "" {
			return ErrInvalidActivity
		}
	default:
		return ErrInvalidActivity
	}
	switch subject.PathClass {
	case "workspace", "hostfs", "profile", "runtime", "system", "external", "unknown":
	default:
		return ErrInvalidActivity
	}
	switch subject.FileType {
	case "regular", "directory", "symlink", "socket", "fifo", "device", "unknown":
	default:
		return ErrInvalidActivity
	}
	if subject.Inode == 0 && (subject.Device != 0 || subject.MountID != 0) {
		return ErrInvalidActivity
	}
	return nil
}

func (subject NetworkSubject) Validate() error {
	if subject.Kind != ActivityConnection ||
		(subject.Protocol != "tcp" && subject.Protocol != "udp") ||
		net.ParseIP(subject.IP) == nil || subject.Port == 0 ||
		(subject.TargetIP != "" && net.ParseIP(subject.TargetIP) == nil) ||
		len(subject.Domain) > 253 || containsControl(subject.Domain) ||
		!operationPattern.MatchString(subject.CorrelationReason) {
		return ErrInvalidActivity
	}
	if (subject.TargetIP != "" && subject.Domain != "") ||
		(subject.TargetIP != "" && subject.TargetPort == 0) ||
		(subject.Route == "proxy" &&
			subject.Domain != "" &&
			subject.TargetPort == 0) ||
		(subject.TargetPort != 0 &&
			subject.TargetIP == "" &&
			subject.Domain == "") {
		return ErrInvalidActivity
	}
	switch subject.DomainAttribution {
	case AttributionExact, AttributionInferred:
		if subject.Domain == "" {
			return ErrInvalidActivity
		}
	case AttributionUnknown:
		if subject.Domain != "" {
			return ErrInvalidActivity
		}
	default:
		return ErrInvalidActivity
	}
	switch subject.Route {
	case "direct", "proxy", "unknown":
	default:
		return ErrInvalidActivity
	}
	if subject.Route != "proxy" &&
		(subject.TargetIP != "" || subject.TargetPort != 0) {
		return ErrInvalidActivity
	}
	switch subject.Direction {
	case "egress", "ingress":
	default:
		return ErrInvalidActivity
	}
	return nil
}

func (subject DNSSubject) Validate() error {
	if subject.Kind != ActivityDNS ||
		!boundedPrintable(subject.Query, 1, 253) ||
		!boundedPrintable(subject.QueryType, 1, 16) ||
		len(subject.Answers) > 64 ||
		!boundedPrintable(subject.ResponseCode, 1, 32) ||
		len(subject.Resolver) > 256 || containsControl(subject.Resolver) {
		return ErrInvalidActivity
	}
	for _, answer := range subject.Answers {
		if len(answer) > 512 || containsControl(answer) {
			return ErrInvalidActivity
		}
	}
	return nil
}

func (subject GenericSubject) Validate() error {
	if !operationPattern.MatchString(subject.Kind) ||
		!operationPattern.MatchString(subject.Code) ||
		len(subject.Summary) > 2048 || containsControl(subject.Summary) {
		return ErrInvalidActivity
	}
	return nil
}

type CoverageInterval struct {
	Schema              string             `json:"schema"`
	ID                  string             `json:"id"`
	Owner               ActivityOwner      `json:"owner"`
	SessionID           string             `json:"sessionId"`
	Subsystem           string             `json:"subsystem"`
	State               string             `json:"state"`
	Reason              string             `json:"reason"`
	CollectorGeneration uint64             `json:"collectorGeneration"`
	DroppedEventCount   uint64             `json:"droppedEventCount"`
	RetentionGap        bool               `json:"retentionGap"`
	Evidence            []CoverageEvidence `json:"evidence,omitempty"`
	StartSequence       uint64             `json:"startSequence,omitempty"`
	EndSequence         *uint64            `json:"endSequence,omitempty"`
	StartedAt           time.Time          `json:"startedAt"`
	EndedAt             *time.Time         `json:"endedAt,omitempty"`
}

type CoverageEvidence struct {
	Code  string `json:"code"`
	Value string `json:"value,omitempty"`
}

func (interval CoverageInterval) Validate() error {
	if interval.Schema != CoverageIntervalSchema ||
		!coverageIDPattern.MatchString(interval.ID) ||
		interval.Owner.Validate() != nil ||
		!sessionIDPattern.MatchString(interval.SessionID) ||
		!validSubsystem(interval.Subsystem) ||
		!operationPattern.MatchString(interval.Reason) ||
		interval.CollectorGeneration == 0 ||
		interval.StartedAt.IsZero() ||
		len(interval.Evidence) > 256 {
		return ErrInvalidCoverage
	}
	switch interval.State {
	case CoverageAvailable:
		if interval.DroppedEventCount != 0 || interval.RetentionGap {
			return ErrFalseAvailableCoverage
		}
	case CoveragePartial, CoverageUnavailable:
	default:
		return ErrInvalidCoverage
	}
	if interval.EndSequence != nil && *interval.EndSequence < interval.StartSequence {
		return ErrInvalidCoverage
	}
	if interval.EndedAt != nil && interval.EndedAt.Before(interval.StartedAt) {
		return ErrInvalidCoverage
	}
	for _, evidence := range interval.Evidence {
		if !boundedPrintable(evidence.Code, 1, 128) ||
			len(evidence.Value) > 1024 || containsControl(evidence.Value) {
			return ErrInvalidCoverage
		}
	}
	return nil
}

func (interval CoverageInterval) ValidateForOwner(owner ActivityOwner) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	if !interval.Owner.Equal(owner) {
		return ErrOwnerMismatch
	}
	return interval.Validate()
}

func validateActivitySubject(kind string, subject any) error {
	switch typed := subject.(type) {
	case ProcessSubject:
		if kind != ActivityProcess {
			return ErrInvalidActivity
		}
		return typed.Validate()
	case *ProcessSubject:
		if typed == nil || kind != ActivityProcess {
			return ErrInvalidActivity
		}
		return typed.Validate()
	case FileSubject:
		if kind != ActivityFile {
			return ErrInvalidActivity
		}
		return typed.Validate()
	case *FileSubject:
		if typed == nil || kind != ActivityFile {
			return ErrInvalidActivity
		}
		return typed.Validate()
	case NetworkSubject:
		if kind != ActivityConnection {
			return ErrInvalidActivity
		}
		return typed.Validate()
	case *NetworkSubject:
		if typed == nil || kind != ActivityConnection {
			return ErrInvalidActivity
		}
		return typed.Validate()
	case DNSSubject:
		if kind != ActivityDNS {
			return ErrInvalidActivity
		}
		return typed.Validate()
	case *DNSSubject:
		if typed == nil || kind != ActivityDNS {
			return ErrInvalidActivity
		}
		return typed.Validate()
	case GenericSubject:
		if kind != ActivityRisk && kind != ActivityCoverage {
			return ErrInvalidActivity
		}
		return typed.Validate()
	case *GenericSubject:
		if typed == nil || (kind != ActivityRisk && kind != ActivityCoverage) {
			return ErrInvalidActivity
		}
		return typed.Validate()
	default:
		return ErrInvalidActivity
	}
}

func validActivityKind(value string) bool {
	switch value {
	case ActivityProcess, ActivityFile, ActivityConnection, ActivityDNS, ActivityRisk, ActivityCoverage:
		return true
	default:
		return false
	}
}

func validAttribution(value string) bool {
	switch value {
	case AttributionExact, AttributionInferred, AttributionMediated, AttributionUnknown:
		return true
	default:
		return false
	}
}

func validSubsystem(value string) bool {
	switch value {
	case SubsystemProcess, SubsystemFile, SubsystemNetwork, SubsystemDNS:
		return true
	default:
		return false
	}
}

func boundedPrintable(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum &&
		strings.TrimSpace(value) == value && !containsControl(value)
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func (owner ActivityOwner) String() string {
	if owner.Kind == OwnerReusableEnvironment {
		return fmt.Sprintf("%s/%s/%s", owner.Backend, owner.EnvironmentID, owner.BackendIncarnationID)
	}
	return fmt.Sprintf("%s/%s/%s", owner.Backend, owner.SessionID, owner.BackendIncarnationID)
}
