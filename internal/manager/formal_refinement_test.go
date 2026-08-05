package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/sessionwire"
	workloadcoverage "github.com/vibe-agi/hideout/internal/workloadobs/coverage"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const formalSchemaBaseURL = "https://hideout.local/schemas/"

type feature045FormalConstants struct {
	Schema              string   `json:"schema"`
	Clients             []string `json:"clients"`
	Profiles            []string `json:"profiles"`
	Values              []string `json:"values"`
	OperationIDs        []string `json:"operationIds"`
	Secrets             []string `json:"secrets"`
	Connections         []string `json:"connections"`
	Owners              []string `json:"owners"`
	Processes           []string `json:"processes"`
	MaxRevision         uint64   `json:"maxRevision"`
	MaxSecretGeneration uint64   `json:"maxSecretGeneration"`
	MaxSequence         uint64   `json:"maxSequence"`
	LivenessMaxSequence uint64   `json:"livenessMaxSequence"`
	MaxRecords          uint64   `json:"maxRecords"`
	MaxRetries          uint64   `json:"maxRetries"`
}

func TestFormalFoundationConfigurationsMatchSharedConstantsAndInvariants(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, "formal", "cfg", "shared-constants.json"))
	if err != nil {
		t.Fatal(err)
	}
	var constants feature045FormalConstants
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&constants); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("shared constants have trailing JSON: %v", err)
	}
	expected := feature045FormalConstants{
		Schema:  "hideout.formal.feature-045-constants/v1",
		Clients: []string{"client_a", "client_b"}, Profiles: []string{"profile_a"},
		Values:       []string{"value_a", "value_b"},
		OperationIDs: []string{"operation_a", "operation_b"},
		Secrets:      []string{"secret_a", "secret_b"},
		Connections:  []string{"connection_a", "connection_b"},
		Owners:       []string{"reusable_environment", "disposable_session"},
		Processes:    []string{"parent", "reused_pid"},
		MaxRevision:  2, MaxSecretGeneration: 2, MaxSequence: 2,
		LivenessMaxSequence: 1, MaxRecords: 1, MaxRetries: 2,
	}
	if !reflect.DeepEqual(constants, expected) {
		t.Fatalf("formal shared constants drifted:\ngot  %+v\nwant %+v", constants, expected)
	}

	assertFormalConfig(t, root, "OperatorConfiguration", []string{
		"SPECIFICATION SafetySpec",
		"Clients = {client_a, client_b}",
		"Profiles = {profile_a}",
		"Values = {value_a, value_b}",
		"OperationIDs = {operation_a, operation_b}",
		"MaxRevision = 2",
		"MaxRetries = 2",
		"INVARIANT StalePlanNeverCommits",
		"INVARIANT CommittedOperationMatchesBinding",
		"INVARIANT OperationBindingUnique",
		"INVARIANT AtMostOneEffectAndRollback",
		"INVARIANT EffectHasAuthoritativeOutcome",
		"INVARIANT RollbackNeverPublishesSuccess",
		"INVARIANT ExclusiveProfileClaim",
		"INVARIANT ClaimsBelongToLiveDaemon",
		"INVARIANT MismatchNeverChangesBinding",
	})
	assertFormalConfigForModule(t, root,
		"OperatorConfigurationLiveness",
		"OperatorConfiguration",
		[]string{
			"SPECIFICATION Spec",
			"Clients = {client_a}",
			"OperationIDs = {operation_a}",
			"MaxRevision = 2",
			"MaxRetries = 2",
			"PROPERTY PlanEventuallyTerminal",
			"PROPERTY EveryClaimEventuallyReleased",
			"PROPERTY CrashEventuallyRestarts",
			"PROPERTY RollbackEventuallyTerminal",
			"PROPERTY TerminalResponseEventuallyDelivered",
		},
	)
	assertFormalConfigForModule(t, root,
		"RequestWorkflowLiveness",
		"RequestWorkflow",
		[]string{
			"SPECIFICATION Spec",
			"Requests = {request_a}",
			"Clients = {cli, tui}",
			"MaxTime = 2",
			"PROPERTY RequestEventuallyTerminal",
			"PROPERTY EveryClaimEventuallyReleased",
			"PROPERTY DisconnectedClaimEventuallyReleased",
			"PROPERTY CrashEventuallyRestarts",
			"PROPERTY EndedSessionEventuallyClean",
		},
	)
	assertFormalConfig(t, root, "SecretTransition", []string{
		"Clients = {client_a, client_b}",
		"OperationIDs = {operation_a, operation_b}",
		"Secrets = {secret_a, secret_b}",
		"Connections = {connection_a, connection_b}",
		"MaxSecretGeneration = 2",
		"MaxRetries = 2",
		"INVARIANT ActiveAndConnectedSecretsRemainAvailable",
		"INVARIANT ActivationRequiresSuccessfulProbe",
		"INVARIANT ProviderCommitRequiresCompleteRouteProofs",
		"INVARIANT NetworkAuthorityResetClosesConnections",
		"INVARIANT ResetRecoveryIsExact",
		"INVARIANT AtMostOneActivationAndRollback",
		"INVARIANT RollbackRestoresPreviousRoute",
		"PROPERTY ExistingConnectionBindingPreserved",
		"PROPERTY TransitionEventuallySettled",
		"PROPERTY ExactResetRecoveryEventuallyTerminal",
		"PROPERTY SecretResponseEventuallyDelivered",
	})
	assertFormalConfig(t, root, "WorkloadObservation", []string{
		"SPECIFICATION SafetySpec",
		"Owners = {reusable_environment, disposable_session}",
		"Processes = {parent, reused_pid}",
		"MaxSequence = 2",
		"MaxRecords = 1",
		"INVARIANT OwnerIsolation",
		"INVARIANT NoFalseAvailableCoverage",
		"INVARIANT KnownLossIsExplicit",
		"INVARIANT RetentionGapIsExplicit",
		"INVARIANT RelayReceiptNeverLeadsAdmission",
		"INVARIANT GracefulDrainIsComplete",
		"INVARIANT ForcedCloseIsExplicit",
		"INVARIANT SessionCompletionRequiresPersistedTerminalReceipt",
		"INVARIANT CleanupCompletionRequiresAbsence",
	})
	assertFormalConfigForModule(
		t,
		root,
		"WorkloadObservationLiveness",
		"WorkloadObservation",
		[]string{
			"SPECIFICATION Spec",
			"Owners = {reusable_environment, disposable_session}",
			"Processes = {parent, reused_pid}",
			"MaxSequence = 1",
			"MaxRecords = 1",
			"INVARIANT OwnerIsolation",
			"INVARIANT NoFalseAvailableCoverage",
			"INVARIANT KnownLossIsExplicit",
			"INVARIANT RetentionGapIsExplicit",
			"INVARIANT RelayReceiptNeverLeadsAdmission",
			"INVARIANT GracefulDrainIsComplete",
			"INVARIANT ForcedCloseIsExplicit",
			"INVARIANT SessionCompletionRequiresPersistedTerminalReceipt",
			"INVARIANT CleanupCompletionRequiresAbsence",
			"PROPERTY ExactOwnerPrune",
			"PROPERTY ExactOwnerCleanup",
			"PROPERTY RetentionEventuallyCompletes",
			"PROPERTY CleanupEventuallyCompletes",
			"PROPERTY TargetExitEventuallyPersistsAndCompletes",
		},
	)
}

func TestOperatorConfigurationProductionTraceRefinesFoundationModel(t *testing.T) {
	profileStore := profile.Store{Root: t.TempDir()}
	if err := profileStore.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	projections := ProfileProjectionService{Store: profileStore, Now: func() time.Time {
		now = now.Add(time.Second)
		return now
	}}
	initial, err := projections.Load("default")
	if err != nil {
		t.Fatal(err)
	}

	operations := OperationStore{Root: t.TempDir(), Now: func() time.Time {
		now = now.Add(time.Second)
		return now
	}}
	staleBinding := OperationBinding{
		ID: "op_formalstale01", Kind: "profile.transaction",
		Owner:      OperationOwner{Kind: "profile", ID: "default"},
		PlanDigest: formalDigest("a"), BaseRevision: initial.Revision,
	}
	if _, created, err := operations.Reserve(staleBinding, nil); err != nil || !created {
		t.Fatalf("CreatePlan refinement: created=%t err=%v", created, err)
	}

	edited, err := profileStore.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	edited.Identity.Hostname = "external-edit"
	if err := profileStore.Save(edited); err != nil {
		t.Fatal(err)
	}
	current, err := projections.Load("default")
	if err != nil {
		t.Fatal(err)
	}
	if current.Revision != initial.Revision+1 {
		t.Fatalf("ExternalEdit refinement revision=%d want %d", current.Revision, initial.Revision+1)
	}
	if err := projections.CheckCAS("default", initial.Revision, initial.ContentDigest); !errors.Is(err, ErrStaleProfileRevision) {
		t.Fatalf("RejectStale refinement error=%v", err)
	}

	if replay, created, err := operations.Reserve(staleBinding, nil); err != nil || created || replay.ID != staleBinding.ID {
		t.Fatalf("RetryExact reservation refinement: replay=%+v created=%t err=%v", replay, created, err)
	}
	mismatch := staleBinding
	mismatch.PlanDigest = formalDigest("b")
	if _, _, err := operations.Reserve(mismatch, nil); !errors.Is(err, ErrOperationMismatch) {
		t.Fatalf("RetryMismatch refinement error=%v", err)
	}

	successBinding := OperationBinding{
		ID: "op_formalsuccess1", Kind: "profile.transaction",
		Owner:      OperationOwner{Kind: "profile", ID: "default"},
		PlanDigest: formalDigest("c"), BaseRevision: current.Revision,
	}
	effects := []EffectResult{{
		ID: "persist-profile", Kind: "persist", Provider: "profile-store", Status: EffectPending,
	}}
	if _, created, err := operations.Reserve(successBinding, effects); err != nil || !created {
		t.Fatalf("successful CreatePlan refinement: created=%t err=%v", created, err)
	}
	for _, phase := range []string{OperationClaimed, OperationStaging} {
		if _, err := operations.Transition(successBinding.ID, phase, nil); err != nil {
			t.Fatalf("transition %s: %v", phase, err)
		}
	}
	if _, execute, err := operations.BeginEffect(successBinding.ID, "persist-profile", "profile-store"); err != nil || !execute {
		t.Fatalf("first effect claim: execute=%t err=%v", execute, err)
	}
	if _, execute, err := operations.BeginEffect(successBinding.ID, "persist-profile", "profile-store"); err != nil || execute {
		t.Fatalf("AtMostOneEffectPerOperation refinement: execute=%t err=%v", execute, err)
	}
	if _, err := operations.FinishEffect(
		successBinding.ID,
		"persist-profile",
		"profile-store",
		EffectSucceeded,
		[]EvidenceRef{{Code: "profile-committed", Ref: "profile:default"}},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.Transition(successBinding.ID, OperationSucceeded, &OperationResult{
		Status: OperationSucceeded, Summary: "profile updated",
	}); err != nil {
		t.Fatal(err)
	}
	replay, created, err := operations.Reserve(successBinding, effects)
	if err != nil || created || replay.Phase != OperationSucceeded {
		t.Fatalf("response-loss RetryExact refinement: replay=%+v created=%t err=%v", replay, created, err)
	}
}

func TestWorkloadObservationProductionTypesRefineFoundationModel(t *testing.T) {
	reusable, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	disposable, err := workloadtypes.NewDisposableOwner("ses_disposable", "lima", "incarnation-b")
	if err != nil {
		t.Fatal(err)
	}
	firstExecution, err := workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner: reusable, SessionID: "ses_fixture", GuestBootID: "boot-a",
		ObserverGeneration: 1, PID: 100, ExecSequence: 1, StartedAtMonoNS: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	reusedPIDExecution, err := workloadtypes.NewExecutionID(workloadtypes.ExecutionIdentityInput{
		Owner: reusable, SessionID: "ses_fixture", GuestBootID: "boot-a",
		ObserverGeneration: 1, PID: 100, ExecSequence: 2, StartedAtMonoNS: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if firstExecution == reusedPIDExecution {
		t.Fatal("PID reuse collapsed two execution identities")
	}

	record := formalActivityRecord(reusable)
	if err := record.ValidatePersistable(); err != nil {
		t.Fatal(err)
	}
	if err := record.ValidateForOwner(disposable); !errors.Is(err, workloadtypes.ErrOwnerMismatch) {
		t.Fatalf("OwnerIsolation refinement error=%v", err)
	}

	timeline, err := workloadcoverage.NewTimeline(reusable, "ses_fixture", workloadtypes.SubsystemProcess)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 7, 29, 10, 30, 0, 0, time.UTC)
	if _, err := timeline.Transition(workloadcoverage.Transition{
		State: workloadtypes.CoverageAvailable, Reason: workloadcoverage.ReasonObserverReady,
		CollectorGeneration: 1, Sequence: 1, At: start,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := timeline.MarkLoss(workloadcoverage.ReasonSequenceGap, 1, 2, start.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	current, err := timeline.Current()
	if err != nil {
		t.Fatal(err)
	}
	if current.State == workloadtypes.CoverageAvailable || current.DroppedEventCount == 0 {
		t.Fatalf("NoFalseAvailableCoverage refinement failed: %+v", current)
	}
	if _, err := timeline.Transition(workloadcoverage.Transition{
		State: workloadtypes.CoverageAvailable, Reason: workloadcoverage.ReasonCollectorRecovered,
		CollectorGeneration: 1, Sequence: 3, At: start.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	current, err = timeline.Current()
	if err != nil {
		t.Fatal(err)
	}
	intervals := timeline.Intervals(time.Time{}, time.Time{})
	if current.State != workloadtypes.CoverageAvailable ||
		len(intervals) != 3 ||
		intervals[1].State != workloadtypes.CoveragePartial ||
		intervals[1].DroppedEventCount != 1 ||
		intervals[1].EndedAt == nil {
		t.Fatalf("KnownLossIsExplicit refinement lost history: current=%+v intervals=%+v", current, intervals)
	}
	if _, err := timeline.MarkRetentionGap(
		workloadcoverage.ReasonRetentionPruned,
		4,
		start.Add(3*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	current, err = timeline.Current()
	if err != nil {
		t.Fatal(err)
	}
	intervals = timeline.Intervals(time.Time{}, time.Time{})
	if current.State != workloadtypes.CoveragePartial ||
		!current.RetentionGap ||
		current.Reason != workloadcoverage.ReasonRetentionPruned ||
		len(intervals) != 4 ||
		intervals[1].State != workloadtypes.CoveragePartial ||
		intervals[1].DroppedEventCount != 1 {
		t.Fatalf("RetentionGapIsExplicit refinement failed: current=%+v intervals=%+v", current, intervals)
	}

	recordsByOwner := map[string][]workloadtypes.ActivityRecord{
		reusable.Key():   {record},
		disposable.Key(): {formalActivityRecord(disposable)},
	}
	otherBefore := recordsByOwner[disposable.Key()][0]
	delete(recordsByOwner, reusable.Key())
	if _, exists := recordsByOwner[reusable.Key()]; exists ||
		len(recordsByOwner[disposable.Key()]) != 1 ||
		recordsByOwner[disposable.Key()][0].ID != otherBefore.ID {
		t.Fatalf("ExactOwnerCleanup refinement crossed owners: %+v", recordsByOwner)
	}

	queue, err := sessionwire.NewObserverQueueWithByteLimit(2, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer queue.Close()
	firstFrame := []byte("first-admitted-frame")
	secondFrame := []byte("second-admitted-frame")
	if err := queue.EnqueueWait(firstFrame, nil); err != nil {
		t.Fatal(err)
	}
	if err := queue.EnqueueWait(secondFrame, nil); err != nil {
		t.Fatal(err)
	}
	queue.Seal()
	if err := queue.Enqueue([]byte("post-seal-frame")); !errors.Is(err, sessionwire.ErrObserverQueueClosed) {
		t.Fatalf("sealed observer queue accepted a new frame: %v", err)
	}
	for index, expected := range [][]byte{firstFrame, secondFrame} {
		actual, ok := queue.Dequeue()
		if !ok || !bytes.Equal(actual, expected) {
			t.Fatalf(
				"graceful observer drain frame %d = %q, %t; want %q",
				index,
				actual,
				ok,
				expected,
			)
		}
	}
	if tail, ok := queue.Dequeue(); ok {
		t.Fatalf("graceful observer drain retained an unexpected tail: %q", tail)
	}
	if !queue.SealedAndDrained() {
		t.Fatal("graceful observer drain lacks an exact sealed receipt")
	}
}

func TestFormalGoFixturesConformToPublishedSchemas(t *testing.T) {
	profileStore := profile.Store{Root: t.TempDir()}
	if err := profileStore.Create(profile.Default("default")); err != nil {
		t.Fatal(err)
	}
	projection, err := (ProfileProjectionService{Store: profileStore}).Load("default")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC)
	operation := Operation{
		Schema: OperationSchema, ID: "op_schemafixture1", Kind: "profile.transaction",
		Owner:      OperationOwner{Kind: "profile", ID: "default"},
		PlanDigest: formalDigest("d"), BaseRevision: 1, Phase: OperationPlanned,
		Effects: []EffectResult{{
			ID: "persist-profile", Kind: "persist", Provider: "profile-store", Status: EffectPending,
		}},
		Recovery:  Recovery{Code: "none", Summary: "no recovery required"},
		CreatedAt: now, UpdatedAt: now,
	}
	owner, err := workloadtypes.NewReusableOwner("env_fixture", "lima", "incarnation-a")
	if err != nil {
		t.Fatal(err)
	}
	coverage := workloadtypes.CoverageInterval{
		Schema: workloadtypes.CoverageIntervalSchema, ID: "cov_schemafixture1",
		Owner: owner, SessionID: "ses_fixture", Subsystem: workloadtypes.SubsystemProcess,
		State: workloadtypes.CoverageAvailable, Reason: workloadcoverage.ReasonObserverReady,
		CollectorGeneration: 1, StartedAt: now,
	}
	fixtures := []struct {
		schema string
		value  any
	}{
		{"profile-projection.schema.json", projection},
		{"operation.schema.json", operation},
		{"activity-owner.schema.json", owner},
		{"activity-record.schema.json", formalActivityRecord(owner)},
		{"coverage-interval.schema.json", coverage},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.schema, func(t *testing.T) {
			if err := validateFormalSchemaFixture(t, fixture.schema, fixture.value); err != nil {
				t.Fatalf("Go fixture does not conform to %s: %v", fixture.schema, err)
			}
		})
	}
}

func assertFormalConfig(t *testing.T, root, model string, required []string) {
	t.Helper()
	assertFormalConfigForModule(t, root, model, model, required)
}

func assertFormalConfigForModule(
	t *testing.T,
	root string,
	configName string,
	moduleName string,
	required []string,
) {
	t.Helper()
	config, err := os.ReadFile(
		filepath.Join(root, "formal", "cfg", configName+".cfg"),
	)
	if err != nil {
		t.Fatal(err)
	}
	module, err := os.ReadFile(
		filepath.Join(root, "formal", moduleName+".tla"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(config, []byte("Placeholder")) {
		t.Fatalf(
			"%s configuration is still marked as a placeholder",
			configName,
		)
	}
	for _, text := range required {
		if !bytes.Contains(config, []byte(text)) {
			t.Fatalf("%s configuration is missing %q", configName, text)
		}
		if strings.HasPrefix(text, "INVARIANT ") ||
			strings.HasPrefix(text, "PROPERTY ") {
			name := strings.TrimPrefix(text, "INVARIANT ")
			name = strings.TrimPrefix(name, "PROPERTY ")
			if !bytes.Contains(module, []byte(name+" ==")) {
				t.Fatalf(
					"%s module is missing property definition %s",
					moduleName,
					name,
				)
			}
		}
	}
	if !bytes.Contains(module, []byte("Spec == Init /\\ [][Next]_vars")) {
		t.Fatalf(
			"%s module is missing the state-machine specification",
			moduleName,
		)
	}
}

func formalActivityRecord(owner workloadtypes.ActivityOwner) workloadtypes.ActivityRecord {
	now := time.Date(2026, 7, 29, 10, 30, 0, 0, time.UTC)
	id := "act_reusable001"
	sessionID := "ses_fixture"
	if owner.Kind == workloadtypes.OwnerDisposableSession {
		id = "act_disposable01"
		sessionID = owner.SessionID
	}
	return workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema, ID: id, Owner: owner,
		SessionID: sessionID, Kind: workloadtypes.ActivityRisk, Operation: "risk.detected",
		Subject: workloadtypes.GenericSubject{
			Kind: workloadtypes.ActivityRisk, Code: "fixture",
		},
		Outcome: workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded},
		Count:   1, FirstAt: now, LastAt: now, FirstSequence: 1, LastSequence: 1,
		Attribution: workloadtypes.AttributionExact, CoverageID: "cov_schemafixture1",
		RedactionStatus: workloadtypes.RedactionPassed,
	}
}

func formalDigest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}

func validateFormalSchemaFixture(t *testing.T, name string, value any) error {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	paths, err := filepath.Glob(filepath.Join("..", "..", "schemas", "*.schema.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return err
		}
		if err := compiler.AddResource(formalSchemaBaseURL+filepath.Base(path), document); err != nil {
			return err
		}
	}
	schema, err := compiler.Compile(formalSchemaBaseURL + name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return schema.Validate(document)
}
