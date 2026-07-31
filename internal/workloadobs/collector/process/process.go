package process

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	EventFork = "process.fork"
	EventExec = "process.exec"
	EventExit = "process.exit"
)

var (
	ErrBoundaryMismatch = errors.New("process event does not match the workload boundary")
	ErrExecutionUnknown = errors.New("process execution identity is unknown")
	ErrInvalidEvent     = errors.New("process event is invalid")
	ErrEventOrder       = errors.New("process event ordering rolled back")
)

type Boundary struct {
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	GuestBootID        string
	CgroupID           uint64
	ObserverGeneration uint64
}

func (boundary Boundary) Validate() error {
	probe := workloadtypes.ExecutionIdentityInput{
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		GuestBootID: boundary.GuestBootID, ObserverGeneration: boundary.ObserverGeneration,
		PID: 1, ExecSequence: 1, StartedAtMonoNS: 1,
	}
	if _, err := workloadtypes.NewExecutionID(probe); err != nil ||
		boundary.CgroupID == 0 {
		return ErrBoundaryMismatch
	}
	return nil
}

type ClockAnchor struct {
	WallTime    time.Time
	MonotonicNS uint64
}

func (anchor ClockAnchor) Validate() error {
	if anchor.WallTime.IsZero() || anchor.MonotonicNS == 0 {
		return ErrInvalidEvent
	}
	return nil
}

type Event struct {
	Kind               string
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	GuestBootID        string
	CgroupID           uint64
	ObserverGeneration uint64

	PID                uint32
	TID                uint32
	ParentPID          uint32
	ExecSequence       uint64
	ParentExecSequence uint64
	CPU                uint64
	MonotonicNS        uint64
	Sequence           uint64

	Executable  string
	Argv        []string
	Cwd         string
	Identity    workloadtypes.GuestIdentity
	ExitCode    *int
	Signal      uint32
	Limitations []string
}

type executionKey struct {
	pid          uint32
	execSequence uint64
}

type forkObservation struct {
	parentKey         executionKey
	parentExecutionID string
	monotonicNS       uint64
	limitations       []string
}

type Normalizer struct {
	mu       sync.Mutex
	boundary Boundary
	anchor   ClockAnchor

	lastSequenceByCPU map[uint64]uint64
	executions        map[executionKey]*workloadtypes.Execution
	currentByPID      map[uint32]executionKey
	pendingFork       map[uint32]forkObservation
}

func NewNormalizer(boundary Boundary, anchor ClockAnchor) (*Normalizer, error) {
	if err := boundary.Validate(); err != nil {
		return nil, err
	}
	if err := anchor.Validate(); err != nil {
		return nil, err
	}
	return &Normalizer{
		boundary: boundary, anchor: anchor,
		lastSequenceByCPU: make(map[uint64]uint64),
		executions:        make(map[executionKey]*workloadtypes.Execution),
		currentByPID:      make(map[uint32]executionKey),
		pendingFork:       make(map[uint32]forkObservation),
	}, nil
}

func (normalizer *Normalizer) Apply(event Event) error {
	if normalizer == nil {
		return errors.New("process normalizer is nil")
	}
	normalizer.mu.Lock()
	defer normalizer.mu.Unlock()
	if !event.Owner.Equal(normalizer.boundary.Owner) ||
		event.SessionID != normalizer.boundary.SessionID ||
		event.GuestBootID != normalizer.boundary.GuestBootID ||
		event.CgroupID != normalizer.boundary.CgroupID ||
		event.ObserverGeneration != normalizer.boundary.ObserverGeneration {
		return ErrBoundaryMismatch
	}
	if event.Sequence == 0 || event.MonotonicNS < normalizer.anchor.MonotonicNS {
		return ErrInvalidEvent
	}
	if event.Sequence <= normalizer.lastSequenceByCPU[event.CPU] {
		return ErrEventOrder
	}

	var err error
	switch event.Kind {
	case EventFork:
		err = normalizer.applyFork(event)
	case EventExec:
		err = normalizer.applyExec(event)
	case EventExit:
		err = normalizer.applyExit(event)
	default:
		err = ErrInvalidEvent
	}
	if err != nil {
		return err
	}
	normalizer.lastSequenceByCPU[event.CPU] = event.Sequence
	return nil
}

func (normalizer *Normalizer) applyFork(event Event) error {
	if !validPID(event.PID) || event.TID != event.PID ||
		!validPID(event.ParentPID) || event.ParentExecSequence == 0 ||
		event.ExecSequence != 0 || event.Executable != "" ||
		len(event.Argv) != 0 || event.Cwd != "" ||
		event.ExitCode != nil || event.Signal != 0 ||
		!validForkLimitations(event.Limitations) {
		return ErrInvalidEvent
	}
	parentKey := executionKey{
		pid: event.ParentPID, execSequence: event.ParentExecSequence,
	}
	parent := normalizer.executions[parentKey]
	if parent == nil {
		return ErrExecutionUnknown
	}
	if _, exists := normalizer.pendingFork[event.PID]; exists {
		return ErrInvalidEvent
	}
	normalizer.pendingFork[event.PID] = forkObservation{
		parentKey:         parentKey,
		parentExecutionID: parent.ID,
		monotonicNS:       event.MonotonicNS,
		limitations:       append([]string(nil), event.Limitations...),
	}
	return nil
}

func (normalizer *Normalizer) applyExec(event Event) error {
	parentReference := event.ParentPID != 0 && event.ParentExecSequence != 0
	parentUnavailable := validPID(event.ParentPID) &&
		event.ParentExecSequence == 0 &&
		hasLimitation(event.Limitations, "state-unavailable")
	if !validPID(event.PID) || event.TID != event.PID ||
		event.ExecSequence == 0 ||
		!(parentReference || parentUnavailable ||
			(event.ParentPID == 0 && event.ParentExecSequence == 0)) ||
		(parentReference && !validPID(event.ParentPID)) ||
		!validText(event.Executable, 1, 4096) ||
		len(event.Argv) > 1024 ||
		len(event.Cwd) > 4096 || containsControl(event.Cwd) ||
		event.ExitCode != nil || event.Signal != 0 ||
		event.Identity.Validate() != nil ||
		!validExecLimitations(event.Limitations, event.Cwd == "") {
		return ErrInvalidEvent
	}
	totalArgvBytes := 0
	for _, argument := range event.Argv {
		if len(argument) > 8192 || strings.IndexByte(argument, 0) >= 0 ||
			!utf8.ValidString(argument) {
			return ErrInvalidEvent
		}
		totalArgvBytes += len(argument)
		if totalArgvBytes > 1<<20 {
			return ErrInvalidEvent
		}
	}
	key := executionKey{pid: event.PID, execSequence: event.ExecSequence}
	if normalizer.executions[key] != nil {
		return ErrInvalidEvent
	}
	var predecessor *workloadtypes.Execution
	if current, exists := normalizer.currentByPID[event.PID]; exists {
		predecessor = normalizer.executions[current]
		if predecessor == nil || predecessor.Exit != nil {
			return ErrInvalidEvent
		}
		if event.MonotonicNS < predecessor.StartedAtMonoNS {
			return ErrEventOrder
		}
	}
	parentExecutionID := ""
	limitations := append([]string(nil), event.Limitations...)
	consumePendingFork := false
	if predecessor != nil {
		if _, exists := normalizer.pendingFork[event.PID]; exists {
			return ErrInvalidEvent
		}
		if (parentReference || parentUnavailable) &&
			!hasLimitation(event.Limitations, "state-unavailable") {
			return ErrInvalidEvent
		}
		parentExecutionID = predecessor.ID
	} else if fork, exists := normalizer.pendingFork[event.PID]; exists {
		if fork.monotonicNS > event.MonotonicNS ||
			fork.parentExecutionID == "" {
			return ErrEventOrder
		}
		if parentReference {
			eventParent := executionKey{
				pid: event.ParentPID, execSequence: event.ParentExecSequence,
			}
			if eventParent != fork.parentKey {
				return ErrInvalidEvent
			}
		} else if !hasLimitation(fork.limitations, "state-unavailable") &&
			!hasLimitation(event.Limitations, "state-unavailable") {
			return ErrInvalidEvent
		}
		parentExecutionID = fork.parentExecutionID
		limitations = mergeLimitations(limitations, fork.limitations)
		consumePendingFork = true
	} else if parentReference {
		parentKey := executionKey{
			pid: event.ParentPID, execSequence: event.ParentExecSequence,
		}
		parent := normalizer.executions[parentKey]
		if parent == nil {
			return ErrExecutionUnknown
		}
		if parent.StartedAtMonoNS > event.MonotonicNS {
			return ErrEventOrder
		}
		parentExecutionID = parent.ID
	}
	startedAt, err := normalizer.wallTime(event.MonotonicNS)
	if err != nil {
		return err
	}
	id, err := workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner: normalizer.boundary.Owner, SessionID: normalizer.boundary.SessionID,
		GuestBootID:        normalizer.boundary.GuestBootID,
		ObserverGeneration: normalizer.boundary.ObserverGeneration,
		PID:                event.PID, ExecSequence: event.ExecSequence,
		StartedAtMonoNS: event.MonotonicNS,
	})
	if err != nil {
		return ErrInvalidEvent
	}
	execution := &workloadtypes.Execution{
		Schema: workloadtypes.ExecutionSchema, ID: id,
		Owner: normalizer.boundary.Owner, SessionID: normalizer.boundary.SessionID,
		ParentExecutionID:  parentExecutionID,
		GuestBootID:        normalizer.boundary.GuestBootID,
		ObserverGeneration: normalizer.boundary.ObserverGeneration,
		PID:                event.PID, TID: event.TID, ExecSequence: event.ExecSequence,
		StartedAtMonoNS: event.MonotonicNS, StartedAt: startedAt,
		Executable: event.Executable, Argv: append([]string(nil), event.Argv...),
		Cwd: event.Cwd, Identity: event.Identity,
		Limitations: limitations,
	}
	if err := execution.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if predecessor != nil {
		predecessor.Exit = &workloadtypes.ExitObservation{
			AtMonoNS:      event.MonotonicNS,
			At:            startedAt,
			UnknownReason: "replaced-by-exec",
		}
		if err := predecessor.Validate(); err != nil {
			predecessor.Exit = nil
			return fmt.Errorf("%w: %v", ErrInvalidEvent, err)
		}
	}
	if consumePendingFork {
		delete(normalizer.pendingFork, event.PID)
	}
	normalizer.executions[key] = execution
	normalizer.currentByPID[event.PID] = key
	return nil
}

func (normalizer *Normalizer) applyExit(event Event) error {
	exitUnavailable := hasLimitation(event.Limitations, "exit-unavailable")
	if !validPID(event.PID) || event.TID != event.PID ||
		event.Executable != "" ||
		len(event.Argv) != 0 || event.Cwd != "" ||
		event.Signal > 128 ||
		(event.ExitCode == nil && event.Signal == 0 && !exitUnavailable) ||
		(exitUnavailable && (event.ExitCode != nil || event.Signal != 0)) ||
		(event.ExitCode != nil && event.Signal != 0) ||
		(event.ExitCode != nil && (*event.ExitCode < 0 || *event.ExitCode > 255)) ||
		!validExitLimitations(event.Limitations) {
		return ErrInvalidEvent
	}
	if event.ExecSequence == 0 {
		if fork, exists := normalizer.pendingFork[event.PID]; exists {
			if event.MonotonicNS < fork.monotonicNS {
				return ErrEventOrder
			}
			delete(normalizer.pendingFork, event.PID)
			return nil
		}
		return ErrExecutionUnknown
	}
	key := executionKey{pid: event.PID, execSequence: event.ExecSequence}
	execution := normalizer.executions[key]
	if execution == nil {
		return ErrExecutionUnknown
	}
	if execution.Exit != nil || event.MonotonicNS < execution.StartedAtMonoNS {
		return ErrEventOrder
	}
	exitedAt, err := normalizer.wallTime(event.MonotonicNS)
	if err != nil {
		return err
	}
	var code *int
	if event.ExitCode != nil {
		value := *event.ExitCode
		code = &value
	}
	previousLimitations := execution.Limitations
	execution.Limitations = mergeLimitations(execution.Limitations, event.Limitations)
	execution.Exit = &workloadtypes.ExitObservation{
		Code: code, Signal: event.Signal,
		AtMonoNS: event.MonotonicNS, At: exitedAt,
	}
	if exitUnavailable {
		execution.Exit.UnknownReason = "exit-unavailable"
	}
	if err := execution.Validate(); err != nil {
		execution.Exit = nil
		execution.Limitations = previousLimitations
		return fmt.Errorf("%w: %v", ErrInvalidEvent, err)
	}
	if current, exists := normalizer.currentByPID[event.PID]; exists &&
		current == key {
		delete(normalizer.currentByPID, event.PID)
	}
	return nil
}

func (normalizer *Normalizer) Executions() []workloadtypes.Execution {
	if normalizer == nil {
		return nil
	}
	normalizer.mu.Lock()
	defer normalizer.mu.Unlock()
	result := make([]workloadtypes.Execution, 0, len(normalizer.executions))
	for _, source := range normalizer.executions {
		result = append(result, cloneExecution(source))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartedAtMonoNS != result[j].StartedAtMonoNS {
			return result[i].StartedAtMonoNS < result[j].StartedAtMonoNS
		}
		if result[i].ExecSequence != result[j].ExecSequence {
			return result[i].ExecSequence < result[j].ExecSequence
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func (normalizer *Normalizer) LookupExecution(
	pid uint32,
	execSequence uint64,
) (workloadtypes.Execution, bool) {
	if normalizer == nil || !validPID(pid) || execSequence == 0 {
		return workloadtypes.Execution{}, false
	}
	normalizer.mu.Lock()
	defer normalizer.mu.Unlock()
	execution := normalizer.executions[executionKey{
		pid: pid, execSequence: execSequence,
	}]
	if execution == nil {
		return workloadtypes.Execution{}, false
	}
	return cloneExecution(execution), true
}

// LookupExecutionID is the allocation-free lookup for collector hot paths
// that need only to bind an observation to an already-normalized execution.
// Execution identities are immutable after insertion.
func (normalizer *Normalizer) LookupExecutionID(
	pid uint32,
	execSequence uint64,
) (string, bool) {
	if normalizer == nil || !validPID(pid) || execSequence == 0 {
		return "", false
	}
	normalizer.mu.Lock()
	defer normalizer.mu.Unlock()
	execution := normalizer.executions[executionKey{
		pid: pid, execSequence: execSequence,
	}]
	if execution == nil || execution.ID == "" {
		return "", false
	}
	return execution.ID, true
}

func (normalizer *Normalizer) LookupCurrentExecution(
	pid uint32,
) (workloadtypes.Execution, bool) {
	if normalizer == nil || !validPID(pid) {
		return workloadtypes.Execution{}, false
	}
	normalizer.mu.Lock()
	defer normalizer.mu.Unlock()
	key, exists := normalizer.currentByPID[pid]
	if !exists {
		return workloadtypes.Execution{}, false
	}
	execution := normalizer.executions[key]
	if execution == nil {
		return workloadtypes.Execution{}, false
	}
	return cloneExecution(execution), true
}

func (normalizer *Normalizer) LookupActor(
	pid uint32,
	execSequence uint64,
) (workloadtypes.Actor, bool) {
	if normalizer == nil || !validPID(pid) || execSequence == 0 {
		return workloadtypes.Actor{}, false
	}
	normalizer.mu.Lock()
	defer normalizer.mu.Unlock()
	execution := normalizer.executions[executionKey{
		pid: pid, execSequence: execSequence,
	}]
	if execution == nil {
		return workloadtypes.Actor{}, false
	}
	actor := workloadtypes.Actor{
		ExecutionID: execution.ID,
		PID:         execution.PID,
		UID:         execution.Identity.UID,
		GID:         execution.Identity.GID,
		User:        execution.Identity.User,
		Group:       execution.Identity.Group,
	}
	if actor.Validate() != nil {
		return workloadtypes.Actor{}, false
	}
	return actor, true
}

// ExecActivity materializes the durable command record for an exec event that
// has already been accepted by Apply. Fork and exit events only update the
// execution graph; a successful exec is the auditable command boundary.
func (normalizer *Normalizer) ExecActivity(
	event Event,
	coverageID string,
) (workloadtypes.ActivityRecord, error) {
	if normalizer == nil ||
		event.Kind != EventExec ||
		!validCoverageID(coverageID) {
		return workloadtypes.ActivityRecord{}, ErrInvalidEvent
	}
	execution, ok := normalizer.LookupExecution(
		event.PID,
		event.ExecSequence,
	)
	if !ok ||
		!execution.Owner.Equal(event.Owner) ||
		execution.SessionID != event.SessionID ||
		execution.GuestBootID != event.GuestBootID ||
		execution.ObserverGeneration != event.ObserverGeneration ||
		execution.PID != event.PID ||
		execution.ExecSequence != event.ExecSequence ||
		execution.StartedAtMonoNS != event.MonotonicNS ||
		execution.Executable != event.Executable {
		return workloadtypes.ActivityRecord{}, ErrInvalidEvent
	}
	actor := workloadtypes.Actor{
		ExecutionID: execution.ID,
		PID:         execution.PID,
		UID:         execution.Identity.UID,
		GID:         execution.Identity.GID,
		User:        execution.Identity.User,
		Group:       execution.Identity.Group,
	}
	if actor.Validate() != nil {
		return workloadtypes.ActivityRecord{}, ErrInvalidEvent
	}
	subject := workloadtypes.ProcessSubject{
		Kind:              workloadtypes.ActivityProcess,
		ExecutionID:       execution.ID,
		ParentExecutionID: execution.ParentExecutionID,
		Executable:        execution.Executable,
		Argv:              append([]string(nil), execution.Argv...),
		Cwd:               execution.Cwd,
		GuestIdentity:     execution.Identity,
	}
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		Owner:  execution.Owner, SessionID: execution.SessionID,
		Actor: &actor,
		Kind:  workloadtypes.ActivityProcess, Operation: "exec",
		Subject: subject,
		Outcome: workloadtypes.Outcome{
			Status: workloadtypes.OutcomeSucceeded,
		},
		Count:           1,
		FirstAt:         execution.StartedAt.UTC(),
		LastAt:          execution.StartedAt.UTC(),
		FirstSequence:   event.Sequence,
		LastSequence:    event.Sequence,
		Attribution:     workloadtypes.AttributionExact,
		Truncation:      append([]string(nil), execution.Limitations...),
		CoverageID:      coverageID,
		RedactionStatus: workloadtypes.RedactionPending,
	}
	id, err := processActivityID(event, subject, coverageID)
	if err != nil {
		return workloadtypes.ActivityRecord{}, ErrInvalidEvent
	}
	record.ID = id
	if err := record.Validate(); err != nil {
		return workloadtypes.ActivityRecord{}, errors.Join(
			ErrInvalidEvent,
			err,
		)
	}
	return record, nil
}

func processActivityID(
	event Event,
	subject workloadtypes.ProcessSubject,
	coverageID string,
) (string, error) {
	payload := struct {
		Owner              workloadtypes.ActivityOwner  `json:"owner"`
		SessionID          string                       `json:"sessionId"`
		CgroupID           uint64                       `json:"cgroupId"`
		ObserverGeneration uint64                       `json:"observerGeneration"`
		Sequence           uint64                       `json:"sequence"`
		MonotonicNS        uint64                       `json:"monotonicNs"`
		Subject            workloadtypes.ProcessSubject `json:"subject"`
		CoverageID         string                       `json:"coverageId"`
	}{
		Owner: event.Owner, SessionID: event.SessionID,
		CgroupID:           event.CgroupID,
		ObserverGeneration: event.ObserverGeneration,
		Sequence:           event.Sequence, MonotonicNS: event.MonotonicNS,
		Subject: subject, CoverageID: coverageID,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append(
		[]byte("hideout.process-activity/v1\x00"),
		encoded...,
	))
	return "act_" + base64.RawURLEncoding.EncodeToString(sum[:18]), nil
}

func validCoverageID(value string) bool {
	if len(value) < len("cov_")+8 || len(value) > 128 ||
		!strings.HasPrefix(value, "cov_") {
		return false
	}
	for _, current := range value[len("cov_"):] {
		if (current >= 'a' && current <= 'z') ||
			(current >= 'A' && current <= 'Z') ||
			(current >= '0' && current <= '9') ||
			current == '_' || current == '-' {
			continue
		}
		return false
	}
	return true
}

func cloneExecution(source *workloadtypes.Execution) workloadtypes.Execution {
	if source == nil {
		return workloadtypes.Execution{}
	}
	value := *source
	value.Argv = append([]string(nil), source.Argv...)
	value.Limitations = append([]string(nil), source.Limitations...)
	if source.Exit != nil {
		exit := *source.Exit
		if source.Exit.Code != nil {
			code := *source.Exit.Code
			exit.Code = &code
		}
		value.Exit = &exit
	}
	return value
}

func (normalizer *Normalizer) wallTime(monotonicNS uint64) (time.Time, error) {
	if monotonicNS < normalizer.anchor.MonotonicNS {
		return time.Time{}, ErrEventOrder
	}
	delta := monotonicNS - normalizer.anchor.MonotonicNS
	if delta > math.MaxInt64 {
		return time.Time{}, ErrInvalidEvent
	}
	return normalizer.anchor.WallTime.Add(time.Duration(delta)), nil
}

func validPID(value uint32) bool {
	return value > 0 && value <= 4194304
}

func validText(value string, minBytes, maxBytes int) bool {
	return len(value) >= minBytes && len(value) <= maxBytes &&
		utf8.ValidString(value) && !containsControl(value)
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) || !unicode.IsPrint(character) ||
			character == '\u001b' {
			return true
		}
	}
	return false
}

func validLimitations(values []string) bool {
	previous := ""
	for _, value := range values {
		switch value {
		case "argv-truncated", "argv-unavailable", "cwd-unavailable",
			"executable-truncated", "exit-unavailable", "state-unavailable":
		default:
			return false
		}
		if value <= previous {
			return false
		}
		previous = value
	}
	return true
}

func validForkLimitations(values []string) bool {
	return validLimitations(values) &&
		(len(values) == 0 ||
			(len(values) == 1 && values[0] == "state-unavailable"))
}

func validExecLimitations(values []string, cwdEmpty bool) bool {
	if !validLimitations(values) {
		return false
	}
	for _, value := range values {
		if value == "exit-unavailable" ||
			(value == "cwd-unavailable" && !cwdEmpty) {
			return false
		}
	}
	return cwdEmpty == hasLimitation(values, "cwd-unavailable")
}

func validExitLimitations(values []string) bool {
	if !validLimitations(values) {
		return false
	}
	for _, value := range values {
		if value != "exit-unavailable" && value != "state-unavailable" {
			return false
		}
	}
	return true
}

func hasLimitation(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func mergeLimitations(left, right []string) []string {
	result := make([]string, 0, len(left)+len(right))
	leftIndex, rightIndex := 0, 0
	for leftIndex < len(left) || rightIndex < len(right) {
		switch {
		case rightIndex >= len(right) ||
			(leftIndex < len(left) && left[leftIndex] < right[rightIndex]):
			result = append(result, left[leftIndex])
			leftIndex++
		case leftIndex >= len(left) || right[rightIndex] < left[leftIndex]:
			result = append(result, right[rightIndex])
			rightIndex++
		default:
			result = append(result, left[leftIndex])
			leftIndex++
			rightIndex++
		}
	}
	return result
}
