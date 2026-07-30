package manager

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestOperationServiceEnforcesEffectProviderOwnership(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	binding := operationBindingFixture("op_serviceprovider1", digestFixture("b"))
	effects := []EffectResult{{
		ID:       "persist-profile",
		Kind:     "persist",
		Provider: "manager.profile",
		Status:   EffectPending,
	}}
	if _, _, err := store.Reserve(binding, effects); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{OperationClaimed, OperationStaging} {
		if _, err := store.Transition(binding.ID, phase, nil); err != nil {
			t.Fatal(err)
		}
	}
	service := OperationService{Store: store}

	if _, _, err := service.BeginEffect(
		binding.ID,
		"persist-profile",
		"manager.network",
	); !errors.Is(err, ErrOperationProviderMismatch) {
		t.Fatalf("wrong-provider begin error=%v", err)
	}
	operation, err := store.Load(binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Effects[0].Status != EffectPending {
		t.Fatalf("wrong provider claimed effect: %+v", operation.Effects[0])
	}

	operation, execute, err := service.BeginEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
	)
	if err != nil || !execute ||
		operation.Effects[0].Status != EffectRunning {
		t.Fatalf("owned begin execute=%t operation=%+v err=%v", execute, operation, err)
	}
	if _, err := service.FinishEffect(
		binding.ID,
		"persist-profile",
		"manager.network",
		EffectSucceeded,
		nil,
	); !errors.Is(err, ErrOperationProviderMismatch) {
		t.Fatalf("wrong-provider finish error=%v", err)
	}
	operation, err = store.Load(binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Effects[0].Status != EffectRunning {
		t.Fatalf("wrong provider finished effect: %+v", operation.Effects[0])
	}
	if _, err := service.FinishEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
		EffectSucceeded,
		[]EvidenceRef{{Code: "profile-committed"}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Terminal(
		binding.ID,
		OperationSucceeded,
		"profile-committed",
		"profile updated",
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.BeginEffect(
		binding.ID,
		"persist-profile",
		"manager.network",
	); !errors.Is(err, ErrOperationProviderMismatch) {
		t.Fatalf("terminal wrong-provider replay error=%v", err)
	}
}

func TestOperationServiceAppendsCompletedEffectEvidenceIdempotently(
	t *testing.T,
) {
	store := OperationStore{Root: t.TempDir()}
	binding := operationBindingFixture(
		"op_serviceevidence1",
		digestFixture("e"),
	)
	if _, _, err := store.Reserve(
		binding,
		[]EffectResult{{
			ID: "persist-profile", Kind: "persist",
			Provider: "manager.profile", Status: EffectPending,
		}},
	); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{
		OperationClaimed,
		OperationStaging,
	} {
		if _, err := store.Transition(
			binding.ID,
			phase,
			nil,
		); err != nil {
			t.Fatal(err)
		}
	}
	service := OperationService{Store: store}
	if _, execute, err := service.BeginEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
	); err != nil || !execute {
		t.Fatalf("begin execute=%t err=%v", execute, err)
	}
	if _, err := service.FinishEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
		EffectSucceeded,
		[]EvidenceRef{{Code: "profile-committed"}},
	); err != nil {
		t.Fatal(err)
	}
	additional := EvidenceRef{
		Code: "network-authority-reset",
		Ref:  "daemon:daemon_test",
	}
	for replay := 0; replay < 2; replay++ {
		if _, err := service.AppendEffectEvidence(
			binding.ID,
			"persist-profile",
			"manager.profile",
			additional,
		); err != nil {
			t.Fatal(err)
		}
	}
	operation, err := store.Load(binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(operation.Effects[0].Evidence) != 2 ||
		operation.Effects[0].Evidence[1] != additional {
		t.Fatalf(
			"appended evidence=%+v",
			operation.Effects[0].Evidence,
		)
	}
	if _, err := service.AppendEffectEvidence(
		binding.ID,
		"persist-profile",
		"manager.network",
		additional,
	); !errors.Is(err, ErrOperationProviderMismatch) {
		t.Fatalf("wrong provider append error=%v", err)
	}
}

func TestOperationServicePublishesOneTerminalEventAcrossConcurrentReplay(t *testing.T) {
	store := OperationStore{Root: t.TempDir()}
	binding := operationBindingFixture("op_servicefixture1", digestFixture("a"))
	effects := []EffectResult{{
		ID: "persist-profile", Kind: "persist",
		Provider: "manager.profile", Status: EffectPending,
	}}
	if _, _, err := store.Reserve(binding, effects); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(binding.ID, OperationClaimed, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Transition(binding.ID, OperationStaging, nil); err != nil {
		t.Fatal(err)
	}
	if _, execute, err := store.BeginEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
	); err != nil || !execute {
		t.Fatalf("begin effect execute=%t err=%v", execute, err)
	}
	if _, err := store.FinishEffect(
		binding.ID,
		"persist-profile",
		"manager.profile",
		EffectSucceeded,
		[]EvidenceRef{{Code: "profile-committed"}},
	); err != nil {
		t.Fatal(err)
	}
	observer := &operationServiceObserver{}
	service := OperationService{Store: store, Observer: observer}

	const clients = 16
	var wait sync.WaitGroup
	errs := make(chan error, clients)
	for index := 0; index < clients; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			operation, err := service.Terminal(
				binding.ID,
				OperationSucceeded,
				"profile-committed",
				"profile updated",
			)
			if err == nil && operation.Phase != OperationSucceeded {
				t.Errorf("terminal replay phase=%q", operation.Phase)
			}
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if observer.count.Load() != 1 {
		t.Fatalf("terminal event count=%d want 1", observer.count.Load())
	}
}

func TestOperationServiceRefusesEveryUnprovedTerminalClaim(t *testing.T) {
	tests := []struct {
		name        string
		effectState string
		phase       string
		prepare     string
	}{
		{
			name:        "succeeded",
			effectState: EffectSucceeded,
			phase:       OperationSucceeded,
		},
		{
			name:        "failed",
			effectState: EffectFailed,
			phase:       OperationFailed,
		},
		{
			name:        "rolled-back",
			effectState: EffectRolledBack,
			phase:       OperationRolledBack,
			prepare:     OperationRollingBack,
		},
		{
			name:        "rollback-unproved",
			effectState: EffectUnproved,
			phase:       OperationRollbackUnproved,
			prepare:     OperationRollingBack,
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := OperationStore{Root: t.TempDir()}
			binding := operationBindingFixture(
				"op_unprovedterminal"+string(rune('a'+index)),
				digestFixture("d"),
			)
			effects := []EffectResult{{
				ID: "effect", Kind: "persist",
				Provider: "provider", Status: EffectPending,
			}}
			if _, _, err := store.Reserve(binding, effects); err != nil {
				t.Fatal(err)
			}
			for _, phase := range []string{
				OperationClaimed,
				OperationStaging,
			} {
				if _, err := store.Transition(
					binding.ID,
					phase,
					nil,
				); err != nil {
					t.Fatal(err)
				}
			}
			if _, execute, err := store.BeginEffect(
				binding.ID,
				"effect",
				"provider",
			); err != nil || !execute {
				t.Fatalf("begin execute=%t err=%v", execute, err)
			}
			if _, err := store.FinishEffect(
				binding.ID,
				"effect",
				"provider",
				test.effectState,
				nil,
			); err != nil {
				t.Fatal(err)
			}
			if test.prepare != "" {
				if _, err := store.Transition(
					binding.ID,
					test.prepare,
					nil,
				); err != nil {
					t.Fatal(err)
				}
			}
			service := OperationService{Store: store}
			if _, err := service.Terminal(
				binding.ID,
				test.phase,
				"terminal",
				"terminal claim",
			); !errors.Is(err, ErrOperationTerminalUnproved) {
				t.Fatalf(
					"unproved terminal error=%v want %v",
					err,
					ErrOperationTerminalUnproved,
				)
			}
			operation, err := store.Load(binding.ID)
			if err != nil {
				t.Fatal(err)
			}
			if operation.Terminal() {
				t.Fatalf("unproved operation became terminal: %+v", operation)
			}
		})
	}
}

type operationServiceObserver struct {
	count atomic.Int32
}

func (observer *operationServiceObserver) OperationEvent(
	kind, phase string,
	_ map[string]any,
) {
	if kind == "operation" && phase == OperationSucceeded {
		observer.count.Add(1)
	}
}
