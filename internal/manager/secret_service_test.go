package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	netpolicy "github.com/vibe-agi/hideout/internal/network"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

func TestSecretServicePlansWithoutValueAndAppliesExactlyOnce(t *testing.T) {
	service, store := newSecretServiceFixture(t)
	plan, err := service.Plan(context.Background(), SecretDraft{
		Schema: SecretDraftSchema,
		Ref:    "local-proxy",
		Action: secrets.ActionSet,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.VerifyDigest(); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"canary-user",
		"canary-password",
		`"value"`,
	} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("secret plan contains %q: %s", forbidden, data)
		}
	}
	canary := "socks5://canary-user:canary-password@127.0.0.1:7890"
	unconfirmed := secretBufferFixture(t, canary)
	if _, err := service.Apply(context.Background(), SecretApplyRequest{
		Schema: SecretApplySchema, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Ref: plan.Ref, Action: plan.Action,
		Confirmed: false, Value: unconfirmed,
	}); !errors.Is(err, ErrSecretConfirmationRequired) {
		t.Fatalf("unconfirmed apply error=%v", err)
	}
	assertManagerSecretBufferCleared(t, unconfirmed)
	if store.writeCount() != 0 {
		t.Fatalf("unconfirmed apply writes=%d", store.writeCount())
	}

	value := secretBufferFixture(t, canary)
	result, err := service.Apply(context.Background(), SecretApplyRequest{
		Schema: SecretApplySchema, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Ref: plan.Ref, Action: plan.Action,
		Confirmed: true, Value: value,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertManagerSecretBufferCleared(t, value)
	if result.Operation.Phase != OperationSucceeded ||
		result.Reference.Availability != secrets.AvailabilityAvailable ||
		result.Reference.Generation != 1 ||
		store.writeCount() != 1 {
		t.Fatalf("apply=%+v writes=%d", result, store.writeCount())
	}

	replayed, err := service.Apply(context.Background(), SecretApplyRequest{
		Schema: SecretApplySchema, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Ref: plan.Ref, Action: plan.Action,
		Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Operation.Phase != OperationSucceeded ||
		replayed.Reference.Generation != 1 ||
		store.writeCount() != 1 {
		t.Fatalf("terminal replay=%+v writes=%d", replayed, store.writeCount())
	}
}

func TestSecretServiceRejectsStalePlanBeforeProviderEffect(t *testing.T) {
	service, store := newSecretServiceFixture(t)
	plan, err := service.Plan(context.Background(), SecretDraft{
		Schema: SecretDraftSchema, Ref: "local-proxy",
		Action: secrets.ActionSet,
	})
	if err != nil {
		t.Fatal(err)
	}
	external := secretBufferFixture(t, "socks5://external.invalid:1")
	if _, err := store.Set(context.Background(), secrets.WriteRequest{
		Ref: "local-proxy", OperationID: "op_externalwrite01",
		ExpectedGeneration: 0, Value: external,
	}); err != nil {
		t.Fatal(err)
	}
	writesBefore := store.writeCount()
	value := secretBufferFixture(t, "socks5://reviewed.invalid:2")
	if _, err := service.Apply(context.Background(), SecretApplyRequest{
		Schema: SecretApplySchema, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Ref: plan.Ref, Action: plan.Action,
		Confirmed: true, Value: value,
	}); !errors.Is(err, ErrStaleSecretPlan) {
		t.Fatalf("stale apply error=%v", err)
	}
	assertManagerSecretBufferCleared(t, value)
	if store.writeCount() != writesBefore {
		t.Fatalf("stale apply writes=%d want=%d", store.writeCount(), writesBefore)
	}
	operation, err := service.operationStore().Load(plan.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != OperationCancelled ||
		operation.Result == nil ||
		operation.Result.Code != "stale-secret-plan" {
		t.Fatalf("stale operation=%+v", operation)
	}
}

func TestSecretServiceReconcilesProviderCommitAfterResponseLossWithoutValue(t *testing.T) {
	service, store := newSecretServiceFixture(t)
	plan, err := service.Plan(context.Background(), SecretDraft{
		Schema: SecretDraftSchema, Ref: "local-proxy",
		Action: secrets.ActionSet,
	})
	if err != nil {
		t.Fatal(err)
	}
	responseLost := errors.New("simulated provider response loss")
	store.failNextWriteAfterCommit(responseLost)
	value := secretBufferFixture(
		t,
		"socks5://reconcile-user:reconcile-password@127.0.0.1:7890",
	)
	if _, err := service.Apply(context.Background(), SecretApplyRequest{
		Schema: SecretApplySchema, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Ref: plan.Ref, Action: plan.Action,
		Confirmed: true, Value: value,
	}); !errors.Is(err, responseLost) {
		t.Fatalf("first apply error=%v", err)
	}
	checkpoint, err := service.operationStore().Load(plan.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if checkpoint.Phase != OperationStaging ||
		len(checkpoint.Effects) != 1 ||
		checkpoint.Effects[0].Status != EffectRunning ||
		store.writeCount() != 1 {
		t.Fatalf("provider checkpoint=%+v writes=%d", checkpoint, store.writeCount())
	}

	restarted := NewSecretService(
		service.Core,
		store,
	)
	restarted.now = service.now
	resumed, err := restarted.ReconcileOperation(
		context.Background(),
		plan.OperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Operation.Phase != OperationSucceeded ||
		resumed.Reference.Generation != 1 ||
		store.writeCount() != 1 {
		t.Fatalf("resumed=%+v writes=%d", resumed, store.writeCount())
	}
}

func TestSecretStartupRecoveryDoesNotClaimPendingValueEffect(t *testing.T) {
	service, store := newSecretServiceFixture(t)
	plan, err := service.Plan(context.Background(), SecretDraft{
		Schema: SecretDraftSchema, Ref: "local-proxy",
		Action: secrets.ActionSet,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.operations().Transition(
		plan.OperationID,
		OperationClaimed,
	); err != nil {
		t.Fatal(err)
	}

	restarted := NewSecretService(service.Core, store)
	restarted.now = service.now
	result, err := restarted.ReconcileOperation(
		context.Background(),
		plan.OperationID,
	)
	if !errors.Is(err, ErrSecretValueRequired) {
		t.Fatalf("startup reconciliation error=%v", err)
	}
	if result.Operation.Phase != OperationClaimed ||
		result.Operation.Effects[0].Status != EffectPending ||
		result.Operation.Recovery.Code != "secret-value-required" ||
		store.writeCount() != 0 {
		t.Fatalf(
			"startup reconciliation=%+v writes=%d",
			result,
			store.writeCount(),
		)
	}
	withoutValue, err := restarted.Apply(
		context.Background(),
		SecretApplyRequest{
			Schema: SecretApplySchema, OperationID: plan.OperationID,
			PlanDigest: plan.PlanDigest, Ref: plan.Ref,
			Action: plan.Action, Confirmed: true,
		},
	)
	if !errors.Is(err, ErrSecretValueRequired) ||
		withoutValue.Operation.Effects[0].Status != EffectPending ||
		store.writeCount() != 0 {
		t.Fatalf(
			"valueless retry claimed provider effect: result=%+v writes=%d err=%v",
			withoutValue,
			store.writeCount(),
			err,
		)
	}

	value := secretBufferFixture(
		t,
		"socks5://retry-user:retry-password@127.0.0.1:7890",
	)
	applied, err := restarted.Apply(
		context.Background(),
		SecretApplyRequest{
			Schema: SecretApplySchema, OperationID: plan.OperationID,
			PlanDigest: plan.PlanDigest, Ref: plan.Ref,
			Action: plan.Action, Confirmed: true, Value: value,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Operation.Phase != OperationSucceeded ||
		store.writeCount() != 1 {
		t.Fatalf("retried operation=%+v writes=%d", applied, store.writeCount())
	}
}

func TestSecretRecoveryRequiredResumesAfterProviderBecomesAvailable(t *testing.T) {
	service, store := newSecretServiceFixture(t)
	plan := stageRunningSecretEffect(t, service)
	providerUnavailable := errors.New("provider temporarily locked")
	store.setReconcileError(providerUnavailable)

	if _, err := service.ReconcileOperation(
		context.Background(),
		plan.OperationID,
	); !errors.Is(err, providerUnavailable) {
		t.Fatalf("unavailable reconcile error=%v", err)
	}
	operation, err := service.operations().RequireRecovery(
		plan.OperationID,
		"provider-completion-unproved",
		"The provider may have committed this effect, but reconciliation could not prove completion.",
		"Unlock the provider and retry the same operation ID.",
	)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != OperationRecoveryRequired {
		t.Fatalf("operation did not require recovery: %+v", operation)
	}

	store.setReconcileError(nil)
	value := secretBufferFixture(
		t,
		"socks5://late-provider:late-secret@127.0.0.1:7890",
	)
	if _, err := store.Set(context.Background(), secrets.WriteRequest{
		Ref: plan.Ref, OperationID: plan.OperationID,
		ExpectedGeneration: plan.BaseGeneration, Value: value,
	}); err != nil {
		t.Fatal(err)
	}
	resumed, err := service.ReconcileOperation(
		context.Background(),
		plan.OperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Operation.Phase != OperationSucceeded ||
		resumed.Operation.Effects[0].Status != EffectSucceeded ||
		len(resumed.Operation.Effects[0].Evidence) == 0 ||
		resumed.Reference.Generation != plan.NextGeneration ||
		store.writeCount() != 1 {
		t.Fatalf(
			"provider recovery did not converge exactly once: result=%+v writes=%d",
			resumed,
			store.writeCount(),
		)
	}
}

func TestSecretRecoveryRequiredKeepsGenerationMismatchUnproved(t *testing.T) {
	service, store := newSecretServiceFixture(t)
	plan := stageRunningSecretEffect(t, service)
	if _, err := service.operations().RequireRecovery(
		plan.OperationID,
		"provider-completion-unproved",
		"The provider may have committed this effect, but reconciliation could not prove completion.",
		"Inspect the provider and retry the same operation ID.",
	); err != nil {
		t.Fatal(err)
	}
	external := secretBufferFixture(
		t,
		"socks5://external:external-secret@127.0.0.1:7890",
	)
	if _, err := store.Set(context.Background(), secrets.WriteRequest{
		Ref: plan.Ref, OperationID: "op_externalrotate1",
		ExpectedGeneration: plan.BaseGeneration, Value: external,
	}); err != nil {
		t.Fatal(err)
	}

	result, err := service.ReconcileOperation(
		context.Background(),
		plan.OperationID,
	)
	if !errors.Is(err, ErrSecretRecoveryRequired) {
		t.Fatalf("generation mismatch reconcile error=%v", err)
	}
	if result.Operation.Phase != OperationRecoveryRequired ||
		result.Operation.Effects[0].Status != EffectRunning ||
		store.writeCount() != 1 {
		t.Fatalf(
			"generation mismatch was falsely proved: result=%+v writes=%d",
			result,
			store.writeCount(),
		)
	}
}

func TestSecretServiceDeleteUsesTombstoneAndExactReplay(t *testing.T) {
	service, store := newSecretServiceFixture(t)
	initial := secretBufferFixture(t, "socks5://delete-fixture.invalid:1")
	if _, err := store.Set(context.Background(), secrets.WriteRequest{
		Ref: "local-proxy", OperationID: "op_deletefixture01",
		ExpectedGeneration: 0, Value: initial,
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(context.Background(), SecretDraft{
		Schema: SecretDraftSchema, Ref: "local-proxy",
		Action: secrets.ActionDelete,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(context.Background(), SecretApplyRequest{
		Schema: SecretApplySchema, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Ref: plan.Ref, Action: plan.Action,
		Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reference.Availability != secrets.AvailabilityMissing ||
		result.Reference.Generation != 2 {
		t.Fatalf("delete result=%+v", result)
	}
	writes := store.writeCount()
	replayed, err := service.Apply(context.Background(), SecretApplyRequest{
		Schema: SecretApplySchema, OperationID: plan.OperationID,
		PlanDigest: plan.PlanDigest, Ref: plan.Ref, Action: plan.Action,
		Confirmed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Reference.Generation != 2 || store.writeCount() != writes {
		t.Fatalf("delete replay=%+v writes=%d want=%d", replayed, store.writeCount(), writes)
	}
}

func TestSecretRecoveryNeverReplaysDeleteWithoutNegativeCommitProof(
	t *testing.T,
) {
	service, store := newSecretServiceFixture(t)
	initial := secretBufferFixture(
		t,
		"socks5://negative-proof.invalid:1",
	)
	if _, err := store.Set(context.Background(), secrets.WriteRequest{
		Ref: "local-proxy", OperationID: "op_negativefixture",
		ExpectedGeneration: 0, Value: initial,
	}); err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(context.Background(), SecretDraft{
		Schema: SecretDraftSchema, Ref: "local-proxy",
		Action: secrets.ActionDelete,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{
		OperationClaimed,
		OperationStaging,
	} {
		if _, err := service.operations().Transition(
			plan.OperationID,
			phase,
		); err != nil {
			t.Fatal(err)
		}
	}
	effect, err := secretProviderEffect(plan, store.Provider())
	if err != nil {
		t.Fatal(err)
	}
	if _, execute, err := service.operations().BeginEffect(
		plan.OperationID,
		effect.ID,
		effect.Provider,
	); err != nil || !execute {
		t.Fatalf("begin delete effect execute=%t err=%v", execute, err)
	}
	store.setReconcileUnproved(true)
	writesBefore := store.writeCount()

	result, err := service.ReconcileOperation(
		context.Background(),
		plan.OperationID,
	)
	if !errors.Is(err, ErrSecretRecoveryRequired) {
		t.Fatalf("ambiguous delete recovery error=%v", err)
	}
	if result.Operation.Phase != OperationRecoveryRequired ||
		result.Operation.Recovery.Code != "secret-provider-state-unproved" ||
		result.Operation.Effects[0].Status != EffectRunning ||
		store.writeCount() != writesBefore {
		t.Fatalf(
			"ambiguous delete was replayed: result=%+v writes=%d want=%d",
			result,
			store.writeCount(),
			writesBefore,
		)
	}
}

func TestSecretRotateCommitsGenerationAndAllLiveRoutesTogether(
	t *testing.T,
) {
	service, store, provider, environmentIDs :=
		newLiveSecretRotationFixture(t)
	provider.commitCheck = func() error {
		reference, err := store.Reference(
			context.Background(),
			"local-proxy",
		)
		if err != nil {
			return err
		}
		if reference.Generation != 2 {
			return errors.New(
				"gateway batch committed before Keychain generation",
			)
		}
		return nil
	}
	plan, err := service.Plan(
		context.Background(),
		SecretDraft{
			Schema: SecretDraftSchema,
			Ref:    "local-proxy",
			Action: secrets.ActionRotate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 ||
		len(plan.AffectedProfiles) != 1 ||
		len(plan.AffectedEnvironments) != 2 ||
		len(plan.Effects) != 11 {
		t.Fatalf("live secret plan=%+v", plan)
	}
	result, err := service.Apply(
		context.Background(),
		SecretApplyRequest{
			Schema: SecretApplySchema, OperationID: plan.OperationID,
			PlanDigest: plan.PlanDigest, Ref: plan.Ref,
			Action: plan.Action, Confirmed: true,
			Value: secretBufferFixture(
				t,
				"socks5://new-user:new-password@127.0.0.1:7890",
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation.Phase != OperationSucceeded ||
		result.Reference.Generation != 2 {
		t.Fatalf("live secret result=%+v", result)
	}
	for _, effect := range result.Operation.Effects {
		if effect.Status != EffectSucceeded ||
			len(effect.Evidence) == 0 {
			t.Fatalf("unproved live secret effect=%+v", effect)
		}
	}
	for _, environmentID := range environmentIDs {
		if provider.current[environmentID] !=
			secretRotationDesiredRoute() {
			t.Fatalf(
				"environment %s route=%+v",
				environmentID,
				provider.current[environmentID],
			)
		}
	}
	if provider.events[len(provider.events)-1] != "batch-commit" {
		t.Fatalf("live secret events=%v", provider.events)
	}
}

func TestSecretRotateRouteFailureKeepsOldGenerationAndRestoresAllRoutes(
	t *testing.T,
) {
	service, store, provider, environmentIDs :=
		newLiveSecretRotationFixture(t)
	ordered := append([]string(nil), environmentIDs...)
	sort.Strings(ordered)
	provider.failEnvironment = ordered[1]
	provider.failPhase = "activate"
	plan, err := service.Plan(
		context.Background(),
		SecretDraft{
			Schema: SecretDraftSchema,
			Ref:    "local-proxy",
			Action: secrets.ActionRotate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(
		context.Background(),
		SecretApplyRequest{
			Schema: SecretApplySchema, OperationID: plan.OperationID,
			PlanDigest: plan.PlanDigest, Ref: plan.Ref,
			Action: plan.Action, Confirmed: true,
			Value: secretBufferFixture(
				t,
				"socks5://new-user:new-password@127.0.0.1:7890",
			),
		},
	)
	if !errors.Is(err, ErrNetworkTransitionRolledBack) {
		t.Fatalf(
			"live secret route failure=%v result=%+v",
			err,
			result,
		)
	}
	reference, referenceErr := store.Reference(
		context.Background(),
		"local-proxy",
	)
	if referenceErr != nil ||
		reference.Generation != 1 ||
		store.writeCount() != 0 {
		t.Fatalf(
			"secret changed despite route failure: ref=%+v writes=%d err=%v",
			reference,
			store.writeCount(),
			referenceErr,
		)
	}
	if result.Operation.Phase != OperationRolledBack {
		t.Fatalf("live secret rollback=%+v", result.Operation)
	}
	for _, environmentID := range environmentIDs {
		if provider.current[environmentID] !=
			secretRotationFromRoute() {
			t.Fatalf(
				"environment %s was not restored: %+v",
				environmentID,
				provider.current[environmentID],
			)
		}
	}
}

func TestSecretRotateReconcilesKeychainResponseLossBeforeRouteCommit(
	t *testing.T,
) {
	service, store, provider, environmentIDs :=
		newLiveSecretRotationFixture(t)
	store.failNextWriteAfterCommit(
		errors.New("injected response loss"),
	)
	plan, err := service.Plan(
		context.Background(),
		SecretDraft{
			Schema: SecretDraftSchema,
			Ref:    "local-proxy",
			Action: secrets.ActionRotate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Apply(
		context.Background(),
		SecretApplyRequest{
			Schema: SecretApplySchema, OperationID: plan.OperationID,
			PlanDigest: plan.PlanDigest, Ref: plan.Ref,
			Action: plan.Action, Confirmed: true,
			Value: secretBufferFixture(
				t,
				"socks5://new-user:new-password@127.0.0.1:7890",
			),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation.Phase != OperationSucceeded ||
		result.Reference.Generation != 2 ||
		store.writeCount() != 1 {
		t.Fatalf("response-loss result=%+v", result)
	}
	for _, environmentID := range environmentIDs {
		if provider.current[environmentID] !=
			secretRotationDesiredRoute() {
			t.Fatalf(
				"environment %s did not commit reconciled generation",
				environmentID,
			)
		}
	}
}

func TestSecretRotateStartupRecoveryCompletesCommittedGenerationWithoutRouteReplay(
	t *testing.T,
) {
	service, store, _, _ := newLiveSecretRotationFixture(t)
	plan, err := service.Plan(
		context.Background(),
		SecretDraft{
			Schema: SecretDraftSchema,
			Ref:    "local-proxy",
			Action: secrets.ActionRotate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stageSecretLiveRouteProofs(t, service, plan)
	secretEffect, err := secretProviderEffect(plan, store.Provider())
	if err != nil {
		t.Fatal(err)
	}
	operation, execute, err := service.operations().BeginEffect(
		plan.OperationID,
		secretEffect.ID,
		secretEffect.Provider,
	)
	if err != nil || !execute {
		t.Fatalf(
			"begin secret effect execute=%t operation=%+v err=%v",
			execute,
			operation,
			err,
		)
	}
	value := secretBufferFixture(
		t,
		"socks5://restart-user:restart-password@127.0.0.1:7890",
	)
	if _, err := store.Set(
		context.Background(),
		secrets.WriteRequest{
			Ref:                plan.Ref,
			OperationID:        plan.OperationID,
			ExpectedGeneration: plan.BaseGeneration,
			Value:              value,
		},
	); err != nil {
		t.Fatal(err)
	}
	value.Clear()

	restarted := NewSecretService(service.Core, store)
	restarted.now = service.now
	reset := NetworkAuthorityResetProof{
		AuthorityID: "daemon_restart-proof-01",
		ObservedAt:  service.nowUTC(),
	}
	result, err := restarted.
		ReconcileOperationAfterNetworkAuthorityReset(
			context.Background(),
			plan.OperationID,
			reset,
		)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation.Phase != OperationSucceeded ||
		result.Operation.Result == nil ||
		result.Operation.Result.Code !=
			"secret-generation-committed-network-authority-reset" ||
		result.Reference.Generation != plan.NextGeneration ||
		store.writeCount() != 1 {
		t.Fatalf(
			"committed restart recovery=%+v writes=%d",
			result,
			store.writeCount(),
		)
	}
	recoveredSecret := operationEffect(
		result.Operation,
		secretEffect.ID,
	)
	if recoveredSecret == nil ||
		recoveredSecret.Status != EffectSucceeded ||
		!effectHasEvidenceCode(
			*recoveredSecret,
			"network-authority-reset",
		) {
		t.Fatalf(
			"secret reset evidence=%+v",
			recoveredSecret,
		)
	}
	for _, effect := range result.Operation.Effects {
		if effect.Status != EffectSucceeded ||
			len(effect.Evidence) == 0 {
			t.Fatalf(
				"committed recovery left effect unproved: %+v",
				effect,
			)
		}
	}

	replayed, err := restarted.
		ReconcileOperationAfterNetworkAuthorityReset(
			context.Background(),
			plan.OperationID,
			reset,
		)
	if err != nil ||
		replayed.Operation.Phase != OperationSucceeded ||
		store.writeCount() != 1 {
		t.Fatalf(
			"committed recovery replay=%+v writes=%d err=%v",
			replayed,
			store.writeCount(),
			err,
		)
	}
}

func TestSecretRotateStartupRecoveryAbortsStagedRoutesBeforeKeychainCommit(
	t *testing.T,
) {
	service, store, _, _ := newLiveSecretRotationFixture(t)
	plan, err := service.Plan(
		context.Background(),
		SecretDraft{
			Schema: SecretDraftSchema,
			Ref:    "local-proxy",
			Action: secrets.ActionRotate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stageSecretLiveRouteProofs(t, service, plan)

	restarted := NewSecretService(service.Core, store)
	restarted.now = service.now
	result, err := restarted.
		ReconcileOperationAfterNetworkAuthorityReset(
			context.Background(),
			plan.OperationID,
			NetworkAuthorityResetProof{
				AuthorityID: "daemon_restart-proof-02",
				ObservedAt:  service.nowUTC(),
			},
		)
	if err != nil {
		t.Fatal(err)
	}
	if result.Operation.Phase != OperationFailed ||
		result.Operation.Result == nil ||
		result.Operation.Result.Code !=
			"secret-live-transition-aborted-network-authority-reset" ||
		result.Reference.Generation != plan.BaseGeneration ||
		store.writeCount() != 0 {
		t.Fatalf(
			"uncommitted restart recovery=%+v writes=%d",
			result,
			store.writeCount(),
		)
	}
	secretEffect, err := secretProviderEffect(plan, store.Provider())
	if err != nil {
		t.Fatal(err)
	}
	failedSecret := operationEffect(
		result.Operation,
		secretEffect.ID,
	)
	if failedSecret == nil ||
		failedSecret.Status != EffectFailed ||
		!effectHasEvidenceCode(
			*failedSecret,
			"network-authority-reset",
		) {
		t.Fatalf(
			"failed secret reset evidence=%+v",
			failedSecret,
		)
	}
	for _, transition := range plan.networkTransitions {
		for _, planned := range transition.Effects {
			effect := operationEffect(
				result.Operation,
				profileNetworkEffectID(
					transition.EnvironmentID,
					planned.ID,
				),
			)
			if effect == nil ||
				effect.Status != EffectRolledBack ||
				!effectHasEvidenceCode(
					*effect,
					"network-authority-reset",
				) {
				t.Fatalf(
					"route reset effect=%+v",
					effect,
				)
			}
		}
	}
}

func TestSecretRotateStartupRecoveryRejectsCommittedGenerationWithoutExactRouteProofs(
	t *testing.T,
) {
	service, store, _, _ := newLiveSecretRotationFixture(t)
	plan, err := service.Plan(
		context.Background(),
		SecretDraft{
			Schema: SecretDraftSchema,
			Ref:    "local-proxy",
			Action: secrets.ActionRotate,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stageSecretLiveRouteEvidence(t, service, plan, false)
	secretEffect, err := secretProviderEffect(plan, store.Provider())
	if err != nil {
		t.Fatal(err)
	}
	if _, execute, err := service.operations().BeginEffect(
		plan.OperationID,
		secretEffect.ID,
		secretEffect.Provider,
	); err != nil || !execute {
		t.Fatalf("begin secret effect execute=%t err=%v", execute, err)
	}
	value := secretBufferFixture(
		t,
		"socks5://restart-user:restart-password@127.0.0.1:7890",
	)
	if _, err := store.Set(
		context.Background(),
		secrets.WriteRequest{
			Ref:                plan.Ref,
			OperationID:        plan.OperationID,
			ExpectedGeneration: plan.BaseGeneration,
			Value:              value,
		},
	); err != nil {
		t.Fatal(err)
	}
	value.Clear()

	restarted := NewSecretService(service.Core, store)
	restarted.now = service.now
	result, err := restarted.
		ReconcileOperationAfterNetworkAuthorityReset(
			context.Background(),
			plan.OperationID,
			NetworkAuthorityResetProof{
				AuthorityID: "daemon_restart-proof-03",
				ObservedAt:  service.nowUTC(),
			},
		)
	if !errors.Is(err, ErrSecretRecoveryRequired) ||
		result.Operation.Phase != OperationRecoveryRequired ||
		store.writeCount() != 1 {
		t.Fatalf(
			"incomplete route proof result=%+v writes=%d err=%v",
			result,
			store.writeCount(),
			err,
		)
	}
}

func stageSecretLiveRouteProofs(
	t *testing.T,
	service *SecretService,
	plan SecretPlan,
) {
	t.Helper()
	stageSecretLiveRouteEvidence(t, service, plan, true)
}

func stageSecretLiveRouteEvidence(
	t *testing.T,
	service *SecretService,
	plan SecretPlan,
	exact bool,
) {
	t.Helper()
	if _, err := service.operations().Transition(
		plan.OperationID,
		OperationClaimed,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := service.operations().Transition(
		plan.OperationID,
		OperationStaging,
	); err != nil {
		t.Fatal(err)
	}
	for _, transition := range plan.networkTransitions {
		for _, planned := range transition.Effects {
			effectID := profileNetworkEffectID(
				transition.EnvironmentID,
				planned.ID,
			)
			operation, execute, err :=
				service.operations().BeginEffect(
					plan.OperationID,
					effectID,
					planned.Provider,
				)
			if err != nil || !execute {
				t.Fatalf(
					"begin route effect %s execute=%t operation=%+v err=%v",
					effectID,
					execute,
					operation,
					err,
				)
			}
			if _, err := service.operations().FinishEffect(
				plan.OperationID,
				effectID,
				planned.Provider,
				EffectSucceeded,
				[]EvidenceRef{{
					Code: func() string {
						if exact {
							return planned.ProofRequired[0]
						}
						return "test-network-proof"
					}(),
					Ref: "environment:" + transition.EnvironmentID,
				}},
			); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func effectHasEvidenceCode(
	effect EffectResult,
	code string,
) bool {
	for _, evidence := range effect.Evidence {
		if evidence.Code == code {
			return true
		}
	}
	return false
}

func newLiveSecretRotationFixture(
	t *testing.T,
) (
	*SecretService,
	*managerSecretStoreFixture,
	*profileNetworkBatchProvider,
	[]string,
) {
	t.Helper()
	service, store := newSecretServiceFixture(t)
	store.mu.Lock()
	store.reference = secrets.Reference{
		Schema: secrets.SecretReferenceSchema, Ref: "local-proxy",
		Provider:     store.Provider(),
		Availability: secrets.AvailabilityAvailable,
		Generation:   1,
		UpdatedAt: time.Date(
			2026, 7, 29, 19, 0, 0, 0, time.UTC,
		),
	}
	store.value = []byte(
		"socks5://old-user:old-password@127.0.0.1:7890",
	)
	store.mu.Unlock()

	selected := profile.Default("default")
	selected.Network = profile.Network{
		Mode:             profile.NetworkModeTun2Socks,
		ProxySecretRef:   "local-proxy",
		MediatedResolver: "1.1.1.1",
	}
	if err := service.Core.Store.Create(selected); err != nil {
		t.Fatal(err)
	}
	selected, err := service.Core.Store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	environmentIDs := []string{
		createLiveProfileEnvironment(
			t,
			service.Core.Store,
			selected,
			"secret-live-route-a",
		),
		createLiveProfileEnvironment(
			t,
			service.Core.Store,
			selected,
			"secret-live-route-b",
		),
	}
	current := make(
		map[string]NetworkRouteConfiguration,
		len(environmentIDs),
	)
	for _, environmentID := range environmentIDs {
		current[environmentID] = secretRotationFromRoute()
	}
	provider := newProfileNetworkBatchProvider(
		current,
		secretRotationDesiredRoute(),
	)
	service.NetworkTransitions =
		&ProfileNetworkTransitionCoordinator{
			Core: service.Core, Provider: provider,
			Sessions: fixedNetworkTransitionSessions(),
		}
	return service, store, provider, environmentIDs
}

func secretRotationFromRoute() NetworkRouteConfiguration {
	return NetworkRouteConfiguration{
		Mode:           netpolicy.ModeTun2Socks,
		ProxySecretRef: "local-proxy", ProxySecretGeneration: 1,
		MediatedResolver: "1.1.1.1",
	}
}

func secretRotationDesiredRoute() NetworkRouteConfiguration {
	desired := secretRotationFromRoute()
	desired.ProxySecretGeneration = 2
	return desired
}

func newSecretServiceFixture(
	t *testing.T,
) (*SecretService, *managerSecretStoreFixture) {
	t.Helper()
	core := New(profile.Store{Root: t.TempDir()})
	store := newManagerSecretStoreFixture()
	service := NewSecretService(core, store)
	service.now = func() time.Time {
		return time.Date(2026, 7, 29, 20, 0, 0, 0, time.UTC)
	}
	return service, store
}

func secretBufferFixture(t *testing.T, value string) *secrets.Buffer {
	t.Helper()
	buffer, err := secrets.NewBuffer([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return buffer
}

func assertManagerSecretBufferCleared(
	t *testing.T,
	buffer *secrets.Buffer,
) {
	t.Helper()
	if err := buffer.Use(func([]byte) error { return nil }); !errors.Is(
		err,
		secrets.ErrSecretBufferUsed,
	) {
		t.Fatalf("secret buffer was not consumed: %v", err)
	}
}

type managerSecretStoreFixture struct {
	mu sync.Mutex

	reference         secrets.Reference
	value             []byte
	operation         string
	base              uint64
	writes            int
	failAfter         error
	reconcileErr      error
	reconcileUnproved bool
}

func newManagerSecretStoreFixture() *managerSecretStoreFixture {
	return &managerSecretStoreFixture{
		reference: secrets.Reference{
			Schema: secrets.SecretReferenceSchema, Ref: "local-proxy",
			Provider:     "memory-keychain",
			Availability: secrets.AvailabilityMissing,
			Reason:       "secret-missing",
		},
	}
}

func (store *managerSecretStoreFixture) Provider() string {
	return "memory-keychain"
}

func (store *managerSecretStoreFixture) List(
	context.Context,
) ([]secrets.Reference, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return []secrets.Reference{store.reference}, nil
}

func (store *managerSecretStoreFixture) Reference(
	_ context.Context,
	ref string,
) (secrets.Reference, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if ref != store.reference.Ref {
		return secrets.Reference{
			Schema: secrets.SecretReferenceSchema, Ref: ref,
			Provider:     store.Provider(),
			Availability: secrets.AvailabilityMissing,
			Reason:       "secret-missing",
		}, nil
	}
	return store.reference, nil
}

func (store *managerSecretStoreFixture) Set(
	_ context.Context,
	request secrets.WriteRequest,
) (secrets.Reference, error) {
	defer request.Value.Clear()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.operation == request.OperationID && store.base == request.ExpectedGeneration &&
		store.reference.Ref == request.Ref &&
		store.reference.Availability == secrets.AvailabilityAvailable {
		matched := false
		if err := request.Value.Use(func(raw []byte) error {
			matched = bytes.Equal(raw, store.value)
			return nil
		}); err != nil {
			return secrets.Reference{}, err
		}
		if !matched {
			return secrets.Reference{}, secrets.ErrSecretOperationMismatch
		}
		return store.reference, nil
	}
	if request.ExpectedGeneration != store.reference.Generation {
		return secrets.Reference{}, &secrets.GenerationConflictError{
			Ref: request.Ref, Expected: request.ExpectedGeneration,
			Current: store.reference.Generation,
		}
	}
	var value []byte
	if err := request.Value.Use(func(raw []byte) error {
		value = append([]byte(nil), raw...)
		return nil
	}); err != nil {
		return secrets.Reference{}, err
	}
	store.base = request.ExpectedGeneration
	store.operation = request.OperationID
	store.value = value
	store.writes++
	store.reference = secrets.Reference{
		Schema: secrets.SecretReferenceSchema, Ref: request.Ref,
		Provider:     store.Provider(),
		Availability: secrets.AvailabilityAvailable,
		Generation:   request.ExpectedGeneration + 1,
		UpdatedAt: time.Date(
			2026, 7, 29, 20, 1, 0, 0, time.UTC,
		),
	}
	if store.failAfter != nil {
		err := store.failAfter
		store.failAfter = nil
		return secrets.Reference{}, err
	}
	return store.reference, nil
}

func (store *managerSecretStoreFixture) Delete(
	_ context.Context,
	request secrets.DeleteRequest,
) (secrets.Reference, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.operation == request.OperationID && store.base == request.ExpectedGeneration &&
		store.reference.Ref == request.Ref &&
		store.reference.Availability == secrets.AvailabilityMissing {
		return store.reference, nil
	}
	if request.ExpectedGeneration != store.reference.Generation {
		return secrets.Reference{}, &secrets.GenerationConflictError{
			Ref: request.Ref, Expected: request.ExpectedGeneration,
			Current: store.reference.Generation,
		}
	}
	store.base = request.ExpectedGeneration
	store.operation = request.OperationID
	clear(store.value)
	store.value = nil
	store.writes++
	store.reference = secrets.Reference{
		Schema: secrets.SecretReferenceSchema, Ref: request.Ref,
		Provider:     store.Provider(),
		Availability: secrets.AvailabilityMissing,
		Generation:   request.ExpectedGeneration + 1,
		UpdatedAt: time.Date(
			2026, 7, 29, 20, 2, 0, 0, time.UTC,
		),
		Reason: "secret-deleted",
	}
	if store.failAfter != nil {
		err := store.failAfter
		store.failAfter = nil
		return secrets.Reference{}, err
	}
	return store.reference, nil
}

func (store *managerSecretStoreFixture) Resolve(
	context.Context,
	string,
) (*secrets.Buffer, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.reference.Availability != secrets.AvailabilityAvailable {
		return nil, secrets.ErrSecretMissing
	}
	return secrets.NewBuffer(store.value)
}

func (store *managerSecretStoreFixture) Reconcile(
	_ context.Context,
	request secrets.ReconcileRequest,
) (secrets.ReconcileResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.reconcileErr != nil {
		return secrets.ReconcileResult{}, store.reconcileErr
	}
	if store.reconcileUnproved {
		return secrets.ReconcileResult{Reference: store.reference}, nil
	}
	want := secrets.AvailabilityAvailable
	if request.Action == secrets.ActionDelete {
		want = secrets.AvailabilityMissing
	}
	committed := store.operation == request.OperationID &&
		store.base == request.ExpectedGeneration &&
		store.reference.Availability == want
	baseAvailability := secrets.AvailabilityAvailable
	if request.Action == secrets.ActionSet {
		baseAvailability = secrets.AvailabilityMissing
	}
	return secrets.ReconcileResult{
		Reference: store.reference,
		Committed: committed,
		Uncommitted: !committed &&
			store.reference.Generation == request.ExpectedGeneration &&
			store.reference.Availability == baseAvailability,
	}, nil
}

func (store *managerSecretStoreFixture) setReconcileError(err error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reconcileErr = err
}

func (store *managerSecretStoreFixture) setReconcileUnproved(value bool) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.reconcileUnproved = value
}

func (store *managerSecretStoreFixture) failNextWriteAfterCommit(
	err error,
) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.failAfter = err
}

func (store *managerSecretStoreFixture) writeCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.writes
}

func stageRunningSecretEffect(
	t *testing.T,
	service *SecretService,
) SecretPlan {
	t.Helper()
	plan, err := service.Plan(context.Background(), SecretDraft{
		Schema: SecretDraftSchema, Ref: "local-proxy",
		Action: secrets.ActionSet,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{OperationClaimed, OperationStaging} {
		if _, err := service.operations().Transition(
			plan.OperationID,
			phase,
		); err != nil {
			t.Fatal(err)
		}
	}
	effect, err := secretProviderEffect(plan, service.Store.Provider())
	if err != nil {
		t.Fatal(err)
	}
	if _, execute, err := service.operations().BeginEffect(
		plan.OperationID,
		effect.ID,
		effect.Provider,
	); err != nil || !execute {
		t.Fatalf("stage running effect execute=%t err=%v", execute, err)
	}
	return plan
}

var (
	_ secrets.RuntimeStore        = (*managerSecretStoreFixture)(nil)
	_ secrets.OperationReconciler = (*managerSecretStoreFixture)(nil)
)
