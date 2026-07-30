package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vibe-agi/hideout/internal/lifecycle"
	"github.com/vibe-agi/hideout/internal/profile"
)

func TestProfileTransactionConcurrentClientsCommitExactlyOneReviewedPlan(t *testing.T) {
	service, store, projection := newProfileTransactionTestService(t)
	var persists atomic.Int32
	service.persistProfile = func(value profile.Profile) error {
		persists.Add(1)
		return store.Save(value)
	}
	first := planProfileEnvironmentChange(t, service, projection, "RACE_VALUE", "first")
	second := planProfileNetworkChange(t, service, projection)

	requests := []ConfigurationApplyRequest{
		configurationApplyRequest(first),
		configurationApplyRequest(second),
	}
	results := make([]ConfigurationApplyResult, len(requests))
	errs := make([]error, len(requests))
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range requests {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			results[index], errs[index] = service.Apply(context.Background(), requests[index])
		}(index)
	}
	close(start)
	wait.Wait()

	success := -1
	stale := -1
	for index, err := range errs {
		switch {
		case err == nil:
			success = index
		case errors.Is(err, ErrStaleConfigurationPlan):
			stale = index
		default:
			t.Fatalf("apply %d error=%v results=%+v", index, err, results)
		}
	}
	if success < 0 || stale < 0 || success == stale {
		t.Fatalf("concurrent results=%+v errors=%v", results, errs)
	}
	if persists.Load() != 1 {
		t.Fatalf("profile persisted %d times, want exactly one", persists.Load())
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if success == 0 && current.Env.Public["RACE_VALUE"] != "first" {
		t.Fatalf("environment winner committed %+v", current.Env.Public)
	}
	if success == 1 &&
		(current.Network.Mode != profile.NetworkModeTun2Socks ||
			current.Network.ProxySecretRef != "local-proxy") {
		t.Fatalf("network winner committed %+v", current.Network)
	}
	if results[success].Operation.Phase != OperationSucceeded ||
		results[success].Projection.Revision != projection.Revision+1 {
		t.Fatalf("winner result=%+v", results[success])
	}
	loser, err := (OperationStore{Root: store.Root}).Load(requests[stale].OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if loser.Phase != OperationCancelled ||
		loser.Result == nil || loser.Result.Code != "stale-plan" {
		t.Fatalf("stale operation=%+v", loser)
	}
}

func TestProfileTransactionInspectPlanRevalidatesExactOperationBinding(
	t *testing.T,
) {
	service, store, projection := newProfileTransactionTestService(t)
	plan := planProfileNetworkChange(t, service, projection)
	inspected, err := service.InspectPlan(
		context.Background(),
		plan.OperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if inspected.OperationID != plan.OperationID ||
		inspected.Profile != plan.Profile ||
		inspected.BaseRevision != plan.BaseRevision ||
		inspected.PlanDigest != plan.PlanDigest ||
		inspected.VerifyDigest() != nil {
		t.Fatalf("inspected plan does not match exact review: %+v", inspected)
	}
	plannedOperation, err := (OperationStore{Root: store.Root}).Load(
		plan.OperationID,
	)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := ConfigurationApplyRequestForOperation(plannedOperation)
	if err != nil {
		t.Fatal(err)
	}
	if retry != configurationApplyRequest(plan) {
		t.Fatalf("operation retry binding=%+v plan=%+v", retry, plan)
	}

	foreignID, err := NewOperationID()
	if err != nil {
		t.Fatal(err)
	}
	_, created, err := (OperationStore{Root: store.Root}).Reserve(
		OperationBinding{
			ID: foreignID, Kind: "environment.stop",
			Owner:        OperationOwner{Kind: "environment", ID: "env_other"},
			PlanDigest:   "sha256:" + strings.Repeat("a", 64),
			BaseRevision: 1,
		},
		[]EffectResult{{
			ID: "stop", Kind: "cleanup", Provider: "manager",
		}},
	)
	if err != nil || !created {
		t.Fatalf("reserve foreign operation created=%t err=%v", created, err)
	}
	if _, err := service.InspectPlan(
		context.Background(),
		foreignID,
	); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("foreign inspect error=%v", err)
	}
	foreign, err := (OperationStore{Root: store.Root}).Load(foreignID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConfigurationApplyRequestForOperation(
		foreign,
	); !errors.Is(err, ErrInvalidConfigurationPlan) {
		t.Fatalf("foreign retry binding error=%v", err)
	}
}

func TestProfileTransactionPlanJSONEncodesCollectionsAsArrays(t *testing.T) {
	service, _, projection := newProfileTransactionTestService(t)
	change, err := NewTypedChange(
		ChangeActivityRetention,
		map[string]any{
			"maxBytes":      268439552,
			"maxAgeSeconds": 0,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(
		context.Background(),
		ConfigurationDraft{
			Schema:       ConfigurationDraftSchema,
			Profile:      projection.Profile,
			BaseRevision: projection.Revision,
			ClientNonce:  "browser-array-contract",
			Changes:      []TypedChange{change},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		"canonicalChanges",
		"diff",
		"effects",
		"blockers",
		"warnings",
	} {
		value := document[field]
		if len(value) == 0 || value[0] != '[' {
			t.Fatalf("%s must encode as an array: %s", field, value)
		}
	}
	var rollback map[string]json.RawMessage
	if err := json.Unmarshal(document["rollback"], &rollback); err != nil {
		t.Fatal(err)
	}
	if value := rollback["effects"]; len(value) == 0 || value[0] != '[' {
		t.Fatalf("rollback.effects must encode as an array: %s", value)
	}
}

func TestProfileTransactionRejectsOutOfBandDriftBeforeAnyEffect(t *testing.T) {
	service, store, projection := newProfileTransactionTestService(t)
	var persists atomic.Int32
	service.persistProfile = func(value profile.Profile) error {
		persists.Add(1)
		return store.Save(value)
	}
	plan := planProfileEnvironmentChange(t, service, projection, "STALE_VALUE", "reviewed")

	external, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	external.Identity.Hostname = "external-edit"
	if err := store.Save(external); err != nil {
		t.Fatal(err)
	}
	_, err = service.Apply(context.Background(), configurationApplyRequest(plan))
	if !errors.Is(err, ErrStaleConfigurationPlan) {
		t.Fatalf("stale apply error=%v", err)
	}
	if persists.Load() != 0 {
		t.Fatalf("stale plan executed %d effects", persists.Load())
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Identity.Hostname != "external-edit" ||
		current.Env.Public["STALE_VALUE"] != "" {
		t.Fatalf("stale apply changed desired state: %+v", current)
	}
	if _, err := os.Lstat(service.planPath(plan.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled operation retained private plan record: %v", err)
	}
}

func TestProfileTransactionRejectsUnownedExternalTargetAsStale(t *testing.T) {
	service, store, projection := newProfileTransactionTestService(t)
	var persists atomic.Int32
	service.persistProfile = func(value profile.Profile) error {
		persists.Add(1)
		return store.Save(value)
	}
	plan := planProfileEnvironmentChange(
		t, service, projection, "CONVERGED_VALUE", "reviewed",
	)

	external, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	external.Env.Public["CONVERGED_VALUE"] = "reviewed"
	if err := store.Save(external); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(
		context.Background(),
		configurationApplyRequest(plan),
	); !errors.Is(err, ErrStaleConfigurationPlan) {
		t.Fatalf("unowned external target error=%v", err)
	}
	if persists.Load() != 0 {
		t.Fatalf("unowned external target executed %d effects", persists.Load())
	}
	operation, err := (OperationStore{Root: store.Root}).Load(plan.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.Phase != OperationCancelled ||
		operation.Result == nil ||
		operation.Result.Code != "stale-plan" {
		t.Fatalf("unowned external target operation=%+v", operation)
	}
}

func TestProfileTransactionExpiredPlanIsCancelledAndRemoved(t *testing.T) {
	service, _, projection := newProfileTransactionTestService(t)
	plan := planProfileEnvironmentChange(
		t, service, projection, "EXPIRED_VALUE", "reviewed",
	)
	service.now = func() time.Time {
		return plan.ExpiresAt
	}

	if _, err := service.Apply(
		context.Background(),
		configurationApplyRequest(plan),
	); !errors.Is(err, ErrConfigurationPlanExpired) {
		t.Fatalf("expired apply error=%v", err)
	}
	if _, err := os.Lstat(service.planPath(plan.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired operation retained private plan record: %v", err)
	}
}

func TestProfileTransactionPersistenceFailureIsTerminalAndReplayable(t *testing.T) {
	service, store, projection := newProfileTransactionTestService(t)
	persistFailure := errors.New("simulated profile store failure")
	var persists atomic.Int32
	service.persistProfile = func(profile.Profile) error {
		persists.Add(1)
		return persistFailure
	}
	plan := planProfileEnvironmentChange(
		t, service, projection, "FAILED_VALUE", "reviewed",
	)
	request := configurationApplyRequest(plan)

	if _, err := service.Apply(
		context.Background(),
		request,
	); !errors.Is(err, persistFailure) {
		t.Fatalf("failed persistence error=%v", err)
	}
	if _, err := os.Lstat(service.planPath(plan.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed operation retained private plan record: %v", err)
	}
	replayed, err := service.Apply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Operation.Phase != OperationFailed ||
		replayed.Operation.Result == nil ||
		replayed.Operation.Result.Code != "profile-persist-failed" ||
		persists.Load() != 1 {
		t.Fatalf("failed replay=%+v persists=%d", replayed, persists.Load())
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Env.Public["FAILED_VALUE"] != "" {
		t.Fatalf("failed persistence changed profile: %+v", current.Env.Public)
	}
}

func TestProfileTransactionApplyLockIsReleasedAfterWaiters(t *testing.T) {
	key := t.TempDir() + "\x00op_locklifecycle"
	firstRelease := acquireProfileTransactionApplyLock(key)
	acquired := make(chan func(), 1)
	go func() {
		acquired <- acquireProfileTransactionApplyLock(key)
	}()
	select {
	case release := <-acquired:
		release()
		t.Fatal("second caller acquired an operation lock before release")
	case <-time.After(25 * time.Millisecond):
	}
	firstRelease()
	var secondRelease func()
	select {
	case secondRelease = <-acquired:
	case <-time.After(5 * time.Second):
		t.Fatal("second caller did not acquire the released operation lock")
	}
	secondRelease()

	profileTransactionApplyLocksMu.Lock()
	_, retained := profileTransactionApplyLocks[key]
	profileTransactionApplyLocksMu.Unlock()
	if retained {
		t.Fatal("unused per-operation apply lock was retained")
	}
}

func TestProfileTransactionRejectsCanonicalReplanDrift(t *testing.T) {
	service, store, projection := newProfileTransactionTestService(t)
	plan := planProfileEnvironmentChange(
		t, service, projection, "REPLAN_VALUE", "reviewed",
	)
	originalBuild := service.build
	service.build = func(
		ctx context.Context,
		current profile.Profile,
		changes []TypedChange,
	) (profileTransactionBuild, error) {
		build, err := originalBuild(ctx, current, changes)
		if err == nil {
			build.Effects[0].Summary += " changed after review"
		}
		return build, err
	}
	_, err := service.Apply(context.Background(), configurationApplyRequest(plan))
	if !errors.Is(err, ErrStaleConfigurationPlan) {
		t.Fatalf("canonical replan drift error=%v", err)
	}
	current, loadErr := store.Load("default")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if current.Env.Public["REPLAN_VALUE"] != "" {
		t.Fatalf("replan drift changed desired state: %+v", current.Env.Public)
	}
	operation, loadErr := (OperationStore{Root: store.Root}).Load(plan.OperationID)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if operation.Phase != OperationCancelled {
		t.Fatalf("replan drift operation=%+v", operation)
	}
}

func TestProfileTransactionPlanRecordIsMinimalAndIntegrityBound(t *testing.T) {
	service, store, projection := newProfileTransactionTestService(t)
	plan := planProfileEnvironmentChange(
		t, service, projection, "PLAN_VALUE", "reviewed",
	)
	path := service.planPath(plan.OperationID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`"desired"`,
		"developer@example.com",
		`"machineId"`,
	} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("private plan record copied unrelated profile field %q: %s", forbidden, data)
		}
	}
	var record profileTransactionPlanRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	record.TargetDigest = digestFixture("e")
	tampered, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(tampered, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Apply(
		context.Background(),
		configurationApplyRequest(plan),
	); !errors.Is(err, ErrInvalidConfigurationPlan) {
		t.Fatalf("tampered plan record error=%v", err)
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Env.Public["PLAN_VALUE"] != "" {
		t.Fatalf("tampered plan changed desired state: %+v", current.Env.Public)
	}
}

func TestProfileTransactionReportsConflictingMutationKeyOwner(t *testing.T) {
	service, store, projection := newProfileTransactionTestService(t)
	first := planProfileEnvironmentChange(t, service, projection, "CONFLICT_A", "first")
	second := planProfileEnvironmentChange(t, service, projection, "CONFLICT_B", "second")

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	service.persistProfile = func(value profile.Profile) error {
		once.Do(func() { close(entered) })
		<-release
		return store.Save(value)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, err := service.Apply(context.Background(), configurationApplyRequest(first))
		firstDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first transaction did not claim its mutation key")
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := service.Apply(context.Background(), configurationApplyRequest(second))
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		var conflict *ConfigurationMutationConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("conflicting apply error=%v", err)
		}
		if conflict.Key != lifecycle.ProfileMutationKey(
			"default",
			"profile.environment",
		) ||
			conflict.OwnerOperationID != first.OperationID ||
			conflict.OwnerKind != lifecycle.MutationOwnerConfiguration ||
			conflict.OwnerPhase != lifecycle.MutationPhaseApplying ||
			conflict.Recovery == "" {
			t.Fatalf("conflict=%+v", conflict)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("conflicting transaction blocked instead of reporting its owner")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestProfileTransactionAndAttachUseSameLifecycleMutationKey(t *testing.T) {
	t.Run("attach blocks configuration without changing profile", func(t *testing.T) {
		service, store, projection := newProfileTransactionTestService(t)
		coordinator := newProfileLifecycleCoordinator(t, store, service.now)
		service.Mutations = coordinator
		key := lifecycle.ProfileMutationKey("default", "profile.environment")
		reservation, err := coordinator.ReserveAttach(
			context.Background(),
			lifecycle.EstablishmentRequest{
				EnvironmentID: "env-profile-conflict",
				SessionID:     "ses-profile-conflict",
				MutationKeys:  []string{key},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		plan := planProfileEnvironmentChange(
			t,
			service,
			projection,
			"ATTACH_CONFLICT",
			"must-not-commit",
		)
		_, err = service.Apply(
			context.Background(),
			configurationApplyRequest(plan),
		)
		var conflict *ConfigurationMutationConflictError
		if !errors.As(err, &conflict) ||
			conflict.Key != key ||
			conflict.OwnerOperationID != "ses-profile-conflict" ||
			conflict.OwnerKind != lifecycle.MutationOwnerAttach ||
			conflict.OwnerPhase != lifecycle.MutationPhaseEstablishing ||
			conflict.Recovery == "" {
			t.Fatalf("configuration conflict=%+v err=%v", conflict, err)
		}
		current, loadErr := store.Load("default")
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if current.Env.Public["ATTACH_CONFLICT"] != "" {
			t.Fatalf("blocked configuration changed profile: %+v", current.Env.Public)
		}
		operation, loadErr := service.operationStore().Load(plan.OperationID)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if operation.Phase != OperationPlanned {
			t.Fatalf("blocked operation phase=%s", operation.Phase)
		}
		if err := reservation.Abort(context.Background(), nil); err != nil {
			t.Fatal(err)
		}
		result, err := service.Apply(
			context.Background(),
			configurationApplyRequest(plan),
		)
		if err != nil || result.Operation.Phase != OperationSucceeded {
			t.Fatalf("retry after attach release result=%+v err=%v", result, err)
		}
	})

	t.Run("configuration blocks attach without duplicating apply", func(t *testing.T) {
		service, store, projection := newProfileTransactionTestService(t)
		coordinator := newProfileLifecycleCoordinator(t, store, service.now)
		service.Mutations = coordinator
		plan := planProfileEnvironmentChange(
			t,
			service,
			projection,
			"CONFIG_CONFLICT",
			"committed-once",
		)
		entered := make(chan struct{})
		release := make(chan struct{})
		var persistCalls atomic.Int32
		var enteredOnce sync.Once
		service.persistProfile = func(value profile.Profile) error {
			persistCalls.Add(1)
			enteredOnce.Do(func() { close(entered) })
			<-release
			return store.Save(value)
		}
		applyDone := make(chan error, 1)
		go func() {
			_, err := service.Apply(
				context.Background(),
				configurationApplyRequest(plan),
			)
			applyDone <- err
		}()
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("configuration did not acquire lifecycle key")
		}
		_, err := coordinator.ReserveAttach(
			context.Background(),
			lifecycle.EstablishmentRequest{
				EnvironmentID: "env-config-owner",
				SessionID:     "ses-config-waiter",
				MutationKeys:  attachProfileMutationKeys("default"),
			},
		)
		var blocker *lifecycle.MutationConflictError
		if !errors.As(err, &blocker) ||
			blocker.Owner.Kind != lifecycle.MutationOwnerConfiguration ||
			blocker.Owner.ID != plan.OperationID ||
			blocker.Owner.Phase != lifecycle.MutationPhaseApplying ||
			blocker.Owner.Recovery == "" {
			t.Fatalf("attach blocker=%+v err=%v", blocker, err)
		}
		close(release)
		if err := <-applyDone; err != nil {
			t.Fatalf("attach conflict damaged configuration: %v", err)
		}
		if persistCalls.Load() != 1 {
			t.Fatalf("configuration persisted %d times", persistCalls.Load())
		}
	})
}

func newProfileLifecycleCoordinator(
	t *testing.T,
	store profile.Store,
	now func() time.Time,
) *lifecycle.Coordinator {
	t.Helper()
	coordinator, err := lifecycle.NewCoordinator(lifecycle.CoordinatorOptions{
		Store:     lifecycle.JournalStore{Root: store.Root},
		DaemonID:  "daemon-profile-test",
		IdleGrace: time.Hour,
		Now:       now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := coordinator.Close(); err != nil {
			t.Error(err)
		}
	})
	return coordinator
}

func TestProfileTransactionResponseLossRetryReplaysTerminalResult(t *testing.T) {
	service, store, projection := newProfileTransactionTestService(t)
	observer := &profileTransactionObserver{}
	service.Core.Observer = observer
	var persists atomic.Int32
	service.persistProfile = func(value profile.Profile) error {
		persists.Add(1)
		return store.Save(value)
	}
	responseLost := errors.New("simulated response loss")
	var loseOnce atomic.Bool
	service.hooks.afterTerminal = func(Operation) error {
		if loseOnce.CompareAndSwap(false, true) {
			return responseLost
		}
		return nil
	}
	plan := planProfileEnvironmentChange(t, service, projection, "RETRY_VALUE", "once")
	request := configurationApplyRequest(plan)

	if _, err := service.Apply(context.Background(), request); !errors.Is(err, responseLost) {
		t.Fatalf("first apply error=%v", err)
	}
	if _, err := os.Lstat(service.planPath(plan.OperationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("terminal operation retained private plan record: %v", err)
	}
	replayed, err := service.Apply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Operation.Phase != OperationSucceeded ||
		replayed.Operation.ID != plan.OperationID ||
		persists.Load() != 1 {
		t.Fatalf("replay=%+v persists=%d", replayed, persists.Load())
	}
	if observer.terminalCount(plan.OperationID) != 1 {
		t.Fatalf("terminal events=%d want 1", observer.terminalCount(plan.OperationID))
	}

	mismatch := request
	mismatch.PlanDigest = digestFixture("f")
	if _, err := service.Apply(context.Background(), mismatch); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("mismatched retry error=%v", err)
	}
	if persists.Load() != 1 {
		t.Fatalf("mismatched retry duplicated effect: %d", persists.Load())
	}
}

func TestProfileTransactionProductionBuilderKeepsEnvironmentValueOutOfPublicPlan(t *testing.T) {
	store := profile.Store{Root: t.TempDir()}
	if _, err := store.LoadOrInit("default"); err != nil {
		t.Fatal(err)
	}
	service := NewProfileTransactionService(New(store))
	service.now = func() time.Time {
		return time.Date(2026, 7, 29, 14, 30, 0, 0, time.UTC)
	}
	projection, err := (ProfileProjectionService{
		Store: store, Now: service.now,
	}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	const canary = "socks5://operator:private-password@127.0.0.1:7890"
	plan := planProfileEnvironmentChange(
		t,
		service,
		projection,
		"LOCAL_PROXY",
		canary,
	)
	public, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(public), canary) ||
		!strings.Contains(string(public), "[value provided]") {
		t.Fatalf("public plan exposed or omitted environment review marker: %s", public)
	}

	recordPath := service.planPath(plan.OperationID)
	info, err := os.Lstat(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("private plan mode=%#o want 0600", info.Mode().Perm())
	}
	private, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(private), canary) {
		t.Fatalf("private replay record does not contain the exact apply input: %s", private)
	}
	if strings.Contains(string(private), `"desired"`) {
		t.Fatalf("private replay record copied the complete desired profile: %s", private)
	}

	request := configurationApplyRequest(plan)
	applied, err := service.Apply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Operation.Phase != OperationSucceeded {
		t.Fatalf("apply operation=%+v", applied.Operation)
	}
	current, err := store.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Env.Public["LOCAL_PROXY"] != canary {
		t.Fatalf("applied environment value=%q want exact private input", current.Env.Public["LOCAL_PROXY"])
	}
	replayed, err := service.Apply(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Operation.ID != applied.Operation.ID ||
		replayed.Operation.Phase != OperationSucceeded {
		t.Fatalf("idempotent replay=%+v", replayed)
	}
}

func TestProfileTransactionCrashBoundariesResumeWithoutDuplicateEffect(t *testing.T) {
	cases := []struct {
		name       string
		install    func(*profileTransactionHooks, error)
		firstPhase string
		firstState string
	}{
		{
			name: "after persist before effect checkpoint",
			install: func(hooks *profileTransactionHooks, crash error) {
				hooks.afterPersist = failProfileTransactionHookOnce(crash)
			},
			firstPhase: OperationStaging,
			firstState: EffectRunning,
		},
		{
			name: "after projection commit before terminal checkpoint",
			install: func(hooks *profileTransactionHooks, crash error) {
				hooks.afterProjectionCommit = failProfileTransactionHookOnce(crash)
			},
			firstPhase: OperationProving,
			firstState: EffectSucceeded,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service, store, projection := newProfileTransactionTestService(t)
			var persists atomic.Int32
			service.persistProfile = func(value profile.Profile) error {
				persists.Add(1)
				return store.Save(value)
			}
			crash := errors.New("simulated daemon crash")
			testCase.install(&service.hooks, crash)
			plan := planProfileEnvironmentChange(
				t, service, projection, "CRASH_VALUE", testCase.name,
			)
			request := configurationApplyRequest(plan)

			if _, err := service.Apply(context.Background(), request); !errors.Is(err, crash) {
				t.Fatalf("first apply error=%v", err)
			}
			checkpoint, err := (OperationStore{Root: store.Root}).Load(plan.OperationID)
			if err != nil {
				t.Fatal(err)
			}
			if checkpoint.Phase != testCase.firstPhase ||
				len(checkpoint.Effects) != 1 ||
				checkpoint.Effects[0].Status != testCase.firstState {
				t.Fatalf("durable crash checkpoint=%+v", checkpoint)
			}

			restarted := NewProfileTransactionService(service.Core)
			restarted.now = service.now
			resumed, err := restarted.ReconcileOperation(
				context.Background(),
				plan.OperationID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if resumed.Operation.Phase != OperationSucceeded ||
				resumed.Projection.Revision != projection.Revision+1 ||
				persists.Load() != 1 {
				t.Fatalf("resumed=%+v persists=%d", resumed, persists.Load())
			}
		})
	}
}

func TestProfileTransactionLeavesExistingSessionSnapshotImmutable(t *testing.T) {
	service, store, projection := newProfileTransactionTestService(t)
	core := service.Core
	workspace := t.TempDir()
	beforePlan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "native", Workspace: workspace,
		Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	existing, err := core.BeginRunSession(
		beforePlan,
		RunEnvironment{},
		RunSessionOptions{ExplainOnly: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := core.CloseRunSession(existing); err != nil {
			t.Errorf("close existing run session: %v", err)
		}
	}()
	existingID := existing.SessionSnapshotID
	if _, present := existing.Plan.RuntimeProfile.Env.Public["SESSION_PIN"]; present {
		t.Fatal("fixture already contains SESSION_PIN")
	}

	plan := planProfileEnvironmentChange(t, service, projection, "SESSION_PIN", "next")
	if _, err := service.Apply(context.Background(), configurationApplyRequest(plan)); err != nil {
		t.Fatal(err)
	}
	if existing.SessionSnapshotID != existingID {
		t.Fatalf("existing snapshot changed %q -> %q", existingID, existing.SessionSnapshotID)
	}
	if _, present := existing.Plan.RuntimeProfile.Env.Public["SESSION_PIN"]; present {
		t.Fatal("existing session profile was mutated in place")
	}

	afterPlan, err := core.PlanRun(RunPlanOptions{
		ProfileName: "default", Backend: "native", Workspace: workspace,
		Command: []string{"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	next, err := core.BeginRunSession(
		afterPlan,
		RunEnvironment{},
		RunSessionOptions{ExplainOnly: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := core.CloseRunSession(next); err != nil {
			t.Errorf("close next run session: %v", err)
		}
	}()
	if next.SessionSnapshotID == existingID ||
		next.Plan.RuntimeProfile.Env.Public["SESSION_PIN"] != "next" {
		t.Fatalf(
			"next session snapshot=%q env=%q existing=%q",
			next.SessionSnapshotID,
			next.Plan.RuntimeProfile.Env.Public["SESSION_PIN"],
			existingID,
		)
	}
	current, err := store.Load("default")
	if err != nil || current.Env.Public["SESSION_PIN"] != "next" {
		t.Fatalf("committed profile=%+v err=%v", current, err)
	}
}

func newProfileTransactionTestService(
	t *testing.T,
) (*ProfileTransactionService, profile.Store, ProfileProjection) {
	t.Helper()
	store := profile.Store{Root: t.TempDir()}
	if _, err := store.LoadOrInit("default"); err != nil {
		t.Fatal(err)
	}
	core := New(store)
	service := NewProfileTransactionService(core)
	service.now = func() time.Time {
		return time.Date(2026, 7, 29, 14, 0, 0, 0, time.UTC)
	}
	projection, err := (ProfileProjectionService{
		Store: store, Now: service.now,
	}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	return service, store, projection
}

func planProfileEnvironmentChange(
	t *testing.T,
	service *ProfileTransactionService,
	projection ProfileProjection,
	name, value string,
) ConfigurationPlan {
	t.Helper()
	change, err := NewTypedChange(ChangeProfileEnvironment, map[string]any{
		"set": map[string]string{name: value},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(context.Background(), ConfigurationDraft{
		Schema: ConfigurationDraftSchema, Profile: projection.Profile,
		BaseRevision: projection.Revision, ClientNonce: "client-" + name,
		Changes: []TypedChange{change},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.VerifyDigest(); err != nil {
		t.Fatal(err)
	}
	return plan
}

func planProfileNetworkChange(
	t *testing.T,
	service *ProfileTransactionService,
	projection ProfileProjection,
) ConfigurationPlan {
	t.Helper()
	posture, err := NewTypedChange(ChangeNetworkPosture, map[string]any{
		"mode": "proxy",
	})
	if err != nil {
		t.Fatal(err)
	}
	proxyRef, err := NewTypedChange(ChangeNetworkProxyRef, map[string]any{
		"ref": "local-proxy",
	})
	if err != nil {
		t.Fatal(err)
	}
	dns, err := NewTypedChange(ChangeNetworkDNS, map[string]any{
		"mode":     "doh",
		"serverIp": "1.1.1.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := service.Plan(context.Background(), ConfigurationDraft{
		Schema: ConfigurationDraftSchema, Profile: projection.Profile,
		BaseRevision: projection.Revision, ClientNonce: "client-network",
		Changes: []TypedChange{posture, proxyRef, dns},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.VerifyDigest(); err != nil {
		t.Fatal(err)
	}
	return plan
}

func configurationApplyRequest(plan ConfigurationPlan) ConfigurationApplyRequest {
	return ConfigurationApplyRequest{
		Schema: ConfigurationApplySchema, OperationID: plan.OperationID,
		Profile: plan.Profile, BaseRevision: plan.BaseRevision,
		PlanDigest: plan.PlanDigest, Confirmed: true,
	}
}

func failProfileTransactionHookOnce(failure error) func(Operation) error {
	var failed atomic.Bool
	return func(Operation) error {
		if failed.CompareAndSwap(false, true) {
			return failure
		}
		return nil
	}
}

type profileTransactionObserver struct {
	mu     sync.Mutex
	events []string
}

func (observer *profileTransactionObserver) OperationEvent(
	kind, phase string,
	details map[string]any,
) {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	observer.events = append(
		observer.events,
		kind+":"+phase+":"+strings.TrimSpace(fmt.Sprint(details["id"])),
	)
}

func (observer *profileTransactionObserver) terminalCount(operationID string) int {
	observer.mu.Lock()
	defer observer.mu.Unlock()
	count := 0
	for _, event := range observer.events {
		if strings.HasSuffix(event, ":"+operationID) &&
			strings.HasPrefix(event, "operation:"+OperationSucceeded+":") {
			count++
		}
	}
	return count
}
