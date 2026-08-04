package lima

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
	"golang.org/x/crypto/ssh"
)

const (
	migrationAdoptionExecutorTimeout = 6 * time.Minute
	migrationAdoptionDocumentLimit   = 64 << 10
)

func (b Backend) AdoptMigrationDestination(
	ctx context.Context,
	request backend.DestinationAdoptionRequest,
) (backend.DestinationAdoption, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return backend.DestinationAdoption{}, err
	}
	capability, err := b.MigrationCapabilities(ctx)
	if err != nil || capability.Revision != request.Binding.CapabilityRevision ||
		!capability.FullImport || capability.AdoptionHelper == nil {
		if err == nil {
			err = errors.New("destination adoption capability is stale or unavailable")
		}
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_capability_changed", request.Binding,
			request.EnvironmentRef, err, false,
		)
	}
	if request.Helper.PackageID != capability.AdoptionHelper.PackageID ||
		request.Helper.Version != capability.AdoptionHelper.Version ||
		request.Helper.SHA256 != capability.AdoptionHelper.Digest {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_helper_changed", request.Binding,
			request.EnvironmentRef, errors.New("adoption helper binding changed"), false,
		)
	}
	hostOS, hostArch, guestArch := b.migrationArchitectures()
	executor, err := b.resolveMigrationAdoptionExecutor(hostOS, hostArch)
	if err != nil || executor.Path == "" {
		if err == nil {
			err = errors.New("packaged VZ adoption executor is unavailable")
		}
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_executor_unavailable", request.Binding,
			request.EnvironmentRef, err, false,
		)
	}
	helper, err := b.resolveMigrationAdoptionHelper(guestArch)
	if err != nil || helper.Path == "" ||
		migration.Digest(helper.ExpectedDigest) != request.Helper.SHA256 {
		if err == nil {
			err = errors.New("packaged adoption helper is unavailable or changed")
		}
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_helper_changed", request.Binding,
			request.EnvironmentRef, err, false,
		)
	}
	home, err := b.migrationLimaHome()
	if err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.lima_home_unsafe", request.Binding,
			request.StageHandle, err, true,
		)
	}
	stageDir := filepath.Join(
		home, "_hideout-migration", "stages", string(request.StageHandle),
	)
	for _, directory := range []string{
		filepath.Join(home, "_hideout-migration"),
		filepath.Join(home, "_hideout-migration", "stages"),
		stageDir,
	} {
		if _, err := protectedMigrationDirectory(home, directory, directory); err != nil {
			return backend.DestinationAdoption{}, migrationStageError(
				"migration.provider.adoption_ownership_unproved", request.Binding,
				request.StageHandle, err, true,
			)
		}
	}
	owner, ownerDigest, configuration, rootEntry, err :=
		loadMigrationAdoptionOwner(stageDir, request)
	if err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_ownership_unproved", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	if replay, exists, err := loadMigrationAdoptionEvidenceReplay(
		home, stageDir, owner, ownerDigest, configuration, rootEntry, request,
	); exists || err != nil {
		if err != nil {
			return backend.DestinationAdoption{}, migrationStageError(
				"migration.provider.adoption_evidence_invalid", request.Binding,
				request.EnvironmentRef, err, true,
			)
		}
		return replay, nil
	}
	controlRelative := filepath.Join(
		"adoption", string(request.EnvironmentRef), "control",
	)
	control := filepath.Join(stageDir, controlRelative)
	if _, err := os.Lstat(control); err == nil {
		recovered, recoveryErr := b.recoverMigrationAdoptionControl(
			ctx, home, stageDir, owner, ownerDigest, configuration, rootEntry,
			request, controlRelative,
		)
		if recoveryErr != nil {
			return backend.DestinationAdoption{}, migrationStageError(
				"migration.provider.adoption_recovery_required", request.Binding,
				request.EnvironmentRef, recoveryErr, true,
			)
		}
		return recovered, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_ownership_unproved", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	if _, err := loadCompletedMigrationStage(home, stageDir, owner, ownerDigest); err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_stage_invalid", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	if err := verifyMigrationStageDestinationNamesFree(home, owner); err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_stopped_unproved", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}

	guestRequest, executionRequest, paths, err := prepareMigrationAdoptionControl(
		home, stageDir, configuration, rootEntry, request, helper,
	)
	if err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_control_invalid", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	response, err := b.runMigrationAdoptionExecutor(
		ctx, executor, executionRequest, paths,
	)
	if err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_executor_invalid", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	receipt, err := readMigrationAdoptionReceipt(paths.GuestReceipt)
	if err != nil || receipt.MatchesRequest(guestRequest) != nil {
		if err == nil {
			err = errors.New("guest adoption receipt does not match its request")
		}
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_receipt_invalid", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	return finalizeMigrationAdoption(
		ctx, home, stageDir, owner, ownerDigest, configuration, rootEntry,
		request, guestRequest, receipt, response, paths,
	)
}

func loadMigrationAdoptionOwner(
	stageDir string,
	request backend.DestinationAdoptionRequest,
) (migrationStageOwner, migration.Digest, migrationStageConfiguration, migrationStageEntry, error) {
	var owner migrationStageOwner
	if err := readMigrationJSONStrict(filepath.Join(stageDir, "owner.json"), &owner); err != nil {
		return migrationStageOwner{}, "", migrationStageConfiguration{}, migrationStageEntry{}, err
	}
	if err := owner.validate(); err != nil || owner.StageHandle != request.StageHandle ||
		owner.Binding.OperationID != request.Binding.OperationID ||
		owner.Binding.CapabilityRevision != request.Binding.CapabilityRevision ||
		owner.Binding.EffectID == request.Binding.EffectID {
		if err == nil {
			err = errors.New("adoption binding does not match the stage owner")
		}
		return migrationStageOwner{}, "", migrationStageConfiguration{}, migrationStageEntry{}, err
	}
	ownerDigest, err := migrationJSONDigest(owner)
	if err != nil {
		return migrationStageOwner{}, "", migrationStageConfiguration{}, migrationStageEntry{}, err
	}
	complete, err := loadMigrationStageComplete(stageDir)
	if err != nil || complete.StageHandle != owner.StageHandle || complete.OwnerDigest != ownerDigest {
		if err == nil {
			err = errors.New("completed stage binding changed")
		}
		return migrationStageOwner{}, "", migrationStageConfiguration{}, migrationStageEntry{}, err
	}
	var configuration migrationStageConfiguration
	for _, candidate := range owner.Configurations {
		if candidate.EnvironmentRef == request.EnvironmentRef {
			configuration = candidate
			break
		}
	}
	if configuration.EnvironmentRef == "" {
		return migrationStageOwner{}, "", migrationStageConfiguration{}, migrationStageEntry{},
			errors.New("adoption environment is absent from the stage")
	}
	var entry migrationStageEntry
	for _, candidate := range owner.Entries {
		if candidate.DiskID == configuration.RootDiskID {
			entry = candidate
			break
		}
	}
	if entry.ComponentID == "" || entry.Role != migration.DiskRoleRoot ||
		entry.ObjectHandle != configuration.BackendIdentity {
		return migrationStageOwner{}, "", migrationStageConfiguration{}, migrationStageEntry{},
			errors.New("adoption environment lacks its exact root disk")
	}
	return owner, ownerDigest, configuration, entry, nil
}

func prepareMigrationAdoptionControl(
	home, stageDir string,
	configuration migrationStageConfiguration,
	rootEntry migrationStageEntry,
	request backend.DestinationAdoptionRequest,
	helper helperbin.LinuxMigrationAdoptResolution,
) (migration.AdoptionRequest, vzexecutor.ExecutionRequest, vzexecutor.ExecutionPaths, error) {
	requestNonce, err := newMigrationAdoptionNonce("nonce_request")
	if err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	receiptNonce, err := newMigrationAdoptionNonce("nonce_receipt")
	if err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	executionNonce, err := newMigrationAdoptionNonce("nonce_execution")
	if err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	defer clear(privateKey)
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	actions := []string{migration.AdoptionActionPreserveIdentity}
	if request.Policy == migration.GuestIdentitySafeClone {
		actions = []string{
			migration.AdoptionActionResetMachineID,
			migration.AdoptionActionResetSSHHostKeys,
		}
	}
	guestRequest := migration.AdoptionRequest{
		Schema:      migration.AdoptionRequestSchema,
		OperationID: request.Binding.OperationID, EnvironmentRef: request.EnvironmentRef,
		RequestNonce: requestNonce, ReceiptNonce: receiptNonce,
		Policy: request.Policy, SourceIdentity: request.SourceIdentity,
		DestinationSSHKeys: []string{strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey)))},
		PermittedActions:   actions, Helper: request.Helper,
	}
	if err := guestRequest.Validate(); err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	controlRelative := filepath.Join(
		"adoption", string(request.EnvironmentRef), "control",
	)
	executionRequest := vzexecutor.ExecutionRequest{
		Schema: vzexecutor.ExecutionRequestSchema, StageDirectory: stageDir,
		RootDiskRelativePath: rootEntry.RelativePath,
		ControlRelativePath:  controlRelative,
		ExecutionNonce:       executionNonce, CPUCount: 2, MemoryBytes: 1 << 30,
	}
	paths, err := executionRequest.Paths()
	if err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if err := ensureMigrationStageParent(
		home, stageDir, filepath.Dir(controlRelative),
	); err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if err := os.Mkdir(paths.Control, 0o700); err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	for _, directory := range []string{
		paths.RequestDirectory, paths.HelperDirectory, paths.ReceiptDirectory,
	} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
		}
	}
	if err := writeMigrationJSONExclusive(paths.GuestRequest, guestRequest); err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if err := os.Chmod(paths.GuestRequest, 0o400); err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	expectedDigest := strings.TrimPrefix(helper.ExpectedDigest, "sha256:")
	if err := helperbin.CopyVerifiedExecutable(
		helper.Path, paths.GuestHelper, expectedDigest,
	); err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if err := os.Chmod(paths.GuestHelper, 0o500); err != nil {
		return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	for _, directory := range []string{
		paths.RequestDirectory, paths.HelperDirectory, paths.ReceiptDirectory, paths.Control,
	} {
		if err := syncMigrationDirectory(directory); err != nil {
			return migration.AdoptionRequest{}, vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
		}
	}
	return guestRequest, executionRequest, paths, nil
}

func (b Backend) runMigrationAdoptionExecutor(
	ctx context.Context,
	executor helperbin.HostMigrationVZAdoptResolution,
	request vzexecutor.ExecutionRequest,
	paths vzexecutor.ExecutionPaths,
) (vzexecutor.ExecutionResponse, error) {
	data, err := json.Marshal(request)
	if err != nil {
		return vzexecutor.ExecutionResponse{}, err
	}
	data = append(data, '\n')
	runCtx, cancel := context.WithTimeout(ctx, migrationAdoptionExecutorTimeout)
	defer cancel()
	stdout := &boundedRuntimeCapture{limit: migrationAdoptionDocumentLimit}
	stderr := &boundedRuntimeCapture{limit: 4096}
	if err := b.runner().Run(
		runCtx, executor.Path, nil,
		[]string{"LANG=C", "LC_ALL=C", "PATH=/usr/bin:/bin"},
		bytes.NewReader(data), stdout, stderr,
	); err != nil {
		return vzexecutor.ExecutionResponse{}, errors.New(
			"packaged VZ adoption executor failed",
		)
	}
	if runCtx.Err() != nil {
		return vzexecutor.ExecutionResponse{}, runCtx.Err()
	}
	if stdout.truncated || stderr.truncated {
		return vzexecutor.ExecutionResponse{}, errors.New(
			"VZ adoption executor output exceeded its bound",
		)
	}
	response, err := decodeMigrationAdoptionExecutorResponse([]byte(stdout.String()))
	if err != nil || response.ExecutionNonce != request.ExecutionNonce {
		if err == nil {
			err = errors.New("VZ adoption execution nonce changed")
		}
		return vzexecutor.ExecutionResponse{}, err
	}
	var durable vzexecutor.ExecutionResponse
	if err := readMigrationJSONStrict(paths.ExecutorResponse, &durable); err != nil ||
		durable.Validate() != nil || !reflect.DeepEqual(response, durable) {
		if err == nil {
			err = errors.New("durable VZ adoption response is absent or changed")
		}
		return vzexecutor.ExecutionResponse{}, err
	}
	rechecked, err := b.resolveMigrationAdoptionExecutor("darwin", "arm64")
	if err != nil || rechecked.ExpectedDigest != executor.ExpectedDigest {
		return vzexecutor.ExecutionResponse{}, errors.New(
			"packaged VZ adoption executor changed while running",
		)
	}
	return response, nil
}

func decodeMigrationAdoptionExecutorResponse(
	data []byte,
) (vzexecutor.ExecutionResponse, error) {
	if len(data) == 0 || len(data) > migrationAdoptionDocumentLimit {
		return vzexecutor.ExecutionResponse{}, errors.New("VZ adoption response size is invalid")
	}
	var response vzexecutor.ExecutionResponse
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return vzexecutor.ExecutionResponse{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return vzexecutor.ExecutionResponse{}, errors.New("VZ adoption response has trailing data")
	}
	return response, response.Validate()
}

func readMigrationAdoptionReceipt(path string) (migration.AdoptionReceipt, error) {
	data, _, err := readStableMigrationFile(path, migrationAdoptionDocumentLimit)
	if err != nil {
		return migration.AdoptionReceipt{}, err
	}
	var receipt migration.AdoptionReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return migration.AdoptionReceipt{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return migration.AdoptionReceipt{}, errors.New("adoption receipt has trailing data")
	}
	return receipt, receipt.Validate()
}

func finalizeMigrationAdoption(
	ctx context.Context,
	home, stageDir string,
	owner migrationStageOwner,
	ownerDigest migration.Digest,
	configuration migrationStageConfiguration,
	rootEntry migrationStageEntry,
	request backend.DestinationAdoptionRequest,
	guestRequest migration.AdoptionRequest,
	receipt migration.AdoptionReceipt,
	response vzexecutor.ExecutionResponse,
	paths vzexecutor.ExecutionPaths,
) (backend.DestinationAdoption, error) {
	if err := response.Validate(); err != nil || receipt.MatchesRequest(guestRequest) != nil ||
		!migrationGuestRequestMatchesDestination(guestRequest, request) {
		if err == nil {
			err = errors.New("adoption completion binding is invalid")
		}
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_receipt_invalid", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	if err := verifyMigrationStageDestinationNamesFree(home, owner); err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_stopped_unproved", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	if _, err := materializeMigrationStageConfigurations(home, stageDir, owner, false); err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_authority_invalid", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	if err := removeMigrationAdoptionControl(ctx, paths); err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_channel_removal_failed", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	rootIdentity, err := migrationAdoptionEvidenceFileIdentity(
		home, stageDir, rootEntry,
	)
	if err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_stopped_unproved", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	evidence := migrationAdoptionEvidence{
		Schema:      migrationAdoptionEvidenceSchema,
		StageHandle: owner.StageHandle, StageOwnerDigest: ownerDigest,
		Binding: request.Binding, EnvironmentRef: configuration.EnvironmentRef,
		BackendIdentity: configuration.BackendIdentity,
		RootDiskID:      configuration.RootDiskID, RootComponentID: rootEntry.ComponentID,
		PreAdoptionContentDigest: rootEntry.ContentDigest,
		PostAdoptionFileIdentity: rootIdentity,
		Request:                  guestRequest, Receipt: receipt, ShutdownProof: response.ShutdownProof,
		Stopped: true, TemporaryAuthorityRemoved: true, ImportedAuthorityAbsent: true,
	}
	if err := writeMigrationAdoptionEvidenceExclusive(home, stageDir, evidence); err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_evidence_write_failed", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	result := backend.DestinationAdoption{
		Binding: request.Binding, StageHandle: request.StageHandle,
		Request: guestRequest, Receipt: receipt,
		Stopped: true, TemporaryAuthorityRemoved: true,
	}
	if err := result.MatchesRequest(request); err != nil {
		return backend.DestinationAdoption{}, migrationStageError(
			"migration.provider.adoption_response_invalid", request.Binding,
			request.EnvironmentRef, err, true,
		)
	}
	return result, nil
}

func removeMigrationAdoptionControl(
	ctx context.Context,
	paths vzexecutor.ExecutionPaths,
) error {
	allowedFiles := map[string]struct{}{
		filepath.Join("request", "request.json"):                {},
		filepath.Join("helper", vzexecutor.GuestHelperFilename): {},
		filepath.Join("receipt", "receipt.json"):                {},
		"executor-response.json":                                {},
		filepath.Join("runtime", "cidata-source", "meta-data"):  {},
		filepath.Join("runtime", "cidata-source", "user-data"):  {},
		filepath.Join("runtime", "cidata.iso"):                  {},
		filepath.Join("runtime", "efi-variable-store"):          {},
		filepath.Join("runtime", "machine-identifier"):          {},
		filepath.Join("runtime", "serial.log"):                  {},
	}
	files, directories, err := migrationCleanupAllowlistForFiles(allowedFiles)
	if err != nil {
		return err
	}
	observedFiles, observedDirectories, err := inspectMigrationCleanupTree(
		ctx, paths.Control, files, directories,
	)
	if err != nil {
		return err
	}
	if err := removeMigrationCleanupTree(
		ctx, paths.Control, observedFiles, observedDirectories,
	); err != nil {
		return err
	}
	if err := syncMigrationDirectory(filepath.Dir(paths.Control)); err != nil {
		return err
	}
	_, err = os.Lstat(paths.Control)
	if !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			err = errors.New("temporary adoption control still exists")
		}
		return err
	}
	return nil
}

func (b Backend) recoverMigrationAdoptionControl(
	ctx context.Context,
	home, stageDir string,
	owner migrationStageOwner,
	ownerDigest migration.Digest,
	configuration migrationStageConfiguration,
	rootEntry migrationStageEntry,
	request backend.DestinationAdoptionRequest,
	controlRelative string,
) (backend.DestinationAdoption, error) {
	responsePath := filepath.Join(stageDir, controlRelative, "executor-response.json")
	var response vzexecutor.ExecutionResponse
	if err := readMigrationJSONStrict(responsePath, &response); err != nil ||
		response.Validate() != nil {
		return backend.DestinationAdoption{}, errors.New(
			"adoption control lacks a valid stopped executor response",
		)
	}
	execution := vzexecutor.ExecutionRequest{
		Schema: vzexecutor.ExecutionRequestSchema, StageDirectory: stageDir,
		RootDiskRelativePath: rootEntry.RelativePath,
		ControlRelativePath:  controlRelative, ExecutionNonce: response.ExecutionNonce,
		CPUCount: 2, MemoryBytes: 1 << 30,
	}
	paths, err := execution.Paths()
	if err != nil {
		return backend.DestinationAdoption{}, err
	}
	var guestRequest migration.AdoptionRequest
	if err := readMigrationJSONStrict(paths.GuestRequest, &guestRequest); err != nil ||
		!migrationGuestRequestMatchesDestination(guestRequest, request) {
		return backend.DestinationAdoption{}, errors.New(
			"recoverable adoption request is absent or changed",
		)
	}
	receipt, err := readMigrationAdoptionReceipt(paths.GuestReceipt)
	if err != nil || receipt.MatchesRequest(guestRequest) != nil {
		return backend.DestinationAdoption{}, errors.New(
			"recoverable adoption receipt is absent or changed",
		)
	}
	return finalizeMigrationAdoption(
		ctx, home, stageDir, owner, ownerDigest, configuration, rootEntry,
		request, guestRequest, receipt, response, paths,
	)
}

func loadMigrationAdoptionEvidenceReplay(
	home, stageDir string,
	owner migrationStageOwner,
	ownerDigest migration.Digest,
	configuration migrationStageConfiguration,
	rootEntry migrationStageEntry,
	request backend.DestinationAdoptionRequest,
) (backend.DestinationAdoption, bool, error) {
	path := filepath.Join(
		stageDir, migrationAdoptionEvidenceRelativePath(request.EnvironmentRef),
	)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return backend.DestinationAdoption{}, false, nil
	} else if err != nil {
		return backend.DestinationAdoption{}, true, err
	}
	var evidence migrationAdoptionEvidence
	if err := readMigrationJSONStrict(path, &evidence); err != nil {
		return backend.DestinationAdoption{}, true, err
	}
	if evidence.Schema != migrationAdoptionEvidenceSchema ||
		evidence.StageHandle != owner.StageHandle || evidence.StageOwnerDigest != ownerDigest ||
		evidence.Binding != request.Binding ||
		evidence.EnvironmentRef != configuration.EnvironmentRef ||
		evidence.BackendIdentity != configuration.BackendIdentity ||
		evidence.RootDiskID != configuration.RootDiskID ||
		evidence.RootComponentID != rootEntry.ComponentID ||
		evidence.PreAdoptionContentDigest != rootEntry.ContentDigest ||
		evidence.ShutdownProof.Validate() != nil || !evidence.Stopped ||
		!evidence.TemporaryAuthorityRemoved || !evidence.ImportedAuthorityAbsent ||
		!migrationGuestRequestMatchesDestination(evidence.Request, request) ||
		evidence.Receipt.MatchesRequest(evidence.Request) != nil {
		return backend.DestinationAdoption{}, true, errors.New(
			"durable adoption evidence binding changed",
		)
	}
	control := filepath.Join(
		stageDir, "adoption", string(request.EnvironmentRef), "control",
	)
	if _, err := os.Lstat(control); !errors.Is(err, os.ErrNotExist) {
		return backend.DestinationAdoption{}, true, errors.New(
			"durable adoption replay retains temporary control",
		)
	}
	identity, err := migrationAdoptionEvidenceFileIdentity(home, stageDir, rootEntry)
	if err != nil || identity != evidence.PostAdoptionFileIdentity {
		return backend.DestinationAdoption{}, true, errors.New(
			"adopted root changed after durable stopped proof",
		)
	}
	result := backend.DestinationAdoption{
		Binding: request.Binding, StageHandle: request.StageHandle,
		Request: evidence.Request, Receipt: evidence.Receipt,
		Stopped: true, TemporaryAuthorityRemoved: true,
	}
	if err := result.MatchesRequest(request); err != nil {
		return backend.DestinationAdoption{}, true, err
	}
	return result, true, nil
}

func migrationGuestRequestMatchesDestination(
	guest migration.AdoptionRequest,
	request backend.DestinationAdoptionRequest,
) bool {
	return guest.Validate() == nil && guest.OperationID == request.Binding.OperationID &&
		guest.EnvironmentRef == request.EnvironmentRef && guest.Policy == request.Policy &&
		guest.SourceIdentity.Equal(request.SourceIdentity) && guest.Helper == request.Helper
}

func newMigrationAdoptionNonce(prefix string) (migration.OpaqueID, error) {
	data := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		return "", err
	}
	value := migration.OpaqueID(prefix + "_" + hex.EncodeToString(data))
	clear(data)
	if _, err := migration.ParseOpaqueID(string(value)); err != nil {
		return "", err
	}
	return value, nil
}
