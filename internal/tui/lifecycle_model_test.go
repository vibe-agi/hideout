package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/session"
)

func TestModelLifecycleModalSelectsExactSessionEnvironmentAndShowsBlocker(
	t *testing.T,
) {
	state := lifecycleModelState()
	state.Overview.Sessions = []manager.SessionSummary{{
		ID: "ses_lifecyclefixture", EnvironmentID: "env_runningfixture",
		Profile: "default", State: session.OwnerStateRunning,
		OwnerStatus: session.OwnerLive, CommandClass: "claude",
	}}
	state.Overview.Environments[0].ActiveSessions = 1
	state.Overview.Environments[0].OwnerHealth = "live"
	provider := &modelLifecycleProvider{}
	model := NewModel(ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true, LifecycleProvider: provider,
	})

	model = updateModel(t, model, key("e"))
	if model.TopModal() != ModalLifecycle ||
		model.LifecycleStage() != tuimodalStageEditing {
		t.Fatalf(
			"lifecycle modal did not open: modal=%s stage=%s",
			model.TopModal(),
			model.LifecycleStage(),
		)
	}
	if !strings.Contains(
		model.View().Content,
		"> env_runningfixture",
	) {
		t.Fatalf(
			"selected session environment is not selected:\n%s",
			model.View().Content,
		)
	}
	model = runLifecycleModelCommand(
		t,
		model,
		specialKey(tea.KeyEnter),
	)
	for _, expected := range []string{
		"Review environment stop",
		"ses_lifecyclefixture",
		"ACTIVE BLOCKERS",
		"APPLY DISABLED",
	} {
		if !strings.Contains(model.View().Content, expected) {
			t.Fatalf(
				"lifecycle modal omitted %q:\n%s",
				expected,
				model.View().Content,
			)
		}
	}
	model = updateModel(t, model, key("a"))
	if provider.applyCalls != 0 ||
		model.LifecycleStage() != tuimodalStageReview {
		t.Fatal("blocked lifecycle review crossed apply authority")
	}
}

func TestModelLifecycleCleanAppliesExactTargetAndUpdatesInventory(
	t *testing.T,
) {
	state := lifecycleModelState()
	state.Overview.Sessions = nil
	state.Overview.Environments = []manager.EnvironmentSummary{
		state.Overview.Environments[1],
	}
	provider := &modelLifecycleProvider{}
	model := NewModel(ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true, LifecycleProvider: provider,
	})

	model = updateModel(t, model, key("e"))
	model = updateModel(t, model, key("c"))
	model = runLifecycleModelCommand(
		t,
		model,
		specialKey(tea.KeyEnter),
	)
	model = updateModel(t, model, key("a"))
	for _, character := range "env_stoppedfixture" {
		model = updateModel(t, model, key(string(character)))
	}
	model = runLifecycleModelCommand(
		t,
		model,
		specialKey(tea.KeyEnter),
	)
	if provider.planCalls != 1 || provider.applyCalls != 1 ||
		provider.lastAction != manager.EnvironmentActionClean ||
		provider.lastTarget != "env_stoppedfixture" {
		t.Fatalf(
			"lifecycle authority mismatch: plan=%d apply=%d action=%q target=%q",
			provider.planCalls,
			provider.applyCalls,
			provider.lastAction,
			provider.lastTarget,
		)
	}
	if len(model.ConsoleState().Overview.Environments) != 0 ||
		model.LifecycleStage() != tuimodalStageTerminal ||
		!strings.Contains(model.View().Content, "CLEAN COMPLETE") {
		t.Fatalf(
			"clean result did not update exact inventory:\n%s\nstate=%+v",
			model.View().Content,
			model.ConsoleState().Overview.Environments,
		)
	}
}

func TestModelStaleStateNeverOpensLifecycleMutation(t *testing.T) {
	state := lifecycleModelState()
	state.ReadOnly = true
	state.RequiresReseed = true
	state.StreamHealth = liveconsole.StreamHealth{
		State: liveconsole.HealthStale, Reason: "sequence gap",
	}
	model := NewModel(ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true, LifecycleProvider: &modelLifecycleProvider{},
	})
	model = updateModel(t, model, key("e"))
	if model.ModalDepth() != 0 {
		t.Fatalf(
			"stale state opened lifecycle authority: modal=%s",
			model.TopModal(),
		)
	}
}

// Local aliases keep this test independent from modal implementation details
// while still asserting the public stage values exposed by the model.
const (
	tuimodalStageEditing  = "editing-draft"
	tuimodalStageReview   = "review"
	tuimodalStageTerminal = "terminal"
)

type modelLifecycleProvider struct {
	planCalls  int
	applyCalls int
	lastAction string
	lastTarget string
	plan       manager.EnvironmentActionPlan
}

func (provider *modelLifecycleProvider) PlanEnvironment(
	_ context.Context,
	action string,
	request manager.EnvironmentActionAPIRequest,
) (manager.EnvironmentActionPlan, error) {
	provider.planCalls++
	if len(request.IDs) != 1 {
		return manager.EnvironmentActionPlan{}, errors.New(
			"expected exact lifecycle target",
		)
	}
	provider.lastAction = action
	provider.lastTarget = request.IDs[0]
	status := "running"
	if action == manager.EnvironmentActionClean {
		status = "stopped"
	}
	provider.plan = manager.EnvironmentActionPlan{
		OperationID: "op_lifecyclefixture",
		PlanDigest:  "sha256:" + strings.Repeat("a", 64),
		Action:      action, RequestedIDs: []string{request.IDs[0]},
		Targets: []manager.EnvironmentActionTarget{{
			ID: request.IDs[0], Profile: "default", Backend: "lima",
			Status: status, InstanceName: "hideout-fixture",
		}},
		Skipped: []manager.EnvironmentActionTarget{},
		Total:   1,
	}
	return provider.plan, nil
}

func (provider *modelLifecycleProvider) ApplyEnvironment(
	_ context.Context,
	action string,
	request manager.EnvironmentActionAPIRequest,
) (manager.EnvironmentActionResult, error) {
	provider.applyCalls++
	if action != provider.lastAction ||
		len(request.IDs) != 1 ||
		request.IDs[0] != provider.lastTarget {
		return manager.EnvironmentActionResult{}, errors.New(
			"lifecycle apply mismatch",
		)
	}
	target := provider.plan.Targets[0]
	if action == manager.EnvironmentActionStop {
		target.Status = "stopped"
	}
	now := time.Date(2026, 7, 29, 12, 1, 0, 0, time.UTC)
	operation := &manager.Operation{
		Schema: manager.OperationSchema, ID: provider.plan.OperationID,
		Kind: "environment." + action,
		Owner: manager.OperationOwner{
			Kind: "environment", ID: provider.lastTarget,
		},
		PlanDigest: provider.plan.PlanDigest,
		Phase:      manager.OperationSucceeded,
		Effects: []manager.EffectResult{{
			ID: "lifecycle-effect", Kind: "cleanup",
			Provider: "tui.test", Status: manager.EffectSucceeded,
			Evidence: []manager.EvidenceRef{{
				Code: "environment-action-proved",
			}},
		}},
		Result: &manager.OperationResult{
			Status: "succeeded", Code: "environment-action-complete",
			Summary: "The exact environment action completed.",
		},
		Recovery: manager.Recovery{
			Code:    "environment-action-complete",
			Summary: "No recovery is required.",
		},
		CreatedAt: now, UpdatedAt: now,
	}
	return manager.EnvironmentActionResult{
		Plan: provider.plan,
		Applied: []manager.EnvironmentActionTarget{
			target,
		},
		Skipped:   []manager.EnvironmentActionTarget{},
		Operation: operation,
	}, nil
}

func lifecycleModelState() liveconsole.State {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return liveconsole.NewState(liveconsole.BuildSeed(
		liveconsole.SeedInput{
			DaemonInstanceID:     "daemon_lifecyclefixture",
			CredentialGeneration: 1,
			EventSequence:        1,
			StreamHealth:         liveconsole.HealthLive,
			Overview: manager.Overview{
				Version: "hideout.manager/v1",
				Environments: []manager.EnvironmentSummary{
					{
						ID: "env_runningfixture", Name: "running",
						Profile: "default", Backend: "lima",
						Status:       "running",
						InstanceName: "hideout-running",
						CreatedAt:    now.Add(-time.Hour),
					},
					{
						ID: "env_stoppedfixture", Name: "stopped",
						Profile: "default", Backend: "lima",
						Status:       "stopped",
						InstanceName: "hideout-stopped",
						CreatedAt:    now.Add(-2 * time.Hour),
					},
				},
			},
		},
	))
}

func runLifecycleModelCommand(
	t *testing.T,
	model *Model,
	message tea.Msg,
) *Model {
	t.Helper()
	next, command := updateModelWithCommand(t, model, message)
	if command == nil {
		t.Fatal("expected asynchronous lifecycle command")
	}
	return updateModel(t, next, command())
}
