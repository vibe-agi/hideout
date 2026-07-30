package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
	tuimodel "github.com/vibe-agi/hideout/internal/tui"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

func TestTUIConfigPTYDraftReviewConfirmApplyAndTerminalEvidence(
	t *testing.T,
) {
	authority, state := newTUIConfigPTYFixture(t)
	model := tuimodel.NewModel(tuimodel.ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true, Unicode: false,
		Context:        context.Background(),
		ConfigProvider: authority,
	})
	output, final, err := exerciseTUIProgramSteps(
		t,
		model,
		[]tuiPTYStep{
			{wait: "Hideout", input: "3"},
			{wait: "Config · default", input: "\r"},
			{wait: "Edit Connection mode", input: "proxy\r"},
			{wait: "Review configuration change", input: "a"},
			{wait: "Apply this exact operation? [y/N]", input: "y"},
			{wait: "SUCCEEDED", input: "\x1b"},
			{input: "q"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAlternateScreenRestored(t, output)
	if authority.planCalls != 1 || authority.applyCalls != 1 {
		t.Fatalf(
			"PTY authority calls plan=%d apply=%d",
			authority.planCalls,
			authority.applyCalls,
		)
	}
	updated, ok := final.(*tuimodel.Model)
	if !ok {
		t.Fatalf("final model=%T, want *tui.Model", final)
	}
	projection := updated.ConsoleState().Profiles[0]
	if projection.Desired.Network.Mode != profile.NetworkModeTun2Socks ||
		len(updated.ConsoleState().Operations) != 1 ||
		updated.ConsoleState().Operations[0].Phase !=
			manager.OperationSucceeded {
		t.Fatalf(
			"terminal result was not projected: profile=%+v operations=%+v",
			projection.Desired.Network,
			updated.ConsoleState().Operations,
		)
	}
}

func TestTUIConfigPTYSecretInputNeverEchoesAndCancelClears(t *testing.T) {
	const canary = "socks5://alice:pty-secret-canary@127.0.0.1:7890"
	authority, state := newTUIConfigPTYFixture(t)
	model := tuimodel.NewModel(tuimodel.ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true, Unicode: false,
		Context:        context.Background(),
		ConfigProvider: authority,
	})
	output, final, err := exerciseTUIProgramSteps(
		t,
		model,
		[]tuiPTYStep{
			{wait: "Hideout", input: "3"},
			{
				wait:  "Config · default",
				input: strings.Repeat("j", 8) + "\r",
			},
			{wait: "Edit Secret lifecycle", input: "set local-proxy\r"},
			{wait: "Review configuration change", input: "a"},
			{wait: "Apply this exact operation? [y/N]", input: "y"},
			{wait: "Secret value", input: canary},
			{wait: "••••••••", input: "\x1b"},
			{input: "q"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAlternateScreenRestored(t, output)
	if strings.Contains(output, canary) ||
		strings.Contains(output, "pty-secret-canary") {
		t.Fatalf("PTY output echoed secret input: %q", output)
	}
	if authority.secretPlanCalls != 1 ||
		authority.secretApplyCalls != 0 {
		t.Fatalf(
			"secret cancel crossed apply boundary: plan=%d apply=%d",
			authority.secretPlanCalls,
			authority.secretApplyCalls,
		)
	}
	updated, ok := final.(*tuimodel.Model)
	if !ok {
		t.Fatalf("final model=%T, want *tui.Model", final)
	}
	if updated.ModalDepth() != 0 || updated.ConfigStage() != "" {
		t.Fatalf(
			"cancel retained modal state: depth=%d stage=%s",
			updated.ModalDepth(),
			updated.ConfigStage(),
		)
	}
}

func TestTUIConfigPTYStalePlanIsReadOnlyWithoutApply(t *testing.T) {
	authority, state := newTUIConfigPTYFixture(t)
	authority.planErr = manager.ErrStaleConfigurationPlan
	model := tuimodel.NewModel(tuimodel.ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true, Unicode: false,
		Context:        context.Background(),
		ConfigProvider: authority,
	})
	output, _, err := exerciseTUIProgramSteps(
		t,
		model,
		[]tuiPTYStep{
			{wait: "Hideout", input: "3"},
			{wait: "Config · default", input: "\r"},
			{wait: "Edit Connection mode", input: "proxy\r"},
			{wait: "STALE · read-only", input: "\x1b"},
			{input: "q"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAlternateScreenRestored(t, output)
	if authority.planCalls != 1 || authority.applyCalls != 0 {
		t.Fatalf(
			"stale PTY crossed apply boundary: plan=%d apply=%d",
			authority.planCalls,
			authority.applyCalls,
		)
	}
}

func TestTUIConfigPTYResponseLossKeepsOperationLookup(t *testing.T) {
	authority, state := newTUIConfigPTYFixture(t)
	authority.applyErr = errors.New("fixture response lost")
	model := tuimodel.NewModel(tuimodel.ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true, Unicode: false,
		Context:        context.Background(),
		ConfigProvider: authority,
	})
	output, final, err := exerciseTUIProgramSteps(
		t,
		model,
		[]tuiPTYStep{
			{wait: "Hideout", input: "3"},
			{wait: "Config · default", input: "\r"},
			{wait: "Edit Connection mode", input: "proxy\r"},
			{wait: "Review configuration change", input: "a"},
			{wait: "Apply this exact operation? [y/N]", input: "y"},
			{wait: "OUTCOME UNKNOWN", input: "\x1b"},
			{input: "4"},
			{wait: "not present in this snapshot", input: "q"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAlternateScreenRestored(t, output)
	if authority.lastPlan.OperationID == "" ||
		!strings.Contains(output, authority.lastPlan.OperationID) ||
		!strings.Contains(output, "Inspect this exact ID in Operations") {
		t.Fatalf(
			"response-loss PTY discarded recovery identity:\n%s",
			output,
		)
	}
	if authority.applyCalls != 1 {
		t.Fatalf("response-loss apply calls=%d want 1", authority.applyCalls)
	}
	updated, ok := final.(*tuimodel.Model)
	if !ok ||
		updated.OperationLookupID() != authority.lastPlan.OperationID ||
		updated.ActiveView() != tuimodel.ViewOperations {
		t.Fatalf(
			"response-loss resume state=%T lookup=%q view=%v",
			final,
			func() string {
				if !ok {
					return ""
				}
				return updated.OperationLookupID()
			}(),
			func() any {
				if !ok {
					return nil
				}
				return updated.ActiveView()
			}(),
		)
	}
}

func TestTUILifecyclePTYShowsActiveBlockerWithoutApply(t *testing.T) {
	authority, state := newTUIConfigPTYFixture(t)
	state.Overview = manager.Overview{
		Version: "hideout.manager/v1",
		Sessions: []manager.SessionSummary{{
			ID: "ses_lifecyclepty", EnvironmentID: "env_lifecyclepty",
			Profile: "default", State: "running", OwnerStatus: "live",
			CommandClass: "claude",
		}},
		Environments: []manager.EnvironmentSummary{{
			ID: "env_lifecyclepty", Name: "lifecycle-pty",
			Profile: "default", Backend: "lima", Status: "running",
			InstanceName:   "hideout-lifecycle-pty",
			ActiveSessions: 1, OwnerHealth: "live",
			CreatedAt: time.Now().UTC().Add(-time.Hour),
		}},
	}
	model := tuimodel.NewModel(tuimodel.ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true, Unicode: false,
		Context:           context.Background(),
		LifecycleProvider: authority,
	})
	output, _, err := exerciseTUIProgramSteps(
		t,
		model,
		[]tuiPTYStep{
			{wait: "Hideout", input: "e"},
			{wait: "Environment lifecycle", input: "\r"},
			{wait: "ACTIVE BLOCKERS", input: "a"},
			{wait: "APPLY DISABLED", input: "\x1b"},
			{input: "q"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAlternateScreenRestored(t, output)
	if authority.lifecyclePlanCalls != 1 ||
		authority.lifecycleApplyCalls != 0 ||
		!strings.Contains(output, "ses_lifecyclepty") {
		t.Fatalf(
			"PTY blocker journey mismatch plan=%d apply=%d:\n%s",
			authority.lifecyclePlanCalls,
			authority.lifecycleApplyCalls,
			output,
		)
	}
}

func TestTUILifecyclePTYCleanTypedConfirmationAndTerminalResult(
	t *testing.T,
) {
	authority, state := newTUIConfigPTYFixture(t)
	state.Overview = manager.Overview{
		Version: "hideout.manager/v1",
		Environments: []manager.EnvironmentSummary{{
			ID: "env_cleanpty", Name: "clean-pty",
			Profile: "default", Backend: "lima", Status: "stopped",
			InstanceName: "hideout-clean-pty",
			CreatedAt:    time.Now().UTC().Add(-time.Hour),
		}},
	}
	model := tuimodel.NewModel(tuimodel.ModelOptions{
		State: state, Width: 100, Height: 30,
		NoColor: true, Unicode: false,
		Context:           context.Background(),
		LifecycleProvider: authority,
	})
	output, final, err := exerciseTUIProgramSteps(
		t,
		model,
		[]tuiPTYStep{
			{wait: "Hideout", input: "e"},
			{wait: "Environment lifecycle", input: "c"},
			{input: "\r"},
			{wait: "Review environment clean", input: "a"},
			{wait: `type "env_cleanpty"`, input: "env_cleanpty\r"},
			{wait: "CLEAN COMPLETE", input: "\x1b"},
			{input: "q"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	assertAlternateScreenRestored(t, output)
	if authority.lifecyclePlanCalls != 1 ||
		authority.lifecycleApplyCalls != 1 {
		t.Fatalf(
			"PTY clean authority calls plan=%d apply=%d",
			authority.lifecyclePlanCalls,
			authority.lifecycleApplyCalls,
		)
	}
	updated, ok := final.(*tuimodel.Model)
	if !ok ||
		len(updated.ConsoleState().Overview.Environments) != 0 {
		t.Fatalf(
			"PTY clean inventory was not updated: final=%T state=%+v",
			final,
			func() any {
				if !ok {
					return nil
				}
				return updated.ConsoleState().Overview.Environments
			}(),
		)
	}
}

type tuiPTYStep struct {
	wait  string
	input string
}

func exerciseTUIProgramSteps(
	t *testing.T,
	model *tuimodel.Model,
	steps []tuiPTYStep,
) (string, any, error) {
	t.Helper()
	terminal, replica, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	defer replica.Close()
	if err := pty.Setsize(
		replica,
		&pty.Winsize{Rows: 30, Cols: 100},
	); err != nil {
		t.Fatal(err)
	}

	var output synchronizedBuffer
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&output, terminal)
		close(readDone)
	}()

	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()
	result := make(chan tuiProgramResult, 1)
	go func() {
		final, runErr := runTUIProgram(ctx, replica, replica, model)
		result <- tuiProgramResult{model: final, err: runErr}
	}()

	waitForTUIOutput(t, &output, enterAlternateScreen)
	for _, step := range steps {
		if step.wait != "" {
			waitForTUIOutput(t, &output, step.wait)
		}
		if _, err := io.WriteString(terminal, step.input); err != nil {
			t.Fatal(err)
		}
		// A standalone Escape must pass the terminal parser's Alt-key
		// disambiguation window before the next key is written.
		time.Sleep(100 * time.Millisecond)
	}

	var completed tuiProgramResult
	select {
	case completed = <-result:
	case <-ctx.Done():
		t.Fatalf(
			"TUI configuration journey did not terminate:\n%s",
			output.String(),
		)
	}
	_ = replica.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("PTY output reader did not terminate")
	}
	return output.String(), completed.model, completed.err
}

type tuiConfigPTYAuthority struct {
	transactions        *manager.ProfileTransactionService
	planCalls           int
	applyCalls          int
	secretPlanCalls     int
	secretApplyCalls    int
	planErr             error
	applyErr            error
	lastPlan            manager.ConfigurationPlan
	lifecyclePlanCalls  int
	lifecycleApplyCalls int
	lifecyclePlan       manager.EnvironmentActionPlan
}

func newTUIConfigPTYFixture(
	t *testing.T,
) (*tuiConfigPTYAuthority, liveconsole.State) {
	t.Helper()
	store := profile.Store{Root: t.TempDir()}
	desired := profile.Default("default")
	desired.Network.ProxySecretRef = "local-proxy"
	desired.Network.MediatedResolver = "1.1.1.1"
	if err := store.Save(desired); err != nil {
		t.Fatal(err)
	}
	projection, err := (manager.ProfileProjectionService{
		Store: store,
	}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	projection.Effective = manager.ProfileEffective{
		Status: manager.EffectiveCurrent,
		Network: &manager.EffectiveNetwork{
			Mode: "direct", DNS: "system",
			ObservedAt: time.Now().UTC(),
		},
		Sessions: []manager.EffectiveSessionSnapshot{},
	}
	capabilities := make([]liveconsole.CapabilityProjection, 0)
	for _, capability := range manager.DefaultConfigurationCapabilities(true) {
		capabilities = append(capabilities, liveconsole.CapabilityProjection{
			ID:       capability.ID,
			Status:   workloadtypes.CoverageAvailable,
			Provider: capability.Provider,
			Mutable:  capability.Mutable,
			ActionRefs: append(
				[]string(nil),
				capability.ActionRefs...,
			),
		})
	}
	state := liveconsole.State{
		Version:              liveconsole.SeedVersionV2,
		DaemonInstanceID:     "daemon_fixture",
		CredentialGeneration: 1,
		LastSeq:              1,
		ProfileScope:         "default",
		Profiles:             []manager.ProfileProjection{projection},
		Capabilities:         capabilities,
		StreamHealth: liveconsole.StreamHealth{
			State: liveconsole.HealthLive,
		},
	}
	return &tuiConfigPTYAuthority{
		transactions: manager.NewProfileTransactionService(
			manager.New(store),
		),
	}, state
}

func (authority *tuiConfigPTYAuthority) PlanConfiguration(
	ctx context.Context,
	draft manager.ConfigurationDraft,
) (manager.ConfigurationPlan, error) {
	authority.planCalls++
	if authority.planErr != nil {
		return manager.ConfigurationPlan{}, authority.planErr
	}
	plan, err := authority.transactions.Plan(ctx, draft)
	authority.lastPlan = plan
	return plan, err
}

func (authority *tuiConfigPTYAuthority) ApplyConfiguration(
	ctx context.Context,
	request manager.ConfigurationApplyRequest,
) (manager.ConfigurationApplyResult, error) {
	authority.applyCalls++
	if authority.applyErr != nil {
		return manager.ConfigurationApplyResult{}, authority.applyErr
	}
	return authority.transactions.Apply(ctx, request)
}

func (authority *tuiConfigPTYAuthority) PlanSecret(
	_ context.Context,
	draft manager.SecretDraft,
) (manager.SecretPlan, error) {
	authority.secretPlanCalls++
	if err := draft.Validate(); err != nil {
		return manager.SecretPlan{}, err
	}
	now := time.Now().UTC()
	plan := manager.SecretPlan{
		Schema:      manager.SecretPlanSchema,
		OperationID: "op_tuisecretfixture01",
		Ref:         draft.Ref,
		Action:      draft.Action,
		Current: secrets.Reference{
			Schema:       secrets.SecretReferenceSchema,
			Ref:          draft.Ref,
			Provider:     "macos-keychain",
			Availability: secrets.AvailabilityMissing,
			Reason:       "not-found",
		},
		NextAvailability:     secrets.AvailabilityAvailable,
		NextGeneration:       1,
		AffectedProfiles:     []string{},
		AffectedEnvironments: []string{},
		Effects: []manager.PlannedEffect{{
			ID:            "write-keychain",
			Kind:          "persist",
			Scope:         "profile",
			Provider:      "keychain",
			Live:          true,
			Summary:       "write a new managed secret generation",
			ProofRequired: []string{"secret.generation"},
		}},
		Blockers: []manager.Blocker{},
		Warnings: []manager.Warning{},
		Rollback: manager.RollbackPlan{
			Mode:    "restore-previous",
			Summary: "restore the previous secret generation",
			Effects: []string{"write-keychain"},
		},
		ExpiresAt: now.Add(15 * time.Minute),
	}
	if err := plan.Seal(); err != nil {
		return manager.SecretPlan{}, err
	}
	return plan, nil
}

func (authority *tuiConfigPTYAuthority) ApplySecret(
	_ context.Context,
	request manager.SecretApplyRequest,
) (manager.SecretApplyResult, error) {
	authority.secretApplyCalls++
	if request.Value != nil {
		request.Value.Clear()
	}
	return manager.SecretApplyResult{},
		errors.New("secret apply is not expected in cancel fixture")
}

func (authority *tuiConfigPTYAuthority) PlanEnvironment(
	_ context.Context,
	action string,
	request manager.EnvironmentActionAPIRequest,
) (manager.EnvironmentActionPlan, error) {
	authority.lifecyclePlanCalls++
	if len(request.IDs) != 1 {
		return manager.EnvironmentActionPlan{},
			errors.New("expected exact lifecycle target")
	}
	status := "running"
	if action == manager.EnvironmentActionClean {
		status = "stopped"
	}
	authority.lifecyclePlan = manager.EnvironmentActionPlan{
		Action: action, RequestedIDs: []string{request.IDs[0]},
		OperationID: "op_lifecyclepty",
		PlanDigest:  "sha256:" + strings.Repeat("d", 64),
		Targets: []manager.EnvironmentActionTarget{{
			ID: request.IDs[0], Profile: "default", Backend: "lima",
			Status: status, InstanceName: "hideout-pty-fixture",
		}},
		Skipped: []manager.EnvironmentActionTarget{},
		Total:   1,
	}
	return authority.lifecyclePlan, nil
}

func (authority *tuiConfigPTYAuthority) ApplyEnvironment(
	_ context.Context,
	action string,
	request manager.EnvironmentActionAPIRequest,
) (manager.EnvironmentActionResult, error) {
	authority.lifecycleApplyCalls++
	if action != authority.lifecyclePlan.Action ||
		len(request.IDs) != 1 ||
		request.IDs[0] != authority.lifecyclePlan.RequestedIDs[0] ||
		request.OperationID != authority.lifecyclePlan.OperationID ||
		request.PlanDigest != authority.lifecyclePlan.PlanDigest ||
		!request.Confirmed {
		return manager.EnvironmentActionResult{},
			errors.New("lifecycle apply mismatch")
	}
	target := authority.lifecyclePlan.Targets[0]
	if action == manager.EnvironmentActionStop {
		target.Status = "stopped"
	}
	return manager.EnvironmentActionResult{
		Plan:    authority.lifecyclePlan,
		Applied: []manager.EnvironmentActionTarget{target},
		Skipped: []manager.EnvironmentActionTarget{},
		Operation: tuiLifecycleOperationFixture(
			authority.lifecyclePlan,
			request.IDs[0],
		),
	}, nil
}
