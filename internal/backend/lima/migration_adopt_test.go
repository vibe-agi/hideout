package lima

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

func TestAdoptMigrationDestinationRunsZeroNetworkExecutorAndReplaysEvidence(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	stageRequest, _, _ := migrationDestinationStageFixture(t, fixture, "adopt")
	stageRequest.Binding.OperationID = "op_migrationadopt1"
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), stageRequest)
	if err != nil {
		t.Fatal(err)
	}
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	runner := &migrationAdoptionRunner{version: "limactl version 2.2.0\n"}
	fixture.provider.Runner = runner
	fixture.provider.Migration.AdoptionExecutorPath = executor

	request := migrationDestinationAdoptionFixture(
		t, fixture.provider, stageRequest, stage, migration.GuestIdentitySafeClone,
	)
	result, err := fixture.provider.AdoptMigrationDestination(context.Background(), request)
	if err != nil {
		var providerErr *backend.MigrationProviderError
		if errors.As(err, &providerErr) {
			t.Fatalf("adoption error=%v cause=%v", err, providerErr.Cause)
		}
		t.Fatal(err)
	}
	if err := result.MatchesRequest(request); err != nil {
		t.Fatal(err)
	}
	if result.Receipt.PostIdentity == nil ||
		result.Receipt.PostIdentity.Equal(request.SourceIdentity) ||
		!result.Stopped || !result.TemporaryAuthorityRemoved || runner.executions != 1 {
		t.Fatalf("adoption result=%+v executions=%d", result, runner.executions)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(stage.StageHandle),
	)
	control := filepath.Join(
		stageDir, "adoption", string(request.EnvironmentRef), "control",
	)
	if _, err := os.Lstat(control); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary adoption control remains: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(
		stageDir, migrationAdoptionEvidenceRelativePath(request.EnvironmentRef),
	)); err != nil {
		t.Fatalf("durable adoption evidence missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(
		fixture.home, string(stageRequest.Objects[0].BackendIdentity),
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("adoption activated a top-level Lima instance: %v", err)
	}

	replayed, err := fixture.provider.AdoptMigrationDestination(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result, replayed) || runner.executions != 1 {
		t.Fatalf("adoption replay=%+v first=%+v executions=%d", replayed, result, runner.executions)
	}
}

func TestAdoptMigrationDestinationRejectsExecutorNetworkMutationWithoutEvidence(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	stageRequest, _, _ := migrationDestinationStageFixture(t, fixture, "network")
	stageRequest.Binding.OperationID = "op_migrationnetwork1"
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), stageRequest)
	if err != nil {
		t.Fatal(err)
	}
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	runner := &migrationAdoptionRunner{
		version: "limactl version 2.2.0\n", responseNetworkDevices: 1,
	}
	fixture.provider.Runner = runner
	fixture.provider.Migration.AdoptionExecutorPath = executor
	request := migrationDestinationAdoptionFixture(
		t, fixture.provider, stageRequest, stage, migration.GuestIdentitySafeClone,
	)
	_, err = fixture.provider.AdoptMigrationDestination(context.Background(), request)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.adoption_executor_invalid" ||
		!providerErr.RecoveryRequired {
		t.Fatalf("network mutation error=%v", err)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(stage.StageHandle),
	)
	if _, err := os.Lstat(filepath.Join(
		stageDir, migrationAdoptionEvidenceRelativePath(request.EnvironmentRef),
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("network-enabled adoption wrote evidence: %v", err)
	}
}

func TestAdoptMigrationDestinationRecoversDurableStoppedResponseWithoutSecondBoot(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	stageRequest, _, _ := migrationDestinationStageFixture(t, fixture, "recover")
	stageRequest.Binding.OperationID = "op_migrationrecover1"
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), stageRequest)
	if err != nil {
		t.Fatal(err)
	}
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	runner := &migrationAdoptionRunner{
		version: "limactl version 2.2.0\n", leaveUnexpectedControl: true,
	}
	fixture.provider.Runner = runner
	fixture.provider.Migration.AdoptionExecutorPath = executor
	request := migrationDestinationAdoptionFixture(
		t, fixture.provider, stageRequest, stage, migration.GuestIdentitySafeClone,
	)
	_, err = fixture.provider.AdoptMigrationDestination(context.Background(), request)
	var providerErr *backend.MigrationProviderError
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.adoption_channel_removal_failed" ||
		!providerErr.RecoveryRequired || runner.executions != 1 {
		t.Fatalf("first recovery error=%v executions=%d", err, runner.executions)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(stage.StageHandle),
	)
	unexpected := filepath.Join(
		stageDir, "adoption", string(request.EnvironmentRef), "control", "unexpected",
	)
	if err := os.Remove(unexpected); err != nil {
		t.Fatal(err)
	}
	runner.leaveUnexpectedControl = false
	recovered, err := fixture.provider.AdoptMigrationDestination(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := recovered.MatchesRequest(request); err != nil {
		t.Fatal(err)
	}
	if runner.executions != 1 {
		t.Fatalf("recovery repeated adoption boot: executions=%d", runner.executions)
	}
}

func TestAdoptMigrationDestinationExactRestorePreservesReceiptIdentity(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	stageRequest, _, _ := migrationDestinationStageFixture(t, fixture, "exact")
	stageRequest.Binding.OperationID = "op_migrationexact1"
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), stageRequest)
	if err != nil {
		t.Fatal(err)
	}
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	runner := &migrationAdoptionRunner{version: "limactl version 2.2.0\n"}
	fixture.provider.Runner = runner
	fixture.provider.Migration.AdoptionExecutorPath = executor
	request := migrationDestinationAdoptionFixture(
		t, fixture.provider, stageRequest, stage, migration.GuestIdentityExactRestore,
	)
	result, err := fixture.provider.AdoptMigrationDestination(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Receipt.PostIdentity == nil ||
		!result.Receipt.PostIdentity.Equal(request.SourceIdentity) {
		t.Fatalf("Exact Guest Restore result=%+v", result)
	}
}

func migrationDestinationAdoptionFixture(
	t *testing.T,
	provider Backend,
	stageRequest backend.DestinationStageRequest,
	stage backend.DestinationStage,
	policy migration.GuestIdentityPolicy,
) backend.DestinationAdoptionRequest {
	t.Helper()
	capability, err := provider.MigrationCapabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if capability.AdoptionHelper == nil {
		t.Fatal("fixture capability lacks adoption helper")
	}
	return backend.DestinationAdoptionRequest{
		Binding: backend.MigrationEffectBinding{
			OperationID:        stageRequest.Binding.OperationID,
			EffectID:           "effect_adoption1234",
			CapabilityRevision: stageRequest.Binding.CapabilityRevision,
		},
		StageHandle:    stage.StageHandle,
		EnvironmentRef: stageRequest.Objects[0].EnvironmentRef,
		Policy:         policy,
		SourceIdentity: migration.GuestIdentityEvidence{
			MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("a", 64)),
			SSHHostKeyDigests: []migration.Digest{
				migration.Digest("sha256:" + strings.Repeat("b", 64)),
			},
		},
		Helper: migration.HelperBinding{
			PackageID: capability.AdoptionHelper.PackageID,
			Version:   capability.AdoptionHelper.Version,
			SHA256:    capability.AdoptionHelper.Digest,
		},
	}
}

type migrationAdoptionRunner struct {
	version                string
	executions             int
	responseNetworkDevices uint8
	leaveUnexpectedControl bool
}

func (runner *migrationAdoptionRunner) LookPath(string) (string, error) {
	return "/opt/homebrew/bin/limactl", nil
}

func (runner *migrationAdoptionRunner) Run(
	_ context.Context,
	_ string,
	args []string,
	_ []string,
	stdin io.Reader,
	stdout,
	_ io.Writer,
) error {
	if reflect.DeepEqual(args, []string{"--version"}) {
		_, err := io.WriteString(stdout, runner.version)
		return err
	}
	if reflect.DeepEqual(args, []string{"--probe"}) {
		return json.NewEncoder(stdout).Encode(vzexecutor.CurrentProbe())
	}
	if len(args) != 0 {
		return errors.New("unexpected adoption executor arguments")
	}
	runner.executions++
	var execution vzexecutor.ExecutionRequest
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&execution); err != nil {
		return err
	}
	paths, err := execution.Paths()
	if err != nil {
		return err
	}
	var guestRequest migration.AdoptionRequest
	requestData, err := os.ReadFile(paths.GuestRequest)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(requestData, &guestRequest); err != nil {
		return err
	}
	if guestRequest.Policy == migration.GuestIdentitySafeClone {
		root, err := os.OpenFile(paths.RootDisk, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		_, writeErr := root.WriteAt([]byte{0x7f}, 0)
		syncErr := root.Sync()
		closeErr := root.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return err
		}
	}
	actions := make([]migration.AdoptionActionResult, len(guestRequest.PermittedActions))
	for index, action := range guestRequest.PermittedActions {
		actions[index] = migration.AdoptionActionResult{
			Action: action, Status: migration.AdoptionActionStatusCompleted,
		}
	}
	postIdentity := migration.GuestIdentityEvidence{
		MachineIDDigest: migration.Digest("sha256:" + strings.Repeat("c", 64)),
		SSHHostKeyDigests: []migration.Digest{
			migration.Digest("sha256:" + strings.Repeat("d", 64)),
		},
	}
	if guestRequest.Policy == migration.GuestIdentityExactRestore {
		postIdentity = guestRequest.SourceIdentity
	}
	receipt := migration.AdoptionReceipt{
		Schema:      migration.AdoptionReceiptSchema,
		OperationID: guestRequest.OperationID, EnvironmentRef: guestRequest.EnvironmentRef,
		RequestNonce: guestRequest.RequestNonce, ReceiptNonce: guestRequest.ReceiptNonce,
		Policy: guestRequest.Policy, Helper: guestRequest.Helper,
		ActionResults: actions, PostIdentity: &postIdentity,
		Status: migration.AdoptionReceiptStatusCompleted, CompletionMarker: true,
	}
	if err := writeMigrationAdoptionJSONFixture(paths.GuestReceipt, receipt, 0o600); err != nil {
		return err
	}
	response := vzexecutor.ExecutionResponse{
		Schema: vzexecutor.ExecutionResponseSchema, ExecutionNonce: execution.ExecutionNonce,
		Started: true, Stopped: true,
		NetworkDeviceCount: runner.responseNetworkDevices,
		ReceiptObserved:    true, StopReason: vzexecutor.StopReasonReceiptAndGuestShutdown,
	}
	proof, err := response.ExpectedShutdownProof()
	if err != nil {
		return err
	}
	response.ShutdownProof = proof
	if err := writeMigrationAdoptionJSONFixture(
		paths.ExecutorResponse, response, 0o600,
	); err != nil {
		return err
	}
	if runner.leaveUnexpectedControl {
		if err := os.WriteFile(
			filepath.Join(paths.Control, "unexpected"), []byte("keep"), 0o600,
		); err != nil {
			return err
		}
	}
	return json.NewEncoder(stdout).Encode(response)
}

func writeMigrationAdoptionJSONFixture(path string, value any, mode os.FileMode) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, mode)
}
