package manager

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/vibe-agi/hideout/internal/lifecycle"
)

// runLifecycleEffects bridges provider-specific callbacks to the generic
// daemon-owned lifecycle registrar. It carries no capability authority.
type runLifecycleEffects struct {
	mu           sync.Mutex
	registration lifecycle.Registration
	sessionID    string
	sequence     uint64
}

// runLifecycleTracker records provider dependencies before startup authority
// is exercised and owns rollback until the completed data plane takes over.
// It contains lifecycle metadata only; provider cleanup remains with Manager.
type runLifecycleTracker struct {
	registration lifecycle.Registration
	refs         []lifecycle.ResourceRef
	committed    bool
}

func (t *runLifecycleTracker) commit(ctx context.Context) error {
	if t == nil || t.registration == nil || t.committed {
		return nil
	}
	if err := t.registration.Commit(ctx); err != nil {
		return err
	}
	t.committed = true
	return nil
}

func newRunLifecycleTracker(registration lifecycle.Registration) *runLifecycleTracker {
	return &runLifecycleTracker{registration: registration}
}

func (t *runLifecycleTracker) plan(ctx context.Context, spec lifecycle.RegistrationSpec) (lifecycle.ResourceRef, error) {
	if t == nil || t.registration == nil {
		return lifecycle.ResourceRef{}, nil
	}
	spec.State = lifecycle.StatePlanned
	ref, err := t.registration.Register(ctx, spec)
	if err != nil {
		return lifecycle.ResourceRef{}, err
	}
	t.refs = append(t.refs, ref)
	return ref, nil
}

func (t *runLifecycleTracker) activate(ctx context.Context, ref lifecycle.ResourceRef) error {
	if t == nil || t.registration == nil || ref.Kind == "" {
		return nil
	}
	return activateLifecycleResource(ctx, t.registration, ref)
}

func activateLifecycleResource(ctx context.Context, registration lifecycle.Registration, ref lifecycle.ResourceRef) error {
	if registration == nil || ref.Kind == "" {
		return nil
	}
	if err := registration.Transition(ctx, ref, lifecycle.StateStarting); err != nil {
		// A planned consumer may join an identical shared provider that another
		// session already activated. Confirming active is idempotent and must not
		// downgrade that shared resource back to starting.
		if activeErr := registration.Transition(ctx, ref, lifecycle.StateActive); activeErr == nil {
			return nil
		}
		return err
	}
	return registration.Transition(ctx, ref, lifecycle.StateActive)
}

func (t *runLifecycleTracker) rollback(cleanupErr error) error {
	if t == nil || t.registration == nil {
		return nil
	}
	if !t.committed {
		t.refs = nil
		return nil
	}
	var result error
	for index := len(t.refs) - 1; index >= 0; index-- {
		result = errors.Join(result, t.registration.Release(context.Background(), t.refs[index], cleanupErr))
	}
	t.refs = nil
	return result
}

func (t *runLifecycleTracker) transfer() []lifecycle.ResourceRef {
	if t == nil {
		return nil
	}
	refs := append([]lifecycle.ResourceRef(nil), t.refs...)
	t.refs = nil
	return refs
}

func newRunLifecycleEffects(registration lifecycle.Registration, sessionID string) *runLifecycleEffects {
	return &runLifecycleEffects{registration: registration, sessionID: sessionID}
}

func (e *runLifecycleEffects) beginHandoff(_ string) (func(bool) error, error) {
	return e.beginFacts([]lifecycle.FactSpec{{
		Kind: lifecycle.KindHostAppHandoff, ID: e.nextID("handoff"), Class: lifecycle.FactHandoff,
	}}), nil
}

func (e *runLifecycleEffects) beginHostFSStage() (func(bool) error, error) {
	id := e.nextID("hostfs")
	return e.beginFacts([]lifecycle.FactSpec{
		{Kind: lifecycle.KindHostFSStaged, ID: id + "-object", Class: lifecycle.FactRetained},
		{Kind: lifecycle.KindDecisionRecord, ID: id + "-decision", Class: lifecycle.FactRetained},
	}), nil
}

func (e *runLifecycleEffects) beginFacts(specs []lifecycle.FactSpec) func(bool) error {
	if e == nil || e.registration == nil {
		return func(bool) error { return nil }
	}
	var once sync.Once
	var result error
	return func(success bool) error {
		once.Do(func() {
			if !success {
				return
			}
			for _, spec := range specs {
				result = errors.Join(result, e.registration.RecordFact(context.Background(), spec))
			}
		})
		return result
	}
}

func (e *runLifecycleEffects) nextID(prefix string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sequence++
	return fmt.Sprintf("%s-%s-%d", prefix, e.sessionID, e.sequence)
}
