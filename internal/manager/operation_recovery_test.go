package manager

import (
	"errors"
	"os"
	"testing"
)

const recoveryTraceMutationEnv = "HIDEOUT_RECOVERY_TRACE_MUTATION"

type recoveryTraceCrashPoint struct {
	boundary string
	after    bool
}

func (point recoveryTraceCrashPoint) name() string {
	if point.after {
		return "after-" + point.boundary
	}
	return "before-" + point.boundary
}

func operationRecoveryCrashPoints() []recoveryTraceCrashPoint {
	boundaries := []string{
		"persist",
		"claim",
		"stage",
		"activate",
		"proof",
		"commit",
		"event",
		"response",
	}
	points := make([]recoveryTraceCrashPoint, 0, len(boundaries)*2)
	for _, boundary := range boundaries {
		points = append(
			points,
			recoveryTraceCrashPoint{boundary: boundary},
			recoveryTraceCrashPoint{boundary: boundary, after: true},
		)
	}
	return points
}

func TestOperationRecoveryCrashMatrix(t *testing.T) {
	for _, point := range operationRecoveryCrashPoints() {
		t.Run(point.name(), func(t *testing.T) {
			trace := newOperationRecoveryTrace(t)
			trace.runToCrash(t, point)
			recovered, state := trace.restart(t, "")

			switch {
			case point.boundary == "persist" && !point.after:
				if state != "absent" || trace.provider.calls != 0 {
					t.Fatalf(
						"unpersisted request recovered as authority: state=%s calls=%d",
						state,
						trace.provider.calls,
					)
				}
			case (point.boundary == "persist" && point.after) ||
				(point.boundary == "claim" && !point.after):
				if state != OperationPlanned ||
					recovered.Phase != OperationPlanned ||
					trace.provider.calls != 0 {
					t.Fatalf(
						"unconfirmed plan crossed the effect boundary: state=%s operation=%+v calls=%d",
						state,
						recovered,
						trace.provider.calls,
					)
				}
				trace.assertExactReplay(t, recovered, false)
			default:
				trace.assertSuccessfulRecovery(t, recovered)
				trace.assertExactReplay(t, recovered, true)
			}

			if trace.observer.terminalEvents > 1 {
				t.Fatalf(
					"recovery published duplicate terminal events: %d",
					trace.observer.terminalEvents,
				)
			}
		})
	}
}

type operationRecoveryTrace struct {
	store      OperationStore
	service    OperationService
	binding    OperationBinding
	effect     EffectResult
	provider   *recoveryTraceProvider
	observer   *recoveryTraceObserver
	responded  bool
	persisted  bool
	lastAction string
}

func newOperationRecoveryTrace(t *testing.T) *operationRecoveryTrace {
	t.Helper()
	store := OperationStore{Root: t.TempDir()}
	observer := &recoveryTraceObserver{}
	return &operationRecoveryTrace{
		store:   store,
		service: OperationService{Store: store, Observer: observer},
		binding: OperationBinding{
			ID:           "op_crashmatrix001",
			Kind:         "profile.transaction",
			Owner:        OperationOwner{Kind: "profile", ID: "default"},
			PlanDigest:   digestFixture("7"),
			BaseRevision: 1,
		},
		effect: EffectResult{
			ID:       "persist-profile",
			Kind:     "persist",
			Provider: "manager.profile",
			Status:   EffectPending,
		},
		provider: &recoveryTraceProvider{},
		observer: observer,
	}
}

func (trace *operationRecoveryTrace) runToCrash(
	t *testing.T,
	point recoveryTraceCrashPoint,
) {
	t.Helper()
	steps := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "persist", run: trace.persist},
		{name: "claim", run: trace.claim},
		{name: "stage", run: trace.stage},
		{name: "activate", run: trace.activate},
		{name: "proof", run: trace.proof},
		{name: "commit", run: trace.commit},
		{name: "event", run: trace.publishEvent},
		{name: "response", run: trace.deliverResponse},
	}
	for _, step := range steps {
		if step.name == point.boundary && !point.after {
			return
		}
		step.run(t)
		trace.lastAction = step.name
		if step.name == point.boundary && point.after {
			return
		}
	}
	t.Fatalf("unknown recovery crash point %q", point.name())
}

func (trace *operationRecoveryTrace) persist(t *testing.T) {
	t.Helper()
	_, created, err := trace.store.Reserve(
		trace.binding,
		[]EffectResult{trace.effect},
	)
	if err != nil || !created {
		t.Fatalf("persist operation created=%t err=%v", created, err)
	}
	trace.persisted = true
}

func (trace *operationRecoveryTrace) claim(t *testing.T) {
	t.Helper()
	if _, err := trace.service.Transition(
		trace.binding.ID,
		OperationClaimed,
	); err != nil {
		t.Fatal(err)
	}
}

func (trace *operationRecoveryTrace) stage(t *testing.T) {
	t.Helper()
	if _, err := trace.service.Transition(
		trace.binding.ID,
		OperationStaging,
	); err != nil {
		t.Fatal(err)
	}
}

func (trace *operationRecoveryTrace) activate(t *testing.T) {
	t.Helper()
	if _, err := trace.service.Transition(
		trace.binding.ID,
		OperationActivating,
	); err != nil {
		t.Fatal(err)
	}
	_, execute, err := trace.service.BeginEffect(
		trace.binding.ID,
		trace.effect.ID,
		trace.effect.Provider,
	)
	if err != nil || !execute {
		t.Fatalf("activate effect execute=%t err=%v", execute, err)
	}
	trace.provider.apply()
}

func (trace *operationRecoveryTrace) proof(t *testing.T) {
	t.Helper()
	if !trace.provider.applied {
		t.Fatal("proof attempted before provider activation")
	}
	if _, err := trace.service.FinishEffect(
		trace.binding.ID,
		trace.effect.ID,
		trace.effect.Provider,
		EffectSucceeded,
		recoveryTraceEvidence(),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := trace.service.Transition(
		trace.binding.ID,
		OperationProving,
	); err != nil {
		t.Fatal(err)
	}
}

// Commit is deliberately split from event publication. OperationService
// performs these in that order; the split lets the table model a process death
// after the durable terminal record but before the best-effort event callback.
func (trace *operationRecoveryTrace) commit(t *testing.T) {
	t.Helper()
	current, err := trace.store.Load(trace.binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateOperationTerminalEvidence(
		current,
		OperationSucceeded,
	); err != nil {
		t.Fatal(err)
	}
	_, changed, err := trace.store.TransitionIfChanged(
		trace.binding.ID,
		OperationSucceeded,
		&OperationResult{
			Status:  OperationSucceeded,
			Code:    "profile-committed",
			Summary: "profile committed",
		},
	)
	if err != nil || !changed {
		t.Fatalf("commit changed=%t err=%v", changed, err)
	}
}

func (trace *operationRecoveryTrace) publishEvent(t *testing.T) {
	t.Helper()
	current, err := trace.store.Load(trace.binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Phase != OperationSucceeded {
		t.Fatalf("event published before terminal commit: %+v", current)
	}
	trace.observer.OperationEvent(
		"operation",
		current.Phase,
		map[string]any{"id": current.ID},
	)
}

func (trace *operationRecoveryTrace) deliverResponse(t *testing.T) {
	t.Helper()
	replayed, created, err := trace.store.Reserve(
		trace.binding,
		[]EffectResult{trace.effect},
	)
	if err != nil || created || replayed.Phase != OperationSucceeded {
		t.Fatalf(
			"terminal response replay created=%t operation=%+v err=%v",
			created,
			replayed,
			err,
		)
	}
	trace.responded = true
}

func (trace *operationRecoveryTrace) restart(
	t *testing.T,
	mutation string,
) (Operation, string) {
	t.Helper()
	trace.store = OperationStore{Root: trace.store.Root}
	trace.service = OperationService{
		Store: trace.store, Observer: trace.observer,
	}
	operation, err := trace.store.Load(trace.binding.ID)
	if errors.Is(err, os.ErrNotExist) {
		return Operation{}, "absent"
	}
	if err != nil {
		t.Fatal(err)
	}
	if operation.Terminal() || operation.Phase == OperationPlanned {
		return operation, operation.Phase
	}

	if operation.Phase == OperationClaimed {
		operation, err = trace.service.Transition(
			operation.ID,
			OperationStaging,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	if operation.Phase == OperationStaging {
		operation, err = trace.service.Transition(
			operation.ID,
			OperationActivating,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	switch operation.Effects[0].Status {
	case EffectPending:
		var execute bool
		operation, execute, err = trace.service.BeginEffect(
			operation.ID,
			trace.effect.ID,
			trace.effect.Provider,
		)
		if err != nil {
			t.Fatal(err)
		}
		if execute {
			trace.provider.apply()
		}
	case EffectRunning, EffectSucceeded:
	default:
		t.Fatalf("unsupported recovery effect state: %+v", operation.Effects[0])
	}
	if operation.Effects[0].Status == EffectRunning {
		if !trace.provider.applied {
			operation, err = trace.service.RequireRecovery(
				operation.ID,
				"provider-completion-unproved",
				"provider completion is unknown",
				"inspect provider state",
			)
			if err != nil {
				t.Fatal(err)
			}
			return operation, operation.Phase
		}
		if mutation == "replay-running-effect" {
			trace.provider.apply()
		}
		if mutation == "success-without-proof" {
			operation, err = trace.store.Transition(
				operation.ID,
				OperationSucceeded,
				&OperationResult{
					Status:  OperationSucceeded,
					Code:    "mutated-success",
					Summary: "mutant bypassed proof",
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			trace.observer.OperationEvent(
				"operation",
				OperationSucceeded,
				map[string]any{"id": operation.ID},
			)
			return operation, operation.Phase
		}
		operation, err = trace.service.FinishEffect(
			operation.ID,
			trace.effect.ID,
			trace.effect.Provider,
			EffectSucceeded,
			recoveryTraceEvidence(),
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	if operation.Phase != OperationProving {
		operation, err = trace.service.Transition(
			operation.ID,
			OperationProving,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	operation, err = trace.service.Terminal(
		operation.ID,
		OperationSucceeded,
		"profile-committed",
		"profile committed",
	)
	if err != nil {
		t.Fatal(err)
	}
	if mutation == "duplicate-terminal-event" {
		trace.observer.OperationEvent(
			"operation",
			OperationSucceeded,
			map[string]any{"id": operation.ID},
		)
	}
	return operation, operation.Phase
}

func (trace *operationRecoveryTrace) assertSuccessfulRecovery(
	t *testing.T,
	operation Operation,
) {
	t.Helper()
	if operation.Phase != OperationSucceeded ||
		len(operation.Effects) != 1 ||
		operation.Effects[0].Status != EffectSucceeded ||
		len(operation.Effects[0].Evidence) == 0 {
		t.Fatalf(
			"recovery invariant violated: terminal state lacks durable proof: %+v",
			operation,
		)
	}
	if trace.provider.calls != 1 {
		t.Fatalf(
			"recovery invariant violated: provider calls=%d want 1",
			trace.provider.calls,
		)
	}
	if trace.observer.terminalEvents > 1 {
		t.Fatalf(
			"recovery invariant violated: terminal events=%d want at most 1",
			trace.observer.terminalEvents,
		)
	}
}

func (trace *operationRecoveryTrace) assertExactReplay(
	t *testing.T,
	before Operation,
	requireTerminal bool,
) {
	t.Helper()
	calls := trace.provider.calls
	replayed, created, err := trace.store.Reserve(
		trace.binding,
		[]EffectResult{trace.effect},
	)
	if err != nil || created || !replayed.Matches(before.Binding()) ||
		replayed.Phase != before.Phase {
		t.Fatalf(
			"exact replay changed operation: created=%t before=%+v replay=%+v err=%v",
			created,
			before,
			replayed,
			err,
		)
	}
	if requireTerminal && !replayed.Terminal() {
		t.Fatalf("exact replay lost terminal result: %+v", replayed)
	}
	if trace.provider.calls != calls {
		t.Fatalf(
			"exact replay invoked provider: before=%d after=%d",
			calls,
			trace.provider.calls,
		)
	}
}

func recoveryTraceEvidence() []EvidenceRef {
	return []EvidenceRef{{
		Code: "profile-persisted",
		Ref:  "profile:default",
	}}
}

type recoveryTraceProvider struct {
	calls   int
	applied bool
}

func (provider *recoveryTraceProvider) apply() {
	provider.calls++
	provider.applied = true
}

type recoveryTraceObserver struct {
	terminalEvents int
}

func (observer *recoveryTraceObserver) OperationEvent(
	kind, phase string,
	_ map[string]any,
) {
	if kind == "operation" && phase == OperationSucceeded {
		observer.terminalEvents++
	}
}
