package modal

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestActivityRetentionDraftAcceptsHumanUnitsAndLifecycle(t *testing.T) {
	for _, test := range []struct {
		input   string
		bytes   int64
		seconds int64
	}{
		{input: "256MiB lifecycle", bytes: 256 << 20},
		{input: "128MiB 7d", bytes: 128 << 20, seconds: 7 * 24 * 60 * 60},
		{input: "1073741824 12h", bytes: 1 << 30, seconds: 12 * 60 * 60},
	} {
		change, err := parseTypedChange(
			manager.ChangeActivityRetention,
			test.input,
		)
		if err != nil {
			t.Fatalf("parse %q: %v", test.input, err)
		}
		var value struct {
			MaxBytes      int64 `json:"maxBytes"`
			MaxAgeSeconds int64 `json:"maxAgeSeconds"`
		}
		if err := json.Unmarshal(change.Value, &value); err != nil {
			t.Fatalf("decode %q: %v", test.input, err)
		}
		if value.MaxBytes != test.bytes ||
			value.MaxAgeSeconds != test.seconds {
			t.Fatalf("parse %q=%+v", test.input, value)
		}
	}
}

func TestConfigModalCancelKeepsDraftClientLocal(t *testing.T) {
	provider := &configProviderFixture{}
	editor := NewConfig(ConfigOptions{
		Context:    context.Background(),
		Provider:   provider,
		Projection: configProjectionFixture(),
		EditorID:   manager.ChangeNetworkPosture,
		Mutable:    true,
	})

	typeKeys(t, editor, "proxy")
	if editor.Stage() != StageEditing {
		t.Fatalf("stage=%s want editing", editor.Stage())
	}
	_, outcome := editor.Update(specialKey(tea.KeyEsc))
	if !outcome.Close {
		t.Fatal("escape did not close the client-local draft")
	}
	if provider.planCalls != 0 || provider.applyCalls != 0 {
		t.Fatalf(
			"cancel crossed the authority boundary: plan=%d apply=%d",
			provider.planCalls,
			provider.applyCalls,
		)
	}
}

func TestConfigModalReviewNeedsDistinctApplyAndConfirmation(t *testing.T) {
	provider := &configProviderFixture{}
	editor := NewConfig(ConfigOptions{
		Context:    context.Background(),
		Provider:   provider,
		Projection: configProjectionFixture(),
		EditorID:   manager.ChangeNetworkPosture,
		Mutable:    true,
	})

	typeKeys(t, editor, "proxy")
	runConfigCommand(t, editor, press(t, editor, specialKey(tea.KeyEnter)))
	if editor.Stage() != StageReview || provider.planCalls != 1 {
		t.Fatalf(
			"plan stage=%s calls=%d",
			editor.Stage(),
			provider.planCalls,
		)
	}
	if output := editor.View(100); !strings.Contains(output, "Before") ||
		!strings.Contains(output, "After") ||
		!strings.Contains(output, provider.profilePlan.OperationID) ||
		!strings.Contains(output, "Rollback") {
		t.Fatalf("review omitted canonical plan facts:\n%s", output)
	}

	// Enter inspects the review. It is deliberately not the Apply action.
	if command := press(t, editor, specialKey(tea.KeyEnter)); command != nil {
		t.Fatal("Enter on review unexpectedly returned an apply command")
	}
	if editor.Stage() != StageReview || provider.applyCalls != 0 {
		t.Fatalf(
			"Enter applied a reviewed change: stage=%s calls=%d",
			editor.Stage(),
			provider.applyCalls,
		)
	}

	if command := press(t, editor, key("a")); command != nil {
		t.Fatal("Apply selection should open confirmation without side effects")
	}
	if editor.Stage() != StageConfirming {
		t.Fatalf("stage=%s want confirming", editor.Stage())
	}
	if command := press(t, editor, key("n")); command != nil {
		t.Fatal("negative confirmation returned an apply command")
	}
	if editor.Stage() != StageReview || provider.applyCalls != 0 {
		t.Fatalf(
			"negative confirmation changed authority: stage=%s calls=%d",
			editor.Stage(),
			provider.applyCalls,
		)
	}

	press(t, editor, key("a"))
	runConfigCommand(t, editor, press(t, editor, key("y")))
	if editor.Stage() != StageTerminal || provider.applyCalls != 1 {
		t.Fatalf(
			"terminal stage=%s apply calls=%d",
			editor.Stage(),
			provider.applyCalls,
		)
	}
	if editor.OperationID() != provider.profilePlan.OperationID ||
		!strings.Contains(editor.View(100), "SUCCEEDED") {
		t.Fatalf(
			"terminal did not retain authoritative result:\n%s",
			editor.View(100),
		)
	}
}

func TestConfigModalHighRiskRequiresTypedProfileName(t *testing.T) {
	provider := &configProviderFixture{highRisk: true}
	editor := NewConfig(ConfigOptions{
		Context:    context.Background(),
		Provider:   provider,
		Projection: configProjectionFixture(),
		EditorID:   manager.ChangeProfileHostFS,
		Mutable:    true,
	})
	typeKeys(t, editor, "add read:/tmp/fixture | review fixture")
	runConfigCommand(t, editor, press(t, editor, specialKey(tea.KeyEnter)))
	press(t, editor, key("a"))

	if editor.Stage() != StageConfirming ||
		!strings.Contains(editor.View(100), `type "default"`) {
		t.Fatalf("high-risk confirmation is not explicit:\n%s", editor.View(100))
	}
	typeKeys(t, editor, "wrong")
	if command := press(t, editor, specialKey(tea.KeyEnter)); command != nil {
		t.Fatal("wrong typed confirmation returned an apply command")
	}
	if editor.Stage() != StageConfirming || provider.applyCalls != 0 {
		t.Fatalf(
			"wrong typed confirmation applied: stage=%s calls=%d",
			editor.Stage(),
			provider.applyCalls,
		)
	}
	clearInput(t, editor)
	typeKeys(t, editor, "default")
	runConfigCommand(t, editor, press(t, editor, specialKey(tea.KeyEnter)))
	if editor.Stage() != StageTerminal || provider.applyCalls != 1 {
		t.Fatalf(
			"typed confirmation did not apply: stage=%s calls=%d",
			editor.Stage(),
			provider.applyCalls,
		)
	}
}

func TestConfigModalStaleProjectionDisablesPlanAndApply(t *testing.T) {
	provider := &configProviderFixture{}
	projection := configProjectionFixture()
	editor := NewConfig(ConfigOptions{
		Context: context.Background(), Provider: provider,
		Projection: projection, EditorID: manager.ChangeNetworkPosture,
		Mutable: true,
	})
	typeKeys(t, editor, "proxy")
	editor.SyncAuthority(false, projection, "authenticated stream is stale")
	if editor.Stage() != StageStale {
		t.Fatalf("editing stale stage=%s", editor.Stage())
	}
	if command := press(t, editor, specialKey(tea.KeyEnter)); command != nil ||
		provider.planCalls != 0 {
		t.Fatal("stale editor planned a mutation")
	}

	editor = NewConfig(ConfigOptions{
		Context: context.Background(), Provider: provider,
		Projection: projection, EditorID: manager.ChangeNetworkPosture,
		Mutable: true,
	})
	typeKeys(t, editor, "proxy")
	runConfigCommand(t, editor, press(t, editor, specialKey(tea.KeyEnter)))
	changed := projection
	changed.Revision++
	changed.ContentDigest = "sha256:" + strings.Repeat("b", 64)
	editor.SyncAuthority(true, changed, "")
	if editor.Stage() != StageStale {
		t.Fatalf("review stale stage=%s", editor.Stage())
	}
	if command := press(t, editor, key("a")); command != nil ||
		provider.applyCalls != 0 {
		t.Fatal("stale review applied a mutation")
	}
}

func TestConfigModalSecretInputIsMaskedAndCleared(t *testing.T) {
	const canary = "socks5://alice:super-secret@127.0.0.1:7890"
	provider := &configProviderFixture{}
	editor := NewConfig(ConfigOptions{
		Context:    context.Background(),
		Provider:   provider,
		Projection: configProjectionFixture(),
		EditorID:   EditorSecret,
		Mutable:    true,
	})
	typeKeys(t, editor, "set local-proxy")
	runConfigCommand(t, editor, press(t, editor, specialKey(tea.KeyEnter)))
	press(t, editor, key("a"))
	if editor.Stage() != StageConfirming {
		t.Fatalf("secret review stage=%s", editor.Stage())
	}
	press(t, editor, key("y"))
	if editor.Stage() != StageSecretInput {
		t.Fatalf("secret confirmation stage=%s", editor.Stage())
	}
	typeKeys(t, editor, canary)
	output := editor.View(100)
	if strings.Contains(output, canary) ||
		strings.Contains(output, "super-secret") ||
		!strings.Contains(output, "••••") {
		t.Fatalf("secret input was not safely masked:\n%s", output)
	}
	if !editor.SecretRetained() {
		t.Fatal("secret fixture was not retained before apply")
	}
	_, outcome := editor.Update(specialKey(tea.KeyEsc))
	if !outcome.Close || editor.SecretRetained() {
		t.Fatal("cancel did not clear secret input")
	}

	editor = NewConfig(ConfigOptions{
		Context:    context.Background(),
		Provider:   provider,
		Projection: configProjectionFixture(),
		EditorID:   EditorSecret,
		Mutable:    true,
	})
	typeKeys(t, editor, "set local-proxy")
	runConfigCommand(t, editor, press(t, editor, specialKey(tea.KeyEnter)))
	press(t, editor, key("a"))
	press(t, editor, key("y"))
	typeKeys(t, editor, canary)
	runConfigCommand(t, editor, press(t, editor, specialKey(tea.KeyEnter)))
	if editor.Stage() != StageTerminal ||
		editor.SecretRetained() ||
		provider.secretApplyCalls != 1 ||
		provider.receivedSecret != canary {
		t.Fatalf(
			"secret apply/clear mismatch: stage=%s retained=%t calls=%d received=%q",
			editor.Stage(),
			editor.SecretRetained(),
			provider.secretApplyCalls,
			provider.receivedSecret,
		)
	}
	if strings.Contains(editor.View(100), canary) {
		t.Fatal("terminal view echoed the secret value")
	}
}

func TestConfigModalResponseLossKeepsStableOperationForLookup(t *testing.T) {
	provider := &configProviderFixture{
		applyErr: errors.New("transport closed after request"),
	}
	editor := NewConfig(ConfigOptions{
		Context:    context.Background(),
		Provider:   provider,
		Projection: configProjectionFixture(),
		EditorID:   manager.ChangeNetworkPosture,
		Mutable:    true,
	})
	typeKeys(t, editor, "proxy")
	runConfigCommand(t, editor, press(t, editor, specialKey(tea.KeyEnter)))
	press(t, editor, key("a"))
	runConfigCommand(t, editor, press(t, editor, key("y")))

	if editor.Stage() != StageTerminal ||
		!editor.ResponseLost() ||
		editor.OperationID() != provider.profilePlan.OperationID {
		t.Fatalf(
			"response loss discarded identity: stage=%s lost=%t id=%q",
			editor.Stage(),
			editor.ResponseLost(),
			editor.OperationID(),
		)
	}
	output := editor.View(100)
	if !strings.Contains(output, "OUTCOME UNKNOWN") ||
		!strings.Contains(output, provider.profilePlan.OperationID) ||
		strings.Contains(strings.ToLower(output), "retry apply") {
		t.Fatalf("response-loss recovery is unsafe:\n%s", output)
	}

	operation := profileOperationFixture(provider.profilePlan)
	editor.ObserveOperation(operation)
	if editor.ResponseLost() ||
		!strings.Contains(editor.View(100), "SUCCEEDED") {
		t.Fatalf("operation lookup did not resolve response loss:\n%s", editor.View(100))
	}
}

type configProviderFixture struct {
	planCalls        int
	applyCalls       int
	secretPlanCalls  int
	secretApplyCalls int
	receivedSecret   string
	applyErr         error
	highRisk         bool
	profilePlan      manager.ConfigurationPlan
	secretPlan       manager.SecretPlan
}

func (provider *configProviderFixture) PlanConfiguration(
	_ context.Context,
	draft manager.ConfigurationDraft,
) (manager.ConfigurationPlan, error) {
	provider.planCalls++
	plan := configurationPlanFixture(draft, provider.highRisk)
	provider.profilePlan = plan
	return plan, nil
}

func (provider *configProviderFixture) ApplyConfiguration(
	_ context.Context,
	request manager.ConfigurationApplyRequest,
) (manager.ConfigurationApplyResult, error) {
	provider.applyCalls++
	if provider.applyErr != nil {
		return manager.ConfigurationApplyResult{}, provider.applyErr
	}
	plan := provider.profilePlan
	if request.OperationID != plan.OperationID ||
		request.PlanDigest != plan.PlanDigest ||
		request.BaseRevision != plan.BaseRevision ||
		!request.Confirmed {
		return manager.ConfigurationApplyResult{}, errors.New("mismatched apply")
	}
	projection := configProjectionFixture()
	projection.Revision++
	return manager.ConfigurationApplyResult{
		Operation:  profileOperationFixture(plan),
		Projection: projection,
	}, nil
}

func (provider *configProviderFixture) PlanSecret(
	_ context.Context,
	draft manager.SecretDraft,
) (manager.SecretPlan, error) {
	provider.secretPlanCalls++
	plan := secretPlanFixture(draft)
	provider.secretPlan = plan
	return plan, nil
}

func (provider *configProviderFixture) ApplySecret(
	_ context.Context,
	request manager.SecretApplyRequest,
) (manager.SecretApplyResult, error) {
	provider.secretApplyCalls++
	if request.Value != nil {
		if err := request.Value.Use(func(value []byte) error {
			provider.receivedSecret = string(value)
			return nil
		}); err != nil {
			return manager.SecretApplyResult{}, err
		}
	}
	plan := provider.secretPlan
	return manager.SecretApplyResult{
		Operation: secretOperationFixture(plan),
		Reference: secrets.Reference{
			Schema: secrets.SecretReferenceSchema,
			Ref:    plan.Ref, Provider: "macos-keychain",
			Availability: secrets.AvailabilityAvailable,
			Generation:   plan.NextGeneration,
			UpdatedAt:    time.Date(2026, 7, 29, 12, 1, 0, 0, time.UTC),
		},
	}, nil
}

func configProjectionFixture() manager.ProfileProjection {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return manager.ProfileProjection{
		Schema:  manager.ProfileProjectionSchema,
		Profile: "default", Revision: 7,
		ContentDigest: "sha256:" + strings.Repeat("a", 64),
		Desired:       profile.Default("default"),
		Effective: manager.ProfileEffective{
			Status:   manager.EffectiveNotObserved,
			Sessions: []manager.EffectiveSessionSnapshot{},
		},
		UpdatedAt: now,
	}
}

func configurationPlanFixture(
	draft manager.ConfigurationDraft,
	highRisk bool,
) manager.ConfigurationPlan {
	now := time.Now().UTC()
	reviewed, err := manager.DefaultTypedChangeRegistry().ReviewChanges(
		draft.Changes,
	)
	if err != nil {
		panic(err)
	}
	plan := manager.ConfigurationPlan{
		Schema:      manager.ConfigurationPlanSchema,
		OperationID: "op_configfixture0001",
		Profile:     draft.Profile, BaseRevision: draft.BaseRevision,
		BaseDigest:       "sha256:" + strings.Repeat("a", 64),
		CanonicalChanges: reviewed,
		Diff: []manager.ReviewDiff{{
			Kind:  draft.Changes[0].Kind,
			Field: "network.mode", Before: "direct", After: "proxy",
			Scope: "active-connections",
		}},
		Effects: []manager.PlannedEffect{{
			ID: "persist-profile", Kind: "persist", Scope: "profile",
			Provider: "manager", Live: true,
			Summary:       "persist the reviewed desired profile",
			ProofRequired: []string{"profile.revision"},
		}},
		Blockers: []manager.Blocker{},
		Warnings: []manager.Warning{},
		Rollback: manager.RollbackPlan{
			Mode:    "restore-previous",
			Summary: "restore the prior desired profile",
			Effects: []string{"persist-profile"},
		},
		ExpiresAt: now.Add(15 * time.Minute),
	}
	if highRisk {
		plan.Diff[0] = manager.ReviewDiff{
			Kind:   manager.ChangeProfileHostFS,
			Field:  "hostfs.allow.hfs_fixture",
			Before: "absent", After: "allow read:/tmp/fixture",
			Scope: "new-sessions",
		}
		plan.Warnings = []manager.Warning{{
			Code:    "hostfs-authority-expanded",
			Summary: "Host file authority expands for future sessions.",
		}}
	}
	if err := plan.Seal(); err != nil {
		panic(err)
	}
	return plan
}

func secretPlanFixture(draft manager.SecretDraft) manager.SecretPlan {
	now := time.Now().UTC()
	plan := manager.SecretPlan{
		Schema:      manager.SecretPlanSchema,
		OperationID: "op_secretfixture0001",
		Ref:         draft.Ref, Action: draft.Action,
		BaseGeneration: 0,
		Current: secrets.Reference{
			Schema: secrets.SecretReferenceSchema,
			Ref:    draft.Ref, Provider: "macos-keychain",
			Availability: secrets.AvailabilityMissing,
			Reason:       "not-found",
		},
		NextAvailability:     secrets.AvailabilityAvailable,
		NextGeneration:       1,
		AffectedProfiles:     []string{},
		AffectedEnvironments: []string{},
		Effects: []manager.PlannedEffect{{
			ID: "write-keychain", Kind: "persist", Scope: "profile",
			Provider: "keychain", Live: true,
			Summary:       "write a new secret generation",
			ProofRequired: []string{"secret.generation"},
		}},
		Blockers: []manager.Blocker{},
		Warnings: []manager.Warning{},
		Rollback: manager.RollbackPlan{
			Mode:    "restore-previous",
			Summary: "restore the prior secret generation",
			Effects: []string{"write-keychain"},
		},
		ExpiresAt: now.Add(15 * time.Minute),
	}
	if err := plan.Seal(); err != nil {
		panic(err)
	}
	return plan
}

func profileOperationFixture(
	plan manager.ConfigurationPlan,
) manager.Operation {
	now := time.Date(2026, 7, 29, 12, 1, 0, 0, time.UTC)
	return manager.Operation{
		Schema: manager.OperationSchema,
		ID:     plan.OperationID, Kind: "profile.transaction",
		Owner:      manager.OperationOwner{Kind: "profile", ID: plan.Profile},
		PlanDigest: plan.PlanDigest, BaseRevision: plan.BaseRevision,
		Phase:   manager.OperationSucceeded,
		Effects: []manager.EffectResult{},
		Result: &manager.OperationResult{
			Status:  manager.OperationSucceeded,
			Code:    "configuration-applied",
			Summary: "configuration committed with terminal evidence",
		},
		Recovery: manager.Recovery{
			Code: "none", Summary: "no recovery is required",
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func secretOperationFixture(plan manager.SecretPlan) manager.Operation {
	now := time.Date(2026, 7, 29, 12, 1, 0, 0, time.UTC)
	return manager.Operation{
		Schema: manager.OperationSchema,
		ID:     plan.OperationID, Kind: "secret.transaction",
		Owner:      manager.OperationOwner{Kind: "secret", ID: plan.Ref},
		PlanDigest: plan.PlanDigest,
		Phase:      manager.OperationSucceeded,
		Effects:    []manager.EffectResult{},
		Result: &manager.OperationResult{
			Status:  manager.OperationSucceeded,
			Code:    "secret-applied",
			Summary: "secret generation committed with terminal evidence",
		},
		Recovery: manager.Recovery{
			Code: "none", Summary: "no recovery is required",
		},
		CreatedAt: now, UpdatedAt: now,
	}
}

func press(t *testing.T, editor *Config, message tea.KeyPressMsg) tea.Cmd {
	t.Helper()
	command, outcome := editor.Update(message)
	if outcome.Close {
		t.Fatal("modal unexpectedly closed")
	}
	return command
}

func runConfigCommand(t *testing.T, editor *Config, command tea.Cmd) {
	t.Helper()
	if command == nil {
		t.Fatal("expected asynchronous command")
	}
	message := command()
	next, outcome := editor.Update(message)
	if outcome.Close {
		t.Fatal("async response unexpectedly closed modal")
	}
	if next != nil {
		t.Fatal("async response unexpectedly returned another command")
	}
}

func typeKeys(t *testing.T, editor *Config, value string) {
	t.Helper()
	for _, character := range value {
		press(t, editor, key(string(character)))
	}
}

func clearInput(t *testing.T, editor *Config) {
	t.Helper()
	for editor.InputLength() > 0 {
		press(t, editor, specialKey(tea.KeyBackspace))
	}
}

func key(value string) tea.KeyPressMsg {
	runes := []rune(value)
	return tea.KeyPressMsg(tea.Key{Text: value, Code: runes[0]})
}

func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Code: code})
}
