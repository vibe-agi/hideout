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
	if len(runner.executionRequests) != 1 ||
		len(runner.executionRequests[0].AttachedDisks) != 1 {
		t.Fatalf("adoption execution requests=%+v", runner.executionRequests)
	}
	executionDisk := runner.executionRequests[0].AttachedDisks[0]
	binding := request.MountBindings[0]
	handle := strings.TrimPrefix(binding.DestinationGuestPath, "/mnt/lima-")
	if executionDisk.DiskID != binding.DiskID ||
		executionDisk.RelativePath != filepath.Join("disks", handle, "datadisk") ||
		executionDisk.GuestMountPath != binding.DestinationGuestPath ||
		executionDisk.FSType != binding.FSType {
		t.Fatalf("attached disk execution=%+v binding=%+v", executionDisk, binding)
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

func TestAdoptMigrationDestinationPreservesBoundGuestFailureWithoutEvidence(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	stageRequest, _, _ := migrationDestinationStageFixture(t, fixture, "guestfail")
	stageRequest.Binding.OperationID = "op_migrationguestfail1"
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), stageRequest)
	if err != nil {
		t.Fatal(err)
	}
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	runner := &migrationAdoptionRunner{
		version:          "limactl version 2.2.0\n",
		guestFailureCode: "migration.adoption.disk_mount_rebind_failed",
	}
	fixture.provider.Runner = runner
	fixture.provider.Migration.AdoptionExecutorPath = executor
	request := migrationDestinationAdoptionFixture(
		t, fixture.provider, stageRequest, stage, migration.GuestIdentitySafeClone,
	)

	for attempt := 1; attempt <= 2; attempt++ {
		_, err = fixture.provider.AdoptMigrationDestination(context.Background(), request)
		var providerErr *backend.MigrationProviderError
		if !errors.As(err, &providerErr) ||
			providerErr.Code != "migration.provider.adoption_guest_failed" ||
			!providerErr.RecoveryRequired || providerErr.Cause == nil ||
			providerErr.Cause.Error() != runner.guestFailureCode {
			t.Fatalf("attempt %d guest failure=%v provider=%+v", attempt, err, providerErr)
		}
	}
	if runner.executions != 1 {
		t.Fatalf("durable guest failure repeated boot: executions=%d", runner.executions)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(stage.StageHandle),
	)
	control := filepath.Join(
		stageDir, "adoption", string(request.EnvironmentRef), "control",
	)
	if info, err := os.Lstat(control); err != nil || !info.IsDir() {
		t.Fatalf("failed guest control was not retained: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(
		stageDir, migrationAdoptionEvidenceRelativePath(request.EnvironmentRef),
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed guest adoption wrote evidence: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(
		fixture.home, string(stageRequest.Objects[0].BackendIdentity),
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed guest adoption materialized authority: %v", err)
	}
}

func TestAdoptMigrationDestinationRejectsAttachedDiskMutationWithoutEvidence(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	stageRequest, _, _ := migrationDestinationStageFixture(t, fixture, "diskchange")
	stageRequest.Binding.OperationID = "op_migrationdiskchange1"
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), stageRequest)
	if err != nil {
		t.Fatal(err)
	}
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	runner := &migrationAdoptionRunner{
		version: "limactl version 2.2.0\n", mutateAttachedDisk: true,
	}
	fixture.provider.Runner = runner
	fixture.provider.Migration.AdoptionExecutorPath = executor
	request := migrationDestinationAdoptionFixture(
		t, fixture.provider, stageRequest, stage, migration.GuestIdentitySafeClone,
	)

	for attempt := 1; attempt <= 2; attempt++ {
		_, err = fixture.provider.AdoptMigrationDestination(context.Background(), request)
		var providerErr *backend.MigrationProviderError
		if !errors.As(err, &providerErr) ||
			providerErr.Code != "migration.provider.adoption_attached_disk_changed" ||
			!providerErr.RecoveryRequired {
			t.Fatalf("attempt %d attached mutation error=%v provider=%+v", attempt, err, providerErr)
		}
	}
	if runner.executions != 1 {
		t.Fatalf("durable attached mutation repeated boot: executions=%d", runner.executions)
	}
	stageDir := filepath.Join(
		fixture.home, "_hideout-migration", "stages", string(stage.StageHandle),
	)
	control := filepath.Join(
		stageDir, "adoption", string(request.EnvironmentRef), "control",
	)
	if info, err := os.Lstat(control); err != nil || !info.IsDir() {
		t.Fatalf("attached mutation control was not retained: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(
		stageDir, migrationAdoptionEvidenceRelativePath(request.EnvironmentRef),
	)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attached mutation wrote adoption evidence: %v", err)
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

func TestAdoptMigrationDestinationRefusesReplayWithoutDurableStoppedResponse(t *testing.T) {
	fixture := newMigrationSourceFixture(t, []migrationSourceInstanceFixture{{
		name: "hideout-source", environmentRef: "environment_source1", status: "Stopped",
	}})
	stageRequest, _, _ := migrationDestinationStageFixture(t, fixture, "ambiguous")
	stageRequest.Binding.OperationID = "op_migrationambiguous1"
	stage, err := fixture.provider.StageMigrationDestination(context.Background(), stageRequest)
	if err != nil {
		t.Fatal(err)
	}
	executor := migrationVZAdoptionExecutorFixture(t, "executor-v1")
	runner := &migrationAdoptionRunner{
		version: "limactl version 2.2.0\n", failBeforeResponse: true,
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
		!providerErr.RecoveryRequired || runner.executions != 1 {
		t.Fatalf("first ambiguous adoption error=%v executions=%d", err, runner.executions)
	}

	runner.failBeforeResponse = false
	_, err = fixture.provider.AdoptMigrationDestination(context.Background(), request)
	providerErr = nil
	if !errors.As(err, &providerErr) ||
		providerErr.Code != "migration.provider.adoption_recovery_required" ||
		!providerErr.RecoveryRequired || runner.executions != 1 {
		t.Fatalf("ambiguous replay error=%v executions=%d", err, runner.executions)
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

func TestMigrationAdoptionExecutionAttachedDisksBindsExactStageObject(t *testing.T) {
	configuration := migrationStageConfiguration{AttachedDisks: []migrationStageAttachedDisk{{
		DiskID: "disk_data1234", ObjectHandle: "disk_handle1234",
		SourceGuestPath: "/mnt/lima-source-data", FSType: "ext4",
	}}}
	entries := []migrationStageEntry{{
		DiskID: "disk_data1234", Role: migration.DiskRoleAttached, Format: "raw",
		ObjectHandle: "disk_handle1234",
		RelativePath: filepath.Join("disks", "disk_handle1234", "datadisk"),
	}}
	bindings := []migration.DiskMountBinding{{
		DiskID: "disk_data1234", SourceGuestPath: "/mnt/lima-source-data",
		DestinationGuestPath: "/mnt/lima-disk_handle1234", FSType: "ext4",
	}}
	disks, err := migrationAdoptionExecutionAttachedDisks(configuration, entries, bindings)
	if err != nil || len(disks) != 1 || disks[0].DiskID != bindings[0].DiskID ||
		disks[0].RelativePath != entries[0].RelativePath ||
		disks[0].GuestMountPath != bindings[0].DestinationGuestPath {
		t.Fatalf("execution disks=%+v error=%v", disks, err)
	}

	for name, mutate := range map[string]func(
		*migrationStageConfiguration, []migrationStageEntry, []migration.DiskMountBinding,
	){
		"non-raw stage object": func(_ *migrationStageConfiguration, entries []migrationStageEntry, _ []migration.DiskMountBinding) {
			entries[0].Format = "qcow2"
		},
		"stage path substitution": func(_ *migrationStageConfiguration, entries []migrationStageEntry, _ []migration.DiskMountBinding) {
			entries[0].RelativePath = filepath.Join("instances", "backend_other1234", "disk")
		},
		"mount handle substitution": func(_ *migrationStageConfiguration, _ []migrationStageEntry, bindings []migration.DiskMountBinding) {
			bindings[0].DestinationGuestPath = "/mnt/lima-disk_other1234"
		},
		"filesystem substitution": func(_ *migrationStageConfiguration, _ []migrationStageEntry, bindings []migration.DiskMountBinding) {
			bindings[0].FSType = "xfs"
		},
	} {
		t.Run(name, func(t *testing.T) {
			changedConfiguration := configuration
			changedConfiguration.AttachedDisks = append(
				[]migrationStageAttachedDisk(nil), configuration.AttachedDisks...,
			)
			changedEntries := append([]migrationStageEntry(nil), entries...)
			changedBindings := append([]migration.DiskMountBinding(nil), bindings...)
			mutate(&changedConfiguration, changedEntries, changedBindings)
			if disks, err := migrationAdoptionExecutionAttachedDisks(
				changedConfiguration, changedEntries, changedBindings,
			); err == nil || disks != nil {
				t.Fatalf("mutation accepted disks=%+v", disks)
			}
		})
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
	mountBindings := make([]migration.DiskMountBinding, 0)
	for _, edge := range stageRequest.Edges {
		if edge.EnvironmentRef != stageRequest.Objects[0].EnvironmentRef ||
			edge.Attachment != migration.DiskRoleAttached {
			continue
		}
		mountBindings = append(mountBindings, migration.DiskMountBinding{
			DiskID: edge.DiskID, SourceGuestPath: edge.GuestPath,
			DestinationGuestPath: "/mnt/lima-" + string(
				migrationDestinationDiskHandle(stageRequest, edge.DiskID),
			),
			FSType: edge.FSType,
		})
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
		MountBindings: mountBindings,
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
	failBeforeResponse     bool
	guestFailureCode       string
	mutateAttachedDisk     bool
	executionRequests      []vzexecutor.ExecutionRequest
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
	if runner.failBeforeResponse {
		return errors.New("injected executor exit before durable stopped response")
	}
	var execution vzexecutor.ExecutionRequest
	decoder := json.NewDecoder(stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&execution); err != nil {
		return err
	}
	runner.executionRequests = append(runner.executionRequests, execution)
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
	if runner.mutateAttachedDisk {
		if len(paths.AttachedDisks) == 0 {
			return errors.New("injected attached-disk mutation lacks a disk")
		}
		disk, err := os.OpenFile(paths.AttachedDisks[0].HostPath, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		_, writeErr := disk.WriteAt([]byte{0x6f}, 0)
		syncErr := disk.Sync()
		closeErr := disk.Close()
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
		MountBindings: append(
			[]migration.DiskMountBinding(nil), guestRequest.MountBindings...,
		),
		ActionResults: actions, PostIdentity: &postIdentity,
		Status: migration.AdoptionReceiptStatusCompleted, CompletionMarker: true,
	}
	if runner.guestFailureCode != "" {
		failedActions := make([]migration.AdoptionActionResult, 0, len(actions))
		for _, action := range actions {
			result := action
			if action.Action == migration.AdoptionActionRebindDiskMounts {
				result.Status = migration.AdoptionActionStatusFailed
				result.Code = runner.guestFailureCode
				failedActions = append(failedActions, result)
				break
			}
			failedActions = append(failedActions, result)
		}
		receipt.ActionResults = failedActions
		receipt.PostIdentity = nil
		receipt.Status = migration.AdoptionReceiptStatusFailed
		receipt.CompletionMarker = false
		receipt.FailureCode = runner.guestFailureCode
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
