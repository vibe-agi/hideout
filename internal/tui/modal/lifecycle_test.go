package modal

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/session"
)

func TestLifecycleModalCancelKeepsSelectionClientLocal(t *testing.T) {
	provider := &lifecycleProviderFixture{}
	editor := NewLifecycle(LifecycleOptions{
		Context: context.Background(), Provider: provider,
		Environments: lifecycleEnvironmentsFixture(),
		Mutable:      true,
	})

	editor.Update(key("j"))
	editor.Update(key("c"))
	_, outcome := editor.Update(specialKey(tea.KeyEsc))
	if !outcome.Close {
		t.Fatal("escape did not close lifecycle selection")
	}
	if provider.planCalls != 0 || provider.applyCalls != 0 {
		t.Fatalf(
			"cancel crossed lifecycle authority: plan=%d apply=%d",
			provider.planCalls,
			provider.applyCalls,
		)
	}
}

func TestLifecycleModalShowsExactActiveBlockersAndDisablesApply(
	t *testing.T,
) {
	provider := &lifecycleProviderFixture{}
	environments := lifecycleEnvironmentsFixture()
	environments[0].ActiveSessions = 1
	environments[0].OwnerHealth = "live"
	editor := NewLifecycle(LifecycleOptions{
		Context: context.Background(), Provider: provider,
		Environments: environments,
		Sessions: []manager.SessionSummary{{
			ID: "ses_activefixture", EnvironmentID: "env_runningfixture",
			State:       session.OwnerStateRunning,
			OwnerStatus: session.OwnerLive,
		}},
		SelectedEnvironmentID: "env_runningfixture",
		Mutable:               true,
	})

	runLifecycleCommand(
		t,
		editor,
		lifecyclePress(t, editor, specialKey(tea.KeyEnter)),
	)
	if editor.Stage() != StageReview || provider.planCalls != 1 {
		t.Fatalf(
			"plan stage=%s calls=%d",
			editor.Stage(),
			provider.planCalls,
		)
	}
	output := editor.View(100)
	for _, expected := range []string{
		"Review environment stop",
		"env_runningfixture",
		"ACTIVE BLOCKERS",
		"ses_activefixture",
		"running",
		"APPLY DISABLED",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("lifecycle blocker review omitted %q:\n%s", expected, output)
		}
	}
	if command := lifecyclePress(t, editor, key("a")); command != nil ||
		editor.Stage() != StageReview ||
		provider.applyCalls != 0 {
		t.Fatal("active blocker allowed lifecycle apply")
	}
}

func TestLifecycleStopNeedsDistinctApplyAndConfirmation(t *testing.T) {
	provider := &lifecycleProviderFixture{}
	editor := NewLifecycle(LifecycleOptions{
		Context: context.Background(), Provider: provider,
		Environments:          lifecycleEnvironmentsFixture(),
		SelectedEnvironmentID: "env_runningfixture",
		Mutable:               true,
	})
	runLifecycleCommand(
		t,
		editor,
		lifecyclePress(t, editor, specialKey(tea.KeyEnter)),
	)
	if command := lifecyclePress(
		t,
		editor,
		specialKey(tea.KeyEnter),
	); command != nil || provider.applyCalls != 0 {
		t.Fatal("Enter on lifecycle review unexpectedly applied")
	}
	lifecyclePress(t, editor, key("a"))
	if editor.Stage() != StageConfirming ||
		!strings.Contains(editor.View(100), "[y/N]") {
		t.Fatalf("stop confirmation is missing:\n%s", editor.View(100))
	}
	lifecyclePress(t, editor, key("n"))
	if editor.Stage() != StageReview || provider.applyCalls != 0 {
		t.Fatal("negative stop confirmation applied")
	}
	lifecyclePress(t, editor, key("a"))
	runLifecycleCommand(
		t,
		editor,
		lifecyclePress(t, editor, key("y")),
	)
	if editor.Stage() != StageTerminal || provider.applyCalls != 1 {
		t.Fatalf(
			"stop terminal stage=%s calls=%d",
			editor.Stage(),
			provider.applyCalls,
		)
	}
	for _, expected := range []string{
		"STOP COMPLETE",
		"env_runningfixture",
		"stopped",
	} {
		if !strings.Contains(editor.View(100), expected) {
			t.Fatalf("stop terminal omitted %q:\n%s", expected, editor.View(100))
		}
	}
}

func TestLifecycleCleanRequiresTypedExactEnvironmentID(t *testing.T) {
	provider := &lifecycleProviderFixture{}
	editor := NewLifecycle(LifecycleOptions{
		Context: context.Background(), Provider: provider,
		Environments:          lifecycleEnvironmentsFixture(),
		SelectedEnvironmentID: "env_stoppedfixture",
		Mutable:               true,
	})
	lifecyclePress(t, editor, key("c"))
	runLifecycleCommand(
		t,
		editor,
		lifecyclePress(t, editor, specialKey(tea.KeyEnter)),
	)
	if editor.Action() != manager.EnvironmentActionClean ||
		!strings.Contains(editor.View(100), "removes environment metadata") {
		t.Fatalf("clean review is not explicit:\n%s", editor.View(100))
	}
	lifecyclePress(t, editor, key("a"))
	if editor.Stage() != StageConfirming ||
		!strings.Contains(editor.View(100), `type "env_stoppedfixture"`) {
		t.Fatalf("clean typed confirmation is missing:\n%s", editor.View(100))
	}
	lifecycleType(t, editor, "wrong")
	if command := lifecyclePress(
		t,
		editor,
		specialKey(tea.KeyEnter),
	); command != nil || provider.applyCalls != 0 {
		t.Fatal("wrong clean confirmation applied")
	}
	for range len("wrong") {
		lifecyclePress(t, editor, specialKey(tea.KeyBackspace))
	}
	lifecycleType(t, editor, "env_stoppedfixture")
	runLifecycleCommand(
		t,
		editor,
		lifecyclePress(t, editor, specialKey(tea.KeyEnter)),
	)
	if editor.Stage() != StageTerminal || provider.applyCalls != 1 ||
		!strings.Contains(editor.View(100), "CLEAN COMPLETE") {
		t.Fatalf(
			"clean terminal mismatch stage=%s calls=%d:\n%s",
			editor.Stage(),
			provider.applyCalls,
			editor.View(100),
		)
	}
}

func TestLifecycleAuthorityChangeInvalidatesReviewedPlan(t *testing.T) {
	provider := &lifecycleProviderFixture{}
	environments := lifecycleEnvironmentsFixture()
	editor := NewLifecycle(LifecycleOptions{
		Context: context.Background(), Provider: provider,
		Environments:          environments,
		SelectedEnvironmentID: "env_runningfixture",
		Mutable:               true,
	})
	runLifecycleCommand(
		t,
		editor,
		lifecyclePress(t, editor, specialKey(tea.KeyEnter)),
	)
	environments[0].Status = "stopped"
	editor.SyncAuthority(
		true,
		environments,
		nil,
		"",
	)
	if editor.Stage() != StageStale ||
		!strings.Contains(editor.View(100), "environment state changed") {
		t.Fatalf("changed lifecycle projection was not stale:\n%s", editor.View(100))
	}
	if command := lifecyclePress(t, editor, key("a")); command != nil ||
		provider.applyCalls != 0 {
		t.Fatal("stale lifecycle plan applied")
	}
}

type lifecycleProviderFixture struct {
	planCalls  int
	applyCalls int
	planErr    error
	applyErr   error
	lastAction string
	lastReq    manager.EnvironmentActionAPIRequest
	lastPlan   manager.EnvironmentActionPlan
}

func (provider *lifecycleProviderFixture) PlanEnvironment(
	_ context.Context,
	action string,
	request manager.EnvironmentActionAPIRequest,
) (manager.EnvironmentActionPlan, error) {
	provider.planCalls++
	provider.lastAction = action
	provider.lastReq = request
	if provider.planErr != nil {
		return manager.EnvironmentActionPlan{}, provider.planErr
	}
	if len(request.IDs) != 1 {
		return manager.EnvironmentActionPlan{}, errors.New("expected exact ID")
	}
	target := manager.EnvironmentActionTarget{
		ID: request.IDs[0], Profile: "default", Backend: "lima",
		Status: "running", InstanceName: "hideout-fixture",
		CreatedAt: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
	}
	if action == manager.EnvironmentActionClean {
		target.Status = "stopped"
	}
	provider.lastPlan = manager.EnvironmentActionPlan{
		Action: action, RequestedIDs: append([]string(nil), request.IDs...),
		Targets:     []manager.EnvironmentActionTarget{target},
		Skipped:     []manager.EnvironmentActionTarget{},
		Total:       1,
		OperationID: "op_lifecyclefixture",
		PlanDigest:  "sha256:" + strings.Repeat("a", 64),
	}
	return provider.lastPlan, nil
}

func (provider *lifecycleProviderFixture) ApplyEnvironment(
	_ context.Context,
	action string,
	request manager.EnvironmentActionAPIRequest,
) (manager.EnvironmentActionResult, error) {
	provider.applyCalls++
	if provider.applyErr != nil {
		return manager.EnvironmentActionResult{}, provider.applyErr
	}
	if action != provider.lastAction ||
		len(request.IDs) != 1 ||
		request.IDs[0] != provider.lastReq.IDs[0] ||
		request.OperationID != provider.lastPlan.OperationID ||
		request.PlanDigest != provider.lastPlan.PlanDigest ||
		!request.Confirmed {
		return manager.EnvironmentActionResult{}, errors.New(
			"lifecycle apply request mismatch",
		)
	}
	applied := provider.lastPlan.Targets[0]
	if action == manager.EnvironmentActionStop {
		applied.Status = "stopped"
	}
	return manager.EnvironmentActionResult{
		Plan:    provider.lastPlan,
		Applied: []manager.EnvironmentActionTarget{applied},
		Skipped: []manager.EnvironmentActionTarget{},
		Operation: lifecycleOperationFixture(
			provider.lastPlan,
			request.IDs[0],
		),
	}, nil
}

func lifecycleOperationFixture(
	plan manager.EnvironmentActionPlan,
	targetID string,
) *manager.Operation {
	now := time.Date(2026, 7, 29, 10, 0, 1, 0, time.UTC)
	return &manager.Operation{
		Schema: manager.OperationSchema,
		ID:     plan.OperationID, Kind: "environment." + plan.Action,
		Owner: manager.OperationOwner{
			Kind: "environment", ID: targetID,
		},
		PlanDigest: plan.PlanDigest,
		Phase:      manager.OperationSucceeded,
		Effects: []manager.EffectResult{{
			ID: "environment-0", Kind: "prove",
			Provider: "daemon.lifecycle." + plan.Action,
			Status:   manager.EffectSucceeded,
			Evidence: []manager.EvidenceRef{{
				Code: "backend-terminal-stable",
			}},
		}},
		Result: &manager.OperationResult{
			Status:  manager.OperationSucceeded,
			Code:    "environment-action-completed",
			Summary: "The exact environment action completed.",
		},
		Recovery: manager.Recovery{
			Code:    "retry-operation",
			Summary: "Retry the same operation identity.",
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func lifecycleEnvironmentsFixture() []manager.EnvironmentSummary {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return []manager.EnvironmentSummary{
		{
			ID: "env_runningfixture", Name: "running", Profile: "default",
			Backend: "lima", Status: "running",
			InstanceName: "hideout-running", CreatedAt: now.Add(-time.Hour),
		},
		{
			ID: "env_stoppedfixture", Name: "stopped", Profile: "default",
			Backend: "lima", Status: "stopped",
			InstanceName: "hideout-stopped", CreatedAt: now.Add(-2 * time.Hour),
		},
	}
}

func lifecyclePress(
	t *testing.T,
	editor *Lifecycle,
	message tea.KeyPressMsg,
) tea.Cmd {
	t.Helper()
	command, _ := editor.Update(message)
	return command
}

func runLifecycleCommand(
	t *testing.T,
	editor *Lifecycle,
	command tea.Cmd,
) {
	t.Helper()
	if command == nil {
		t.Fatal("expected lifecycle command")
	}
	message := command()
	if message == nil {
		t.Fatal("lifecycle command returned nil message")
	}
	editor.Update(message)
}

func lifecycleType(t *testing.T, editor *Lifecycle, value string) {
	t.Helper()
	for _, character := range value {
		lifecyclePress(t, editor, key(string(character)))
	}
}
