package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	"github.com/vibe-agi/hideout/internal/workloadobs/coverage"
	workloadredact "github.com/vibe-agi/hideout/internal/workloadobs/redact"
	workloadstore "github.com/vibe-agi/hideout/internal/workloadobs/store"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

var (
	errActivityObservationInvalid = errors.New("daemon activity observation is invalid")
	errActivityBoundaryNotReady   = errors.New("daemon activity boundary is not ready")
	errActivitySessionClosed      = errors.New("daemon activity session is closed")
	errActivityRedactionDropped   = errors.New("daemon activity observation was safely dropped by redaction")

	activityReasonPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	activitySubsystems    = []string{
		workloadtypes.SubsystemProcess,
		workloadtypes.SubsystemFile,
		workloadtypes.SubsystemNetwork,
		workloadtypes.SubsystemDNS,
	}
)

const (
	activityRedactionSnapshotTimeout = 5 * time.Second
	maxActivityIngestBatch           = workloadstore.MaxAppendBatchEntries
)

type activityService struct {
	mu                  sync.RWMutex
	clockMu             sync.Mutex
	redactionMutationMu sync.RWMutex
	sessions            map[string]*activitySession
	now                 func() time.Time
	store               *workloadstore.Store
	redaction           activityRedactionBuilder
	redactionBuild      chan struct{}
	redactionWait       time.Duration
}

type activityRedactionBuilder interface {
	Build(context.Context) (*workloadredact.Snapshot, error)
}

type activitySession struct {
	mu sync.Mutex

	preparation backend.ActivityPreparation
	ready       *sessionwire.SupervisorActivityReady
	tracker     *sessionwire.ObserverSequenceTracker
	timelines   map[string]*coverage.Timeline

	accepted        uint64
	duplicates      uint64
	missing         uint64
	reportedDropped uint64
	droppedBytes    uint64
	kernelDropped   uint64
	ringDropped     uint64
	invalid         uint64
	ordinal         uint64
	lastKind        string

	heartbeatByCPU      map[uint64]activityHeartbeatCounters
	supervisorDropped   map[string]uint64
	accountedDropped    map[string]uint64
	observerClosed      bool
	sessionClosed       bool
	now                 func() time.Time
	persistCoverage     func(workloadtypes.CoverageInterval) error
	persistExecutions   func(context.Context, []workloadtypes.Execution) error
	persistActivities   func(context.Context, []workloadtypes.ActivityRecord) error
	redaction           activityRedactionBuilder
	redactionSnapshot   *workloadredact.Snapshot
	redactionFailed     bool
	redactionGeneration string
}

type activityHeartbeatCounters struct {
	kernel uint64
	ring   uint64
}

type activitySessionSnapshot struct {
	SessionID           string
	Owner               workloadtypes.ActivityOwner
	Boundary            *workloadtypes.WorkloadBoundary
	Accepted            uint64
	Duplicates          uint64
	Missing             uint64
	ReportedDropped     uint64
	DroppedBytes        uint64
	KernelDropped       uint64
	RingDropped         uint64
	Invalid             uint64
	LastKind            string
	ObserverClosed      bool
	SessionClosed       bool
	RedactionGeneration string
	Coverage            map[string]coverage.Summary
	Intervals           map[string][]workloadtypes.CoverageInterval
}

type activityLossPayload struct {
	Dropped      uint64 `json:"dropped"`
	DroppedBytes uint64 `json:"droppedBytes"`
	Reason       string `json:"reason"`
	Scope        string `json:"scope"`
}

type activityHeartbeatPayload struct {
	LatestSequence uint64 `json:"latestSequence"`
	KernelDropped  uint64 `json:"kernelDropped"`
	RingDropped    uint64 `json:"ringDropped"`
}

type activityCoveragePayload struct {
	Subsystem         string   `json:"subsystem"`
	State             string   `json:"state"`
	Reason            string   `json:"reason"`
	Evidence          []string `json:"evidence"`
	DroppedEventCount uint64   `json:"droppedEventCount"`
}

type observedActivityWire struct {
	Schema          string                      `json:"schema"`
	ID              string                      `json:"id"`
	Owner           workloadtypes.ActivityOwner `json:"owner"`
	SessionID       string                      `json:"sessionId"`
	Actor           *workloadtypes.Actor        `json:"actor,omitempty"`
	Mediator        *workloadtypes.Mediator     `json:"mediator,omitempty"`
	Kind            string                      `json:"kind"`
	Operation       string                      `json:"operation"`
	Subject         json.RawMessage             `json:"subject"`
	Outcome         workloadtypes.Outcome       `json:"outcome"`
	Count           uint64                      `json:"count"`
	Bytes           uint64                      `json:"bytes,omitempty"`
	FirstAt         time.Time                   `json:"firstAt"`
	LastAt          time.Time                   `json:"lastAt"`
	FirstSequence   uint64                      `json:"firstSequence"`
	LastSequence    uint64                      `json:"lastSequence"`
	Attribution     string                      `json:"attribution"`
	Truncation      []string                    `json:"truncation,omitempty"`
	CoverageID      string                      `json:"coverageId"`
	RedactionStatus string                      `json:"redactionStatus"`
}

type observedActivityShape struct {
	kind      string
	operation string
}

type preparedActivityObservation struct {
	record    *workloadtypes.ActivityRecord
	execution *workloadtypes.Execution
}

var observedActivityShapes = map[string]observedActivityShape{
	"process.exec":  {kind: workloadtypes.ActivityProcess, operation: "exec"},
	"file.open":     {kind: workloadtypes.ActivityFile, operation: "open"},
	"file.read":     {kind: workloadtypes.ActivityFile, operation: "read"},
	"file.write":    {kind: workloadtypes.ActivityFile, operation: "write"},
	"file.mmap":     {kind: workloadtypes.ActivityFile, operation: "mmap"},
	"file.create":   {kind: workloadtypes.ActivityFile, operation: "create"},
	"file.truncate": {kind: workloadtypes.ActivityFile, operation: "truncate"},
	"file.rename":   {kind: workloadtypes.ActivityFile, operation: "rename"},
	"file.unlink":   {kind: workloadtypes.ActivityFile, operation: "unlink"},
	"file.metadata": {kind: workloadtypes.ActivityFile, operation: "metadata"},
	"file.hardlink": {kind: workloadtypes.ActivityFile, operation: "hardlink"},
	"file.symlink":  {kind: workloadtypes.ActivityFile, operation: "symlink"},
	"file.mkdir":    {kind: workloadtypes.ActivityFile, operation: "mkdir"},
	"file.rmdir":    {kind: workloadtypes.ActivityFile, operation: "rmdir"},
	"network.connect": {
		kind: workloadtypes.ActivityConnection, operation: "connect",
	},
	"dns.response": {kind: workloadtypes.ActivityDNS, operation: "resolve"},
}

func newActivityService(now func() time.Time) *activityService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &activityService{
		sessions:       make(map[string]*activitySession),
		now:            now,
		redaction:      workloadredact.Builder{Now: now},
		redactionBuild: make(chan struct{}, 1),
		redactionWait:  activityRedactionSnapshotTimeout,
	}
}

func (service *activityService) setPersistentStore(store *workloadstore.Store) {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.store = store
	for _, session := range service.sessions {
		session.mu.Lock()
		session.persistCoverage = persistentCoverageWriter(store)
		session.persistExecutions = persistentExecutionWriter(store)
		session.persistActivities = persistentActivityWriter(store)
		session.mu.Unlock()
	}
	service.mu.Unlock()
}

func (service *activityService) setRedactionBuilder(
	builder activityRedactionBuilder,
) {
	if service == nil {
		return
	}
	if builder == nil {
		builder = workloadredact.Builder{Now: service.nowUTC}
	}
	service.mu.Lock()
	service.redaction = builder
	for _, session := range service.sessions {
		session.mu.Lock()
		session.redaction = builder
		session.mu.Unlock()
	}
	service.mu.Unlock()
}

func (service *activityService) invalidateRedactionSnapshots() error {
	if service == nil {
		return nil
	}
	service.mu.RLock()
	sessions := make([]*activitySession, 0, len(service.sessions))
	for _, session := range service.sessions {
		sessions = append(sessions, session)
	}
	service.mu.RUnlock()
	var result error
	for _, session := range sessions {
		result = errors.Join(
			result,
			session.invalidateRedactionSnapshot(),
		)
	}
	return result
}

func (service *activityService) beginSecretMutation() (func(), error) {
	if service == nil {
		return func() {}, nil
	}
	service.redactionMutationMu.Lock()
	if err := service.invalidateRedactionSnapshots(); err != nil {
		refreshErr := service.refreshRedactionSnapshots()
		service.redactionMutationMu.Unlock()
		return nil, errors.Join(err, refreshErr)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			// Keep the mutation gate exclusive until every active session has
			// either adopted the committed (or rolled-back) secret state or
			// remained explicitly fail-closed.
			_ = service.refreshRedactionSnapshots()
			service.redactionMutationMu.Unlock()
		})
	}, nil
}

func (service *activityService) refreshRedactionSnapshots() error {
	if service == nil {
		return nil
	}
	service.mu.RLock()
	builder := service.redaction
	sessions := make([]*activitySession, 0, len(service.sessions))
	for _, session := range service.sessions {
		sessions = append(sessions, session)
	}
	service.mu.RUnlock()
	if builder == nil {
		return workloadredact.ErrSnapshotUnavailable
	}

	var result error
	for _, session := range sessions {
		session.mu.Lock()
		needsRefresh := !session.sessionClosed &&
			!session.observerClosed &&
			session.redactionSnapshot == nil
		session.mu.Unlock()
		if !needsRefresh {
			continue
		}
		snapshot, err := service.buildRedactionSnapshot(builder)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		session.mu.Lock()
		if session.sessionClosed || session.observerClosed ||
			session.redactionSnapshot != nil {
			session.mu.Unlock()
			snapshot.Clear()
			continue
		}
		session.redactionSnapshot = snapshot
		session.redactionFailed = false
		session.redactionGeneration = snapshot.Metadata().ID
		session.mu.Unlock()
	}
	return result
}

func (session *activitySession) invalidateRedactionSnapshot() error {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.redactionSnapshot == nil {
		session.redactionFailed = true
		session.redactionGeneration = ""
		return nil
	}
	session.redactionSnapshot.Clear()
	session.redactionSnapshot = nil
	session.redactionFailed = true
	session.redactionGeneration = ""
	if session.sessionClosed || session.observerClosed ||
		session.ready == nil {
		return nil
	}
	return session.markAllLossLocked(
		coverage.ReasonRedactionDropped,
		1,
		[]workloadtypes.CoverageEvidence{{
			Code: "managed-secret-state-changed",
		}},
	)
}

type activityRedactionBuildResult struct {
	snapshot *workloadredact.Snapshot
	err      error
}

func (service *activityService) buildRedactionSnapshot(
	builder activityRedactionBuilder,
) (*workloadredact.Snapshot, error) {
	if service == nil || builder == nil {
		return nil, workloadredact.ErrSnapshotUnavailable
	}
	ctx, cancel := context.WithTimeout(
		context.Background(),
		service.redactionTimeout(),
	)
	defer cancel()
	select {
	case service.redactionBuild <- struct{}{}:
	case <-ctx.Done():
		return nil, errors.Join(
			workloadredact.ErrSnapshotUnavailable,
			ctx.Err(),
		)
	}

	result := make(chan activityRedactionBuildResult)
	abandoned := make(chan struct{})
	defer close(abandoned)
	go func() {
		snapshot, err := builder.Build(ctx)
		<-service.redactionBuild
		select {
		case result <- activityRedactionBuildResult{
			snapshot: snapshot,
			err:      err,
		}:
		case <-abandoned:
			snapshot.Clear()
		}
	}()
	select {
	case built := <-result:
		if built.err == nil && built.snapshot == nil {
			return nil, workloadredact.ErrSnapshotUnavailable
		}
		return built.snapshot, built.err
	case <-ctx.Done():
		return nil, errors.Join(
			workloadredact.ErrSnapshotUnavailable,
			ctx.Err(),
		)
	}
}

func (service *activityService) redactionTimeout() time.Duration {
	if service != nil && service.redactionWait > 0 {
		return service.redactionWait
	}
	return activityRedactionSnapshotTimeout
}

func persistentCoverageWriter(
	store *workloadstore.Store,
) func(workloadtypes.CoverageInterval) error {
	if store == nil {
		return nil
	}
	return func(interval workloadtypes.CoverageInterval) error {
		return store.AppendCoverage(context.Background(), interval)
	}
}

func persistentActivityWriter(
	store *workloadstore.Store,
) func(context.Context, []workloadtypes.ActivityRecord) error {
	if store == nil {
		return nil
	}
	return store.AppendActivities
}

func persistentExecutionWriter(
	store *workloadstore.Store,
) func(context.Context, []workloadtypes.Execution) error {
	if store == nil {
		return nil
	}
	return store.AppendExecutions
}

func (service *activityService) Prepare(
	preparation backend.ActivityPreparation,
) (sessionwire.SupervisorActivityExpectation, error) {
	if service == nil {
		return sessionwire.SupervisorActivityExpectation{}, errors.New("daemon activity service is unavailable")
	}
	if err := preparation.Validate(); err != nil {
		return sessionwire.SupervisorActivityExpectation{}, err
	}
	// A secret mutation owns the write side from snapshot invalidation through
	// the Keychain commit. This read side ensures a concurrent Prepare either
	// finishes and is invalidated before that commit, or builds from the new
	// secret state after the mutation releases the gate.
	service.redactionMutationMu.RLock()
	defer service.redactionMutationMu.RUnlock()
	service.mu.RLock()
	builder := service.redaction
	store := service.store
	service.mu.RUnlock()
	if store != nil {
		if err := store.BindOwnerRetention(
			context.Background(),
			preparation.Owner,
			preparation.Retention,
		); err != nil &&
			!errors.Is(err, workloadstore.ErrOwnerRetentionConflict) {
			return sessionwire.SupervisorActivityExpectation{}, err
		}
	}
	token, err := sessionwire.NewObserverStreamToken()
	if err != nil {
		return sessionwire.SupervisorActivityExpectation{}, err
	}
	expectation := sessionwire.SupervisorActivityExpectation{
		Owner: preparation.Owner, ObserverGeneration: preparation.ObserverGeneration,
		ObserverHelperDigest: preparation.ObserverHelperDigest,
		ObserverStreamToken:  token,
	}
	redactionFailed := builder == nil
	var redactionSnapshot *workloadredact.Snapshot
	if builder != nil {
		snapshot, snapshotErr := service.buildRedactionSnapshot(builder)
		if snapshotErr != nil {
			redactionFailed = true
		} else {
			redactionSnapshot = snapshot
			expectation.RedactionGeneration = snapshot.Metadata().ID
		}
	}
	if err := expectation.Validate(preparation.SessionID); err != nil {
		token.Destroy()
		redactionSnapshot.Clear()
		return sessionwire.SupervisorActivityExpectation{}, err
	}
	session := &activitySession{
		preparation:         preparation,
		timelines:           make(map[string]*coverage.Timeline, len(activitySubsystems)),
		heartbeatByCPU:      make(map[uint64]activityHeartbeatCounters),
		supervisorDropped:   make(map[string]uint64, len(activitySubsystems)),
		accountedDropped:    make(map[string]uint64, len(activitySubsystems)),
		now:                 service.nowUTC,
		persistCoverage:     persistentCoverageWriter(store),
		persistExecutions:   persistentExecutionWriter(store),
		persistActivities:   persistentActivityWriter(store),
		redaction:           builder,
		redactionSnapshot:   redactionSnapshot,
		redactionFailed:     redactionFailed,
		redactionGeneration: expectation.RedactionGeneration,
	}
	service.mu.Lock()
	if _, exists := service.sessions[preparation.SessionID]; exists {
		service.mu.Unlock()
		token.Destroy()
		redactionSnapshot.Clear()
		return sessionwire.SupervisorActivityExpectation{}, errors.New("daemon activity session is already prepared")
	}
	service.sessions[preparation.SessionID] = session
	service.mu.Unlock()
	return expectation, nil
}

func (service *activityService) RegisterBoundary(
	sessionID string,
	ready *sessionwire.SupervisorActivityReady,
) error {
	session, err := service.session(sessionID)
	if err != nil {
		return err
	}
	return session.registerBoundary(ready)
}

func (service *activityService) Ingest(
	sessionID string,
	envelope sessionwire.ObservationEnvelope,
) error {
	return service.IngestBatch(sessionID, []sessionwire.ObservationEnvelope{
		envelope,
	})
}

func (service *activityService) IngestBatch(
	sessionID string,
	envelopes []sessionwire.ObservationEnvelope,
) error {
	if service == nil {
		return errors.New("daemon activity service is unavailable")
	}
	service.redactionMutationMu.RLock()
	defer service.redactionMutationMu.RUnlock()
	session, err := service.session(sessionID)
	if err != nil {
		return err
	}
	return session.ingestBatch(envelopes)
}

func (service *activityService) PersistActivity(
	ctx context.Context,
	sessionID string,
	record workloadtypes.ActivityRecord,
) error {
	if service == nil {
		return errors.New("daemon activity service is unavailable")
	}
	service.redactionMutationMu.RLock()
	defer service.redactionMutationMu.RUnlock()
	session, err := service.session(sessionID)
	if err != nil {
		return err
	}
	return session.persistObservedActivity(ctx, record)
}

func (service *activityService) ObserverExited(sessionID string, cause error) error {
	session, err := service.session(sessionID)
	if err != nil {
		return err
	}
	return session.observerExited(cause)
}

func (service *activityService) SessionExited(
	sessionID string,
	completion *sessionwire.SupervisorActivityCompletion,
	cause error,
) error {
	session, err := service.session(sessionID)
	if err != nil {
		return err
	}
	return session.sessionExited(completion, cause)
}

func (service *activityService) BoundaryReady(sessionID string) bool {
	session, err := service.session(sessionID)
	if err != nil {
		return false
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.ready != nil && !session.sessionClosed
}

func (service *activityService) Snapshot(
	sessionID string,
) (activitySessionSnapshot, bool) {
	session, err := service.session(sessionID)
	if err != nil {
		return activitySessionSnapshot{}, false
	}
	return session.snapshot(), true
}

func (service *activityService) session(sessionID string) (*activitySession, error) {
	if service == nil || sessionID == "" {
		return nil, errors.New("daemon activity session identity is required")
	}
	service.mu.RLock()
	session := service.sessions[sessionID]
	service.mu.RUnlock()
	if session == nil {
		return nil, errors.New("daemon activity session is not prepared")
	}
	return session, nil
}

func (service *activityService) nowUTC() time.Time {
	service.clockMu.Lock()
	defer service.clockMu.Unlock()
	now := service.now
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

func (session *activitySession) registerBoundary(
	ready *sessionwire.SupervisorActivityReady,
) error {
	if session == nil || ready == nil {
		return errActivityBoundaryNotReady
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.sessionClosed || session.observerClosed {
		return errActivitySessionClosed
	}
	if session.ready != nil {
		return errors.New("daemon activity boundary was registered more than once")
	}
	preparation := session.preparation
	if err := ready.Validate(preparation.SessionID); err != nil {
		return err
	}
	if !ready.Boundary.Owner.Equal(preparation.Owner) ||
		ready.Boundary.SessionID != preparation.SessionID ||
		ready.Boundary.ObserverGeneration != preparation.ObserverGeneration ||
		ready.Boundary.GuestBootID != preparation.GuestBootID ||
		ready.ObserverHelperDigest != preparation.ObserverHelperDigest {
		return sessionwire.ErrObserverIdentity
	}
	binding := sessionwire.ObserverBinding{
		Owner: preparation.Owner, SessionID: preparation.SessionID,
		EnvironmentID:        preparation.EnvironmentID,
		BackendIncarnationID: preparation.BackendIncarnationID,
		GuestBootID:          ready.Boundary.GuestBootID, CgroupID: ready.Boundary.CgroupID,
		ObserverGeneration: ready.Boundary.ObserverGeneration,
	}
	tracker, err := sessionwire.NewObserverSequenceTracker(binding)
	if err != nil {
		return err
	}
	at := session.now()
	timelines := make(map[string]*coverage.Timeline, len(activitySubsystems))
	supervisorDropped := make(map[string]uint64, len(activitySubsystems))
	accountedDropped := make(map[string]uint64, len(activitySubsystems))
	for _, summary := range ready.Coverage {
		timeline, timelineErr := coverage.NewTimeline(
			preparation.Owner,
			preparation.SessionID,
			summary.Subsystem,
		)
		if timelineErr != nil {
			return timelineErr
		}
		reason, reasonErr := initialCoverageReason(summary.State)
		if reasonErr != nil {
			return reasonErr
		}
		if _, transitionErr := timeline.Transition(coverage.Transition{
			State: summary.State, Reason: reason,
			CollectorGeneration: preparation.ObserverGeneration,
			DroppedEventCount:   summary.DroppedEventCount,
			Evidence:            observerCoverageEvidence(summary),
			Sequence:            0,
			At:                  at,
		}); transitionErr != nil {
			return transitionErr
		}
		accounted := summary.DroppedEventCount
		if session.redactionFailed {
			state := workloadtypes.CoveragePartial
			if summary.State == workloadtypes.CoverageUnavailable {
				state = workloadtypes.CoverageUnavailable
			}
			if _, transitionErr := timeline.Transition(coverage.Transition{
				State: state, Reason: coverage.ReasonRedactionDropped,
				CollectorGeneration: preparation.ObserverGeneration,
				DroppedEventCount:   1,
				Evidence: []workloadtypes.CoverageEvidence{{
					Code: "redaction-snapshot-unavailable",
				}},
				Sequence: 1, At: at,
			}); transitionErr != nil {
				return transitionErr
			}
			accounted = saturatingAdd(accounted, 1)
		}
		timelines[summary.Subsystem] = timeline
		supervisorDropped[summary.Subsystem] = summary.DroppedEventCount
		accountedDropped[summary.Subsystem] = accounted
	}
	for _, subsystem := range activitySubsystems {
		if timelines[subsystem] == nil {
			return errors.New("daemon activity readiness omitted a coverage subsystem")
		}
		if err := session.persistTimelineLocked(timelines[subsystem]); err != nil {
			return fmt.Errorf("persist initial activity coverage: %w", err)
		}
	}
	session.tracker = tracker
	session.timelines = timelines
	session.supervisorDropped = supervisorDropped
	session.accountedDropped = accountedDropped
	if session.redactionFailed {
		session.ordinal = 1
	}
	session.ready = cloneActivityReady(ready)
	return nil
}

func (session *activitySession) persistObservedActivity(
	ctx context.Context,
	record workloadtypes.ActivityRecord,
) error {
	if session == nil || ctx == nil {
		return errActivityObservationInvalid
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.persistObservedActivityLocked(ctx, record)
}

func (session *activitySession) persistObservedActivityLocked(
	ctx context.Context,
	record workloadtypes.ActivityRecord,
) error {
	safe, err := session.prepareObservedActivityLocked(ctx, record)
	if err != nil {
		return err
	}
	return session.persistPreparedActivitiesLocked(
		ctx,
		[]workloadtypes.ActivityRecord{safe},
	)
}

func (session *activitySession) prepareObservedActivityLocked(
	ctx context.Context,
	record workloadtypes.ActivityRecord,
) (workloadtypes.ActivityRecord, error) {
	if err := ctx.Err(); err != nil {
		return workloadtypes.ActivityRecord{}, err
	}
	if session.sessionClosed || session.observerClosed {
		return workloadtypes.ActivityRecord{}, errActivitySessionClosed
	}
	if session.ready == nil || session.tracker == nil {
		return workloadtypes.ActivityRecord{}, errActivityBoundaryNotReady
	}
	if !record.Owner.Equal(session.preparation.Owner) ||
		record.SessionID != session.preparation.SessionID {
		return workloadtypes.ActivityRecord{}, workloadtypes.ErrOwnerMismatch
	}
	subsystem := activityRecordSubsystem(record.Kind)
	if subsystem == "" || session.timelines[subsystem] == nil {
		return workloadtypes.ActivityRecord{}, errActivityObservationInvalid
	}
	if session.redactionSnapshot == nil {
		transitionErr := session.markSubsystemLossLocked(
			subsystem,
			coverage.ReasonRedactionDropped,
			1,
			[]workloadtypes.CoverageEvidence{{
				Code: "redaction-snapshot-unavailable",
			}},
		)
		return workloadtypes.ActivityRecord{}, activityRedactionDropError(
			workloadredact.ErrSnapshotUnavailable,
			transitionErr,
		)
	}
	safe, err := session.redactionSnapshot.Activity(record)
	if err != nil {
		transitionErr := session.markSubsystemLossLocked(
			subsystem,
			coverage.ReasonRedactionDropped,
			1,
			[]workloadtypes.CoverageEvidence{{
				Code: "activity-redaction-failed",
			}},
		)
		return workloadtypes.ActivityRecord{}, activityRedactionDropError(
			workloadredact.ErrRedactionFailed,
			transitionErr,
		)
	}
	return safe, nil
}

func (session *activitySession) prepareObservedExecutionLocked(
	ctx context.Context,
	execution workloadtypes.Execution,
) (workloadtypes.Execution, error) {
	if err := ctx.Err(); err != nil {
		return workloadtypes.Execution{}, err
	}
	if session.sessionClosed || session.observerClosed {
		return workloadtypes.Execution{}, errActivitySessionClosed
	}
	if session.ready == nil || session.tracker == nil {
		return workloadtypes.Execution{}, errActivityBoundaryNotReady
	}
	if !execution.Owner.Equal(session.preparation.Owner) ||
		execution.SessionID != session.preparation.SessionID ||
		execution.GuestBootID != session.ready.Boundary.GuestBootID ||
		execution.ObserverGeneration !=
			session.preparation.ObserverGeneration {
		return workloadtypes.Execution{}, workloadtypes.ErrOwnerMismatch
	}
	if session.redactionSnapshot == nil {
		transitionErr := session.markSubsystemLossLocked(
			workloadtypes.SubsystemProcess,
			coverage.ReasonRedactionDropped,
			1,
			[]workloadtypes.CoverageEvidence{{
				Code: "redaction-snapshot-unavailable",
			}},
		)
		return workloadtypes.Execution{}, activityRedactionDropError(
			workloadredact.ErrSnapshotUnavailable,
			transitionErr,
		)
	}
	safe, err := session.redactionSnapshot.Execution(execution)
	if err != nil {
		transitionErr := session.markSubsystemLossLocked(
			workloadtypes.SubsystemProcess,
			coverage.ReasonRedactionDropped,
			1,
			[]workloadtypes.CoverageEvidence{{
				Code: "execution-redaction-failed",
			}},
		)
		return workloadtypes.Execution{}, activityRedactionDropError(
			workloadredact.ErrRedactionFailed,
			transitionErr,
		)
	}
	return safe, nil
}

func activityRedactionDropError(cause, transitionErr error) error {
	if transitionErr != nil {
		return errors.Join(cause, transitionErr)
	}
	return errors.Join(errActivityRedactionDropped, cause)
}

func (session *activitySession) persistPreparedActivitiesLocked(
	ctx context.Context,
	records []workloadtypes.ActivityRecord,
) error {
	if len(records) == 0 {
		return nil
	}
	if session.persistActivities == nil {
		return errors.New("activity persistence is unavailable")
	}
	if err := session.persistActivities(ctx, records); err != nil {
		return err
	}
	session.markRedactionGenerationLocked()
	return nil
}

func (session *activitySession) persistPreparedObservationsLocked(
	ctx context.Context,
	executions []workloadtypes.Execution,
	records []workloadtypes.ActivityRecord,
) error {
	if len(executions) != 0 && session.persistExecutions == nil {
		return errors.New("execution persistence is unavailable")
	}
	if len(executions) != 0 {
		if err := session.persistExecutions(ctx, executions); err != nil {
			return err
		}
	}
	if err := session.persistPreparedActivitiesLocked(ctx, records); err != nil {
		return err
	}
	if len(executions) != 0 && len(records) == 0 {
		session.markRedactionGenerationLocked()
	}
	return nil
}

func (session *activitySession) markRedactionGenerationLocked() {
	if session == nil || session.redactionSnapshot == nil {
		return
	}
	session.redactionFailed = false
	session.redactionGeneration =
		session.redactionSnapshot.Metadata().ID
}

func (session *activitySession) ingestBatch(
	envelopes []sessionwire.ObservationEnvelope,
) error {
	if session == nil || len(envelopes) == 0 ||
		len(envelopes) > maxActivityIngestBatch {
		return errActivityObservationInvalid
	}
	session.mu.Lock()
	defer session.mu.Unlock()

	records := make([]workloadtypes.ActivityRecord, 0, len(envelopes))
	executions := make([]workloadtypes.Execution, 0, len(envelopes))
	acceptedKinds := make([]string, 0, len(envelopes))
	for _, envelope := range envelopes {
		prepared, accepted, err := session.prepareEnvelopeLocked(envelope)
		if err != nil {
			if errors.Is(err, errActivityRedactionDropped) {
				continue
			}
			persistErr := session.persistPreparedObservationsLocked(
				context.Background(),
				executions,
				records,
			)
			if persistErr == nil {
				session.acceptKindsLocked(acceptedKinds)
			}
			return errors.Join(err, persistErr)
		}
		if !accepted {
			continue
		}
		acceptedKinds = append(acceptedKinds, envelope.Kind)
		if prepared.record != nil {
			records = append(records, *prepared.record)
		}
		if prepared.execution != nil {
			executions = append(executions, *prepared.execution)
		}
	}
	if err := session.persistPreparedObservationsLocked(
		context.Background(),
		executions,
		records,
	); err != nil {
		return err
	}
	session.acceptKindsLocked(acceptedKinds)
	return nil
}

func (session *activitySession) prepareEnvelopeLocked(
	envelope sessionwire.ObservationEnvelope,
) (preparedActivityObservation, bool, error) {
	if session.sessionClosed || session.observerClosed {
		return preparedActivityObservation{}, false, errActivitySessionClosed
	}
	if session.ready == nil || session.tracker == nil {
		return preparedActivityObservation{}, false, errActivityBoundaryNotReady
	}
	if err := envelope.Validate(); err != nil {
		return preparedActivityObservation{}, false,
			session.rejectInvalidLocked(err, coverage.ReasonInvalidFrame)
	}
	if !envelope.Owner.Equal(session.preparation.Owner) ||
		envelope.SessionID != session.preparation.SessionID ||
		envelope.CgroupID != session.ready.Boundary.CgroupID {
		return preparedActivityObservation{}, false, session.rejectInvalidLocked(
			sessionwire.ErrObserverIdentity,
			coverage.ReasonInvalidFrame,
		)
	}
	if envelope.ObserverGeneration != session.preparation.ObserverGeneration {
		return preparedActivityObservation{}, false, session.rejectInvalidLocked(
			sessionwire.ErrObserverIdentity,
			coverage.ReasonCollectorRestarted,
		)
	}
	prepared, err := session.validateObservationPayloadLocked(envelope)
	if err != nil {
		return preparedActivityObservation{}, false,
			session.rejectInvalidLocked(err, coverage.ReasonInvalidFrame)
	}
	sequence, err := session.tracker.Observe(envelope)
	if err != nil {
		return preparedActivityObservation{}, false,
			session.rejectInvalidLocked(err, coverage.ReasonInvalidFrame)
	}
	switch sequence.Disposition {
	case sessionwire.ObserverSequenceDuplicate:
		session.duplicates = saturatingAdd(session.duplicates, 1)
		return preparedActivityObservation{}, false, nil
	case sessionwire.ObserverSequenceGap:
		missing := inclusiveSequenceCount(sequence.MissingFrom, sequence.MissingTo)
		session.missing = saturatingAdd(session.missing, missing)
		if err := session.markAllLossLocked(coverage.ReasonSequenceGap, missing, nil); err != nil {
			return preparedActivityObservation{}, false, err
		}
	case sessionwire.ObserverSequenceRestart:
		missing := inclusiveSequenceCount(sequence.MissingFrom, sequence.MissingTo)
		if missing == 0 {
			missing = 1
		}
		session.missing = saturatingAdd(session.missing, missing)
		if err := session.markAllLossLocked(coverage.ReasonCollectorRestarted, missing, nil); err != nil {
			return preparedActivityObservation{}, false, err
		}
	}

	switch envelope.Kind {
	case "collector.loss":
		if err := session.ingestLossLocked(envelope); err != nil {
			return preparedActivityObservation{}, false,
				session.rejectInvalidLocked(err, coverage.ReasonInvalidFrame)
		}
	case "collector.heartbeat":
		if err := session.ingestHeartbeatLocked(envelope); err != nil {
			return preparedActivityObservation{}, false,
				session.rejectInvalidLocked(err, coverage.ReasonInvalidFrame)
		}
	case "coverage.changed":
		if err := session.ingestCoverageLocked(envelope); err != nil {
			return preparedActivityObservation{}, false,
				session.rejectInvalidLocked(err, coverage.ReasonInvalidFrame)
		}
	}
	if prepared.execution != nil {
		safe, err := session.prepareObservedExecutionLocked(
			context.Background(),
			*prepared.execution,
		)
		if err != nil {
			return preparedActivityObservation{}, false, err
		}
		prepared.execution = &safe
	}
	if prepared.record != nil {
		subsystem := activityRecordSubsystem(prepared.record.Kind)
		current, currentErr := session.timelines[subsystem].Current()
		if currentErr != nil {
			return preparedActivityObservation{}, false, currentErr
		}
		// Coverage intervals are host-owned evidence. The guest can normalize the
		// event, but it cannot mint or select the interval that vouches for it.
		prepared.record.CoverageID = current.ID
		safe, err := session.prepareObservedActivityLocked(
			context.Background(),
			*prepared.record,
		)
		if err != nil {
			return preparedActivityObservation{}, false, err
		}
		prepared.record = &safe
	}
	return prepared, true, nil
}

func (session *activitySession) acceptKindsLocked(kinds []string) {
	for _, kind := range kinds {
		session.accepted = saturatingAdd(session.accepted, 1)
		session.lastKind = kind
	}
}

func activityRecordSubsystem(kind string) string {
	switch kind {
	case workloadtypes.ActivityProcess:
		return workloadtypes.SubsystemProcess
	case workloadtypes.ActivityFile:
		return workloadtypes.SubsystemFile
	case workloadtypes.ActivityConnection:
		return workloadtypes.SubsystemNetwork
	case workloadtypes.ActivityDNS:
		return workloadtypes.SubsystemDNS
	default:
		return ""
	}
}

func (session *activitySession) validateObservationPayloadLocked(
	envelope sessionwire.ObservationEnvelope,
) (preparedActivityObservation, error) {
	switch envelope.Kind {
	case "collector.loss":
		_, _, err := session.validatedLossPayloadLocked(envelope)
		return preparedActivityObservation{}, err
	case "collector.heartbeat":
		_, err := session.validatedHeartbeatPayloadLocked(envelope)
		return preparedActivityObservation{}, err
	case "coverage.changed":
		_, err := session.validatedCoveragePayloadLocked(envelope)
		return preparedActivityObservation{}, err
	case "process.execution":
		var execution workloadtypes.Execution
		if err := decodeActivityPayload(
			envelope.Payload,
			&execution,
		); err != nil {
			return preparedActivityObservation{}, err
		}
		if execution.Validate() != nil ||
			!execution.Owner.Equal(session.preparation.Owner) ||
			execution.SessionID != session.preparation.SessionID ||
			execution.GuestBootID != session.ready.Boundary.GuestBootID ||
			execution.ObserverGeneration !=
				session.preparation.ObserverGeneration ||
			(execution.Exit == nil &&
				execution.StartedAtMonoNS != envelope.MonotonicNS) ||
			(execution.Exit != nil &&
				execution.Exit.AtMonoNS != envelope.MonotonicNS) {
			return preparedActivityObservation{},
				errActivityObservationInvalid
		}
		return preparedActivityObservation{execution: &execution}, nil
	default:
		shape, ok := observedActivityShapes[envelope.Kind]
		if !ok {
			return preparedActivityObservation{}, fmt.Errorf(
				"%w: observation kind %q has no durable record contract",
				errActivityObservationInvalid,
				envelope.Kind,
			)
		}
		record, err := decodeObservedActivity(envelope.Payload)
		if err != nil {
			return preparedActivityObservation{}, err
		}
		if record.Kind != shape.kind ||
			record.Operation != shape.operation ||
			record.RedactionStatus != workloadtypes.RedactionPending ||
			!record.Owner.Equal(session.preparation.Owner) ||
			record.SessionID != session.preparation.SessionID {
			return preparedActivityObservation{},
				errActivityObservationInvalid
		}
		return preparedActivityObservation{record: &record}, nil
	}
}

func decodeObservedActivity(
	payload json.RawMessage,
) (workloadtypes.ActivityRecord, error) {
	var wire observedActivityWire
	if err := decodeActivityPayload(payload, &wire); err != nil {
		return workloadtypes.ActivityRecord{}, err
	}
	var subject any
	switch wire.Kind {
	case workloadtypes.ActivityProcess:
		var value workloadtypes.ProcessSubject
		if err := decodeActivityPayload(wire.Subject, &value); err != nil {
			return workloadtypes.ActivityRecord{}, err
		}
		subject = value
	case workloadtypes.ActivityFile:
		var value workloadtypes.FileSubject
		if err := decodeActivityPayload(wire.Subject, &value); err != nil {
			return workloadtypes.ActivityRecord{}, err
		}
		subject = value
	case workloadtypes.ActivityConnection:
		var value workloadtypes.NetworkSubject
		if err := decodeActivityPayload(wire.Subject, &value); err != nil {
			return workloadtypes.ActivityRecord{}, err
		}
		subject = value
	case workloadtypes.ActivityDNS:
		var value workloadtypes.DNSSubject
		if err := decodeActivityPayload(wire.Subject, &value); err != nil {
			return workloadtypes.ActivityRecord{}, err
		}
		subject = value
	default:
		return workloadtypes.ActivityRecord{}, errActivityObservationInvalid
	}
	record := workloadtypes.ActivityRecord{
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
	if err := record.Validate(); err != nil {
		return workloadtypes.ActivityRecord{}, errors.Join(
			errActivityObservationInvalid,
			err,
		)
	}
	return record, nil
}

func (session *activitySession) ingestLossLocked(
	envelope sessionwire.ObservationEnvelope,
) error {
	payload, reason, err := session.validatedLossPayloadLocked(envelope)
	if err != nil {
		return err
	}
	session.reportedDropped = saturatingAdd(session.reportedDropped, payload.Dropped)
	session.droppedBytes = saturatingAdd(session.droppedBytes, payload.DroppedBytes)
	return session.markAllLossLocked(reason, payload.Dropped, []workloadtypes.CoverageEvidence{
		{Code: payload.Reason},
		{Code: "loss-scope", Value: payload.Scope},
	})
}

func (session *activitySession) validatedLossPayloadLocked(
	envelope sessionwire.ObservationEnvelope,
) (activityLossPayload, string, error) {
	var payload activityLossPayload
	if err := decodeActivityPayload(envelope.Payload, &payload); err != nil {
		return activityLossPayload{}, "", err
	}
	if payload.Dropped == 0 || !activityReasonPattern.MatchString(payload.Reason) {
		return activityLossPayload{}, "", errors.New("observer loss payload has invalid counters or reason")
	}
	reason := coverage.ReasonRingOverflow
	switch payload.Scope {
	case "guest-observer-transport":
		if envelope.CPU != sessionwire.ObserverTransportCPU ||
			payload.Reason != "observer-send-queue-overflow" ||
			payload.DroppedBytes == 0 {
			return activityLossPayload{}, "", errors.New("observer transport loss payload is invalid")
		}
		reason = coverage.ReasonTransportDrop
	case "kernel-ring", "collector-ring":
		if envelope.CPU == sessionwire.ObserverTransportCPU {
			return activityLossPayload{}, "", errors.New("collector ring loss used the reserved transport CPU")
		}
	default:
		return activityLossPayload{}, "", errors.New("observer loss scope is invalid")
	}
	return payload, reason, nil
}

func (session *activitySession) ingestHeartbeatLocked(
	envelope sessionwire.ObservationEnvelope,
) error {
	payload, err := session.validatedHeartbeatPayloadLocked(envelope)
	if err != nil {
		return err
	}
	previous := session.heartbeatByCPU[envelope.CPU]
	kernelDelta := payload.KernelDropped - previous.kernel
	ringDelta := payload.RingDropped - previous.ring
	session.heartbeatByCPU[envelope.CPU] = activityHeartbeatCounters{
		kernel: payload.KernelDropped,
		ring:   payload.RingDropped,
	}
	session.kernelDropped = saturatingAdd(session.kernelDropped, kernelDelta)
	session.ringDropped = saturatingAdd(session.ringDropped, ringDelta)
	dropped := saturatingAdd(kernelDelta, ringDelta)
	if dropped == 0 {
		return nil
	}
	return session.markAllLossLocked(coverage.ReasonRingOverflow, dropped, []workloadtypes.CoverageEvidence{
		{Code: "heartbeat-drop-counter"},
	})
}

func (session *activitySession) validatedHeartbeatPayloadLocked(
	envelope sessionwire.ObservationEnvelope,
) (activityHeartbeatPayload, error) {
	var payload activityHeartbeatPayload
	if err := decodeActivityPayload(envelope.Payload, &payload); err != nil {
		return activityHeartbeatPayload{}, err
	}
	if payload.LatestSequence != envelope.Sequence {
		return activityHeartbeatPayload{}, errors.New("observer heartbeat sequence does not match its envelope")
	}
	previous := session.heartbeatByCPU[envelope.CPU]
	if payload.KernelDropped < previous.kernel || payload.RingDropped < previous.ring {
		return activityHeartbeatPayload{}, errors.New("observer heartbeat loss counter rolled back")
	}
	return payload, nil
}

func (session *activitySession) ingestCoverageLocked(
	envelope sessionwire.ObservationEnvelope,
) error {
	payload, err := session.validatedCoveragePayloadLocked(envelope)
	if err != nil {
		return err
	}
	timeline := session.timelines[payload.Subsystem]
	reason, err := initialCoverageReason(payload.State)
	if err != nil {
		return err
	}
	session.ordinal = saturatingAdd(session.ordinal, 1)
	_, err = timeline.Transition(coverage.Transition{
		State: payload.State, Reason: reason,
		CollectorGeneration: session.preparation.ObserverGeneration,
		DroppedEventCount:   payload.DroppedEventCount,
		Evidence: observerCoverageEvidence(sessionwire.SupervisorCoverageSummary{
			Subsystem: payload.Subsystem, State: payload.State,
			Reason: payload.Reason, Evidence: payload.Evidence,
			DroppedEventCount: payload.DroppedEventCount,
		}),
		Sequence: session.ordinal, At: session.now(),
	})
	if err == nil && payload.DroppedEventCount != 0 {
		session.accountedDropped[payload.Subsystem] = saturatingAdd(
			session.accountedDropped[payload.Subsystem],
			payload.DroppedEventCount,
		)
	}
	if err == nil {
		err = session.persistTimelineLocked(timeline)
	}
	return err
}

func (session *activitySession) validatedCoveragePayloadLocked(
	envelope sessionwire.ObservationEnvelope,
) (activityCoveragePayload, error) {
	var payload activityCoveragePayload
	if err := decodeActivityPayload(envelope.Payload, &payload); err != nil {
		return activityCoveragePayload{}, err
	}
	if session.timelines[payload.Subsystem] == nil ||
		!validCoverageState(payload.State) ||
		!activityReasonPattern.MatchString(payload.Reason) ||
		len(payload.Evidence) > 64 {
		return activityCoveragePayload{}, errors.New("observer coverage change payload is invalid")
	}
	if payload.State == workloadtypes.CoverageAvailable &&
		payload.DroppedEventCount != 0 {
		return activityCoveragePayload{}, workloadtypes.ErrFalseAvailableCoverage
	}
	for _, evidence := range payload.Evidence {
		if !activityReasonPattern.MatchString(evidence) {
			return activityCoveragePayload{}, errors.New("observer coverage evidence is invalid")
		}
	}
	if _, err := initialCoverageReason(payload.State); err != nil {
		return activityCoveragePayload{}, err
	}
	return payload, nil
}

func (session *activitySession) rejectInvalidLocked(
	cause error,
	reason string,
) error {
	session.invalid = saturatingAdd(session.invalid, 1)
	transitionErr := session.markAllLossLocked(reason, 1, []workloadtypes.CoverageEvidence{
		{Code: "observation-rejected"},
	})
	return errors.Join(errActivityObservationInvalid, cause, transitionErr)
}

func (session *activitySession) observerExited(cause error) error {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.observerClosed || session.sessionClosed {
		return nil
	}
	session.observerClosed = true
	session.clearRedactionSnapshotLocked()
	if session.ready == nil {
		return nil
	}
	var result error
	if reason, evidence, invalid, account := observerExitLoss(cause); account {
		if invalid {
			session.invalid = saturatingAdd(session.invalid, 1)
		}
		result = errors.Join(result, session.markAllLossLocked(
			reason,
			1,
			[]workloadtypes.CoverageEvidence{{Code: evidence}},
		))
	}
	result = errors.Join(result, session.markAllStateLocked(
		workloadtypes.CoverageUnavailable,
		coverage.ReasonDaemonDisconnected,
		0,
		[]workloadtypes.CoverageEvidence{{Code: "observer-stream-ended"}},
	))
	return result
}

func observerExitLoss(
	cause error,
) (reason, evidence string, invalid, account bool) {
	if cause == nil || errors.Is(cause, errActivityObservationInvalid) {
		return "", "", false, false
	}
	switch {
	case errors.Is(cause, sessionwire.ErrObserverGenerationRollback):
		return coverage.ReasonCollectorRestarted,
			"observer-generation-rollback",
			true,
			true
	case errors.Is(cause, sessionwire.ErrObserverCRC),
		errors.Is(cause, sessionwire.ErrObserverSchema),
		errors.Is(cause, sessionwire.ErrObserverKind),
		errors.Is(cause, sessionwire.ErrObserverFrameTooLarge),
		errors.Is(cause, sessionwire.ErrObserverIdentity):
		return coverage.ReasonInvalidFrame,
			"observer-stream-rejected",
			true,
			true
	case errors.Is(cause, sessionwire.ErrObserverBackpressure):
		return coverage.ReasonTransportDrop,
			"observer-transport-backpressure",
			false,
			true
	default:
		// Once readiness was accepted, an unexpected end to the observer or
		// supervisor transport means at least one event may have been lost.
		// Account that uncertainty before terminal lifecycle coverage replaces
		// the current state. A clean, proved completion reaches this function
		// with a nil cause and does not add loss.
		return coverage.ReasonTransportDrop,
			"observer-stream-ended-unexpectedly",
			false,
			true
	}
}

func (session *activitySession) sessionExited(
	completion *sessionwire.SupervisorActivityCompletion,
	cause error,
) error {
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.sessionClosed {
		return nil
	}
	observerAlreadyClosed := session.observerClosed
	session.sessionClosed = true
	session.observerClosed = true
	session.clearRedactionSnapshotLocked()
	if session.ready == nil {
		return nil
	}
	var result error
	if lossReason, evidence, invalid, account := observerExitLoss(cause); account &&
		!observerAlreadyClosed {
		if invalid {
			session.invalid = saturatingAdd(session.invalid, 1)
		}
		result = errors.Join(result, session.markAllLossLocked(
			lossReason,
			1,
			[]workloadtypes.CoverageEvidence{{Code: evidence}},
		))
	}
	reason := coverage.ReasonCleanupUnproved
	if completion != nil {
		if err := completion.ValidateReady(session.preparation.SessionID, session.ready); err != nil {
			result = errors.Join(result, err)
		} else {
			result = errors.Join(result, session.reconcileCompletionLocked(completion))
			if completion.CleanupProved {
				reason = coverage.ReasonTargetExited
			}
		}
	}
	result = errors.Join(result, session.markAllStateLocked(
		workloadtypes.CoverageUnavailable,
		reason,
		0,
		[]workloadtypes.CoverageEvidence{{Code: reason}},
	))
	return result
}

func (session *activitySession) clearRedactionSnapshotLocked() {
	if session == nil || session.redactionSnapshot == nil {
		return
	}
	session.redactionSnapshot.Clear()
	session.redactionSnapshot = nil
}

func (session *activitySession) reconcileCompletionLocked(
	completion *sessionwire.SupervisorActivityCompletion,
) error {
	var result error
	for _, summary := range completion.Coverage {
		timeline := session.timelines[summary.Subsystem]
		if timeline == nil {
			result = errors.Join(result, errors.New("activity completion contains an unknown subsystem"))
			continue
		}
		supervisorReported := session.supervisorDropped[summary.Subsystem]
		if summary.DroppedEventCount < supervisorReported {
			result = errors.Join(result, errors.New("activity completion loss counter rolled back"))
			continue
		}
		session.supervisorDropped[summary.Subsystem] = summary.DroppedEventCount
		accounted := session.accountedDropped[summary.Subsystem]
		if summary.DroppedEventCount > accounted {
			delta := summary.DroppedEventCount - accounted
			session.ordinal = saturatingAdd(session.ordinal, 1)
			current, _ := timeline.Current()
			state := workloadtypes.CoveragePartial
			if current.State == workloadtypes.CoverageUnavailable {
				state = workloadtypes.CoverageUnavailable
			}
			_, err := timeline.Transition(coverage.Transition{
				State: state, Reason: coverage.ReasonTransportDrop,
				CollectorGeneration: session.preparation.ObserverGeneration,
				DroppedEventCount:   delta,
				Evidence:            observerCoverageEvidence(summary),
				Sequence:            session.ordinal, At: session.now(),
			})
			if err == nil {
				session.accountedDropped[summary.Subsystem] = summary.DroppedEventCount
				err = session.persistTimelineLocked(timeline)
			}
			result = errors.Join(result, err)
		}
		current, currentErr := timeline.Current()
		if currentErr != nil {
			result = errors.Join(result, currentErr)
			continue
		}
		// A guest terminal summary may make coverage more conservative, but it
		// can never erase a host-observed loss by returning to Available.
		if summary.State == workloadtypes.CoverageAvailable ||
			summary.State == current.State {
			continue
		}
		reason, err := initialCoverageReason(summary.State)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		session.ordinal = saturatingAdd(session.ordinal, 1)
		_, err = timeline.Transition(coverage.Transition{
			State: summary.State, Reason: reason,
			CollectorGeneration: session.preparation.ObserverGeneration,
			Evidence:            observerCoverageEvidence(summary),
			Sequence:            session.ordinal, At: session.now(),
		})
		if err == nil {
			err = session.persistTimelineLocked(timeline)
		}
		result = errors.Join(result, err)
	}
	return result
}

func (session *activitySession) markAllLossLocked(
	reason string,
	dropped uint64,
	evidence []workloadtypes.CoverageEvidence,
) error {
	if dropped == 0 {
		return errors.New("activity loss must be non-zero")
	}
	session.ordinal = saturatingAdd(session.ordinal, 1)
	at := session.now()
	var result error
	for _, subsystem := range activitySubsystems {
		timeline := session.timelines[subsystem]
		if timeline == nil {
			continue
		}
		current, err := timeline.Current()
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		state := workloadtypes.CoveragePartial
		if current.State == workloadtypes.CoverageUnavailable {
			state = workloadtypes.CoverageUnavailable
		}
		_, err = timeline.Transition(coverage.Transition{
			State: state, Reason: reason,
			CollectorGeneration: session.preparation.ObserverGeneration,
			DroppedEventCount:   dropped,
			Evidence:            append([]workloadtypes.CoverageEvidence(nil), evidence...),
			Sequence:            session.ordinal, At: at,
		})
		if err == nil {
			session.accountedDropped[subsystem] = saturatingAdd(
				session.accountedDropped[subsystem],
				dropped,
			)
			err = session.persistTimelineLocked(timeline)
		}
		result = errors.Join(result, err)
	}
	return result
}

func (session *activitySession) markSubsystemLossLocked(
	subsystem, reason string,
	dropped uint64,
	evidence []workloadtypes.CoverageEvidence,
) error {
	if dropped == 0 {
		return errors.New("activity loss must be non-zero")
	}
	timeline := session.timelines[subsystem]
	if timeline == nil {
		return errActivityObservationInvalid
	}
	current, err := timeline.Current()
	if err != nil {
		return err
	}
	state := workloadtypes.CoveragePartial
	if current.State == workloadtypes.CoverageUnavailable {
		state = workloadtypes.CoverageUnavailable
	}
	session.ordinal = saturatingAdd(session.ordinal, 1)
	_, err = timeline.Transition(coverage.Transition{
		State: state, Reason: reason,
		CollectorGeneration: session.preparation.ObserverGeneration,
		DroppedEventCount:   dropped,
		Evidence: append(
			[]workloadtypes.CoverageEvidence(nil),
			evidence...,
		),
		Sequence: session.ordinal, At: session.now(),
	})
	if err != nil {
		return err
	}
	session.accountedDropped[subsystem] = saturatingAdd(
		session.accountedDropped[subsystem],
		dropped,
	)
	return session.persistTimelineLocked(timeline)
}

func (session *activitySession) markAllStateLocked(
	state, reason string,
	dropped uint64,
	evidence []workloadtypes.CoverageEvidence,
) error {
	session.ordinal = saturatingAdd(session.ordinal, 1)
	at := session.now()
	var result error
	for _, subsystem := range activitySubsystems {
		timeline := session.timelines[subsystem]
		if timeline == nil {
			continue
		}
		_, err := timeline.Transition(coverage.Transition{
			State: state, Reason: reason,
			CollectorGeneration: session.preparation.ObserverGeneration,
			DroppedEventCount:   dropped,
			Evidence:            append([]workloadtypes.CoverageEvidence(nil), evidence...),
			Sequence:            session.ordinal, At: at,
		})
		if err == nil {
			err = session.persistTimelineLocked(timeline)
		}
		result = errors.Join(result, err)
	}
	return result
}

func (session *activitySession) snapshot() activitySessionSnapshot {
	session.mu.Lock()
	defer session.mu.Unlock()
	snapshot := activitySessionSnapshot{
		SessionID: session.preparation.SessionID, Owner: session.preparation.Owner,
		Accepted: session.accepted, Duplicates: session.duplicates,
		Missing: session.missing, ReportedDropped: session.reportedDropped,
		DroppedBytes: session.droppedBytes, KernelDropped: session.kernelDropped,
		RingDropped: session.ringDropped, Invalid: session.invalid,
		LastKind: session.lastKind, ObserverClosed: session.observerClosed,
		SessionClosed:       session.sessionClosed,
		RedactionGeneration: session.redactionGeneration,
		Coverage:            make(map[string]coverage.Summary, len(session.timelines)),
		Intervals:           make(map[string][]workloadtypes.CoverageInterval, len(session.timelines)),
	}
	if session.ready != nil {
		boundary := session.ready.Boundary
		snapshot.Boundary = &boundary
	}
	for _, subsystem := range activitySubsystems {
		timeline := session.timelines[subsystem]
		if timeline == nil {
			continue
		}
		snapshot.Coverage[subsystem] = timeline.Summary()
		snapshot.Intervals[subsystem] = timeline.Intervals(time.Time{}, time.Time{})
	}
	return snapshot
}

func (session *activitySession) persistTimelineLocked(
	timeline *coverage.Timeline,
) error {
	if session == nil || timeline == nil || session.persistCoverage == nil {
		return nil
	}
	var result error
	for _, interval := range timeline.Intervals(time.Time{}, time.Time{}) {
		result = errors.Join(result, session.persistCoverage(interval))
	}
	return result
}

func decodeActivityPayload(payload json.RawMessage, target any) error {
	if len(payload) == 0 || target == nil {
		return errActivityObservationInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode control payload", errActivityObservationInvalid)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing control payload", errActivityObservationInvalid)
	}
	return nil
}

func initialCoverageReason(state string) (string, error) {
	switch state {
	case workloadtypes.CoverageAvailable:
		return coverage.ReasonObserverReady, nil
	case workloadtypes.CoveragePartial:
		return coverage.ReasonCollectorPartial, nil
	case workloadtypes.CoverageUnavailable:
		return coverage.ReasonCollectorUnavailable, nil
	default:
		return "", errors.New("observer coverage state is invalid")
	}
}

func validCoverageState(state string) bool {
	switch state {
	case workloadtypes.CoverageAvailable,
		workloadtypes.CoveragePartial,
		workloadtypes.CoverageUnavailable:
		return true
	default:
		return false
	}
}

func observerCoverageEvidence(
	summary sessionwire.SupervisorCoverageSummary,
) []workloadtypes.CoverageEvidence {
	evidence := make([]workloadtypes.CoverageEvidence, 0, len(summary.Evidence)+1)
	if summary.Reason != "" {
		evidence = append(evidence, workloadtypes.CoverageEvidence{
			Code: "observer-reason", Value: summary.Reason,
		})
	}
	for _, code := range summary.Evidence {
		evidence = append(evidence, workloadtypes.CoverageEvidence{Code: code})
	}
	return evidence
}

func cloneActivityReady(
	ready *sessionwire.SupervisorActivityReady,
) *sessionwire.SupervisorActivityReady {
	if ready == nil {
		return nil
	}
	cloned := *ready
	cloned.Coverage = make([]sessionwire.SupervisorCoverageSummary, len(ready.Coverage))
	for index := range ready.Coverage {
		cloned.Coverage[index] = ready.Coverage[index]
		cloned.Coverage[index].Evidence = append(
			[]string(nil),
			ready.Coverage[index].Evidence...,
		)
	}
	return &cloned
}

func inclusiveSequenceCount(from, to uint64) uint64 {
	if from == 0 || to < from {
		return 0
	}
	return to - from + 1
}

func saturatingAdd(left, right uint64) uint64 {
	if right > math.MaxUint64-left {
		return math.MaxUint64
	}
	return left + right
}
