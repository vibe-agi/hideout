package lima

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/helperbin"
	"github.com/vibe-agi/hideout/internal/migration"
	"github.com/vibe-agi/hideout/internal/migration/vzexecutor"
)

const (
	migrationSnapshotIdentityEvidenceSchema       = "hideout.lima-migration-source-identities/v1"
	migrationIdentityProbeOwnerSchema             = "hideout.lima-migration-identity-probe-owner/v1"
	migrationSnapshotIdentityEvidenceRelativePath = "identities.json"
)

type migrationSnapshotIdentityEvidence struct {
	Schema            string                            `json:"schema"`
	SnapshotHandle    migration.OpaqueID                `json:"snapshotHandle"`
	SnapshotOwner     migration.Digest                  `json:"snapshotOwnerDigest"`
	Binding           backend.MigrationEffectBinding    `json:"binding"`
	Identities        []backend.MigrationSourceIdentity `json:"identities"`
	ObservationProofs []migration.Digest                `json:"observationProofs"`
}

type migrationIdentityProbeOwner struct {
	Schema           string                               `json:"schema"`
	SnapshotHandle   migration.OpaqueID                   `json:"snapshotHandle"`
	SnapshotOwner    migration.Digest                     `json:"snapshotOwnerDigest"`
	Binding          backend.MigrationEffectBinding       `json:"binding"`
	EnvironmentRef   migration.OpaqueID                   `json:"environmentRef"`
	RootComponent    migration.OpaqueID                   `json:"rootComponent"`
	RootFileIdentity migrationStageFileIdentity           `json:"rootFileIdentity"`
	ProbeHandle      migration.OpaqueID                   `json:"probeHandle"`
	ProbeInstance    string                               `json:"probeInstance"`
	Request          migration.IdentityObservationRequest `json:"request"`
	ExecutionNonce   migration.OpaqueID                   `json:"executionNonce"`
	ExecutorDigest   migration.Digest                     `json:"executorDigest"`
}

func (b Backend) observeMigrationSnapshotIdentities(
	ctx context.Context,
	home, snapshotDir string,
	owner migrationSnapshotOwner,
	ownerDigest migration.Digest,
) (migrationSnapshotIdentityEvidence, error) {
	if evidence, err := loadMigrationSnapshotIdentityEvidence(
		snapshotDir, owner, ownerDigest,
	); err == nil {
		return evidence, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return migrationSnapshotIdentityEvidence{}, err
	}

	var identities []backend.MigrationSourceIdentity
	var proofs []migration.Digest
	var err error
	if b.Migration != nil && b.Migration.sourceIdentityObserver != nil {
		identities, proofs, err = b.Migration.sourceIdentityObserver(
			ctx, snapshotDir, owner, ownerDigest,
		)
	} else {
		identities, proofs, err = b.runMigrationSnapshotIdentityObservations(
			ctx, home, snapshotDir, owner, ownerDigest,
		)
	}
	if err != nil {
		return migrationSnapshotIdentityEvidence{}, err
	}
	evidence := migrationSnapshotIdentityEvidence{
		Schema:         migrationSnapshotIdentityEvidenceSchema,
		SnapshotHandle: owner.SnapshotHandle, SnapshotOwner: ownerDigest,
		Binding: owner.Binding, Identities: identities, ObservationProofs: proofs,
	}
	if err := evidence.validate(owner, ownerDigest); err != nil {
		return migrationSnapshotIdentityEvidence{}, err
	}
	if err := writeMigrationJSONExclusive(
		filepath.Join(snapshotDir, migrationSnapshotIdentityEvidenceRelativePath),
		evidence,
	); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return migrationSnapshotIdentityEvidence{}, err
		}
		return loadMigrationSnapshotIdentityEvidence(snapshotDir, owner, ownerDigest)
	}
	if err := syncMigrationDirectory(snapshotDir); err != nil {
		return migrationSnapshotIdentityEvidence{}, err
	}
	return evidence, nil
}

func loadMigrationSnapshotIdentityEvidence(
	snapshotDir string,
	owner migrationSnapshotOwner,
	ownerDigest migration.Digest,
) (migrationSnapshotIdentityEvidence, error) {
	path := filepath.Join(snapshotDir, migrationSnapshotIdentityEvidenceRelativePath)
	if _, err := os.Lstat(path); err != nil {
		return migrationSnapshotIdentityEvidence{}, err
	}
	var evidence migrationSnapshotIdentityEvidence
	if err := readMigrationJSONStrict(path, &evidence); err != nil {
		return migrationSnapshotIdentityEvidence{}, err
	}
	return evidence, evidence.validate(owner, ownerDigest)
}

func (evidence migrationSnapshotIdentityEvidence) validate(
	owner migrationSnapshotOwner,
	ownerDigest migration.Digest,
) error {
	if evidence.Schema != migrationSnapshotIdentityEvidenceSchema ||
		evidence.SnapshotHandle != owner.SnapshotHandle ||
		evidence.SnapshotOwner != ownerDigest || evidence.Binding != owner.Binding ||
		len(evidence.Identities) != len(owner.Selections) ||
		len(evidence.ObservationProofs) != len(owner.Selections) {
		return errors.New("source identity evidence envelope is invalid")
	}
	for index, selection := range owner.Selections {
		identity := evidence.Identities[index]
		root, err := migrationSnapshotRootForSelection(owner, selection)
		if err != nil || identity.EnvironmentRef != selection.EnvironmentRef ||
			identity.RootComponent != root.ComponentID || identity.Evidence.Validate() != nil ||
			evidence.ObservationProofs[index].Validate() != nil {
			return errors.New("source identity evidence binding is invalid")
		}
	}
	return nil
}

func (b Backend) runMigrationSnapshotIdentityObservations(
	ctx context.Context,
	home, snapshotDir string,
	owner migrationSnapshotOwner,
	ownerDigest migration.Digest,
) ([]backend.MigrationSourceIdentity, []migration.Digest, error) {
	capability, err := b.MigrationCapabilities(ctx)
	if err != nil || !capability.FullExport || capability.AdoptionHelper == nil ||
		capability.Revision != owner.Binding.CapabilityRevision {
		if err == nil {
			err = errors.New("source identity observation capability changed")
		}
		return nil, nil, err
	}
	_, hostArch, guestArch := b.migrationArchitectures()
	helper, err := b.resolveMigrationAdoptionHelper(guestArch)
	if err != nil || migration.Digest(helper.ExpectedDigest) != capability.AdoptionHelper.Digest {
		return nil, nil, errors.New("source identity observation helper changed")
	}
	executor, err := b.resolveMigrationAdoptionExecutor("darwin", hostArch)
	if err != nil || executor.Path == "" {
		return nil, nil, errors.New("source identity observation executor is unavailable")
	}

	identities := make([]backend.MigrationSourceIdentity, 0, len(owner.Selections))
	proofs := make([]migration.Digest, 0, len(owner.Selections))
	for _, selection := range owner.Selections {
		root, err := migrationSnapshotRootForSelection(owner, selection)
		if err != nil {
			return nil, nil, err
		}
		identity, proof, err := b.runMigrationIdentityProbe(
			ctx, home, snapshotDir, owner, ownerDigest, selection, root,
			helper, executor,
		)
		if err != nil {
			return nil, nil, err
		}
		identities = append(identities, backend.MigrationSourceIdentity{
			EnvironmentRef: selection.EnvironmentRef,
			RootComponent:  root.ComponentID,
			Evidence:       identity,
		})
		proofs = append(proofs, proof)
	}
	return identities, proofs, nil
}

func (b Backend) runMigrationIdentityProbe(
	ctx context.Context,
	home, snapshotDir string,
	owner migrationSnapshotOwner,
	ownerDigest migration.Digest,
	selection backend.MigrationSourceSelection,
	root migrationSnapshotEntry,
	helper helperbin.LinuxMigrationAdoptResolution,
	executor helperbin.HostMigrationVZAdoptResolution,
) (migration.GuestIdentityEvidence, migration.Digest, error) {
	rootPath := migrationSnapshotEntryPath(home, snapshotDir, root)
	rootInfo, err := os.Lstat(rootPath)
	if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.Mode().IsRegular() {
		return migration.GuestIdentityEvidence{}, "", errors.New("snapshot identity root is unsafe")
	}
	rootIdentity, err := platformMigrationStageFileIdentity(rootInfo)
	if err != nil {
		return migration.GuestIdentityEvidence{}, "", err
	}
	probeOwner := buildMigrationIdentityProbeOwner(
		owner, ownerDigest, selection, root, rootIdentity, helper, executor,
	)
	probeDir, execution, paths, err := b.prepareMigrationIdentityProbe(
		home, snapshotDir, rootPath, probeOwner, helper,
	)
	if err != nil {
		return migration.GuestIdentityEvidence{}, "", err
	}
	_ = probeDir

	var response vzexecutor.ExecutionResponse
	if _, statErr := os.Lstat(paths.ExecutorResponse); statErr == nil {
		if err := readMigrationJSONStrict(paths.ExecutorResponse, &response); err != nil ||
			response.Validate() != nil || response.ExecutionNonce != execution.ExecutionNonce {
			return migration.GuestIdentityEvidence{}, "", errors.New(
				"recoverable source identity executor response is invalid",
			)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return migration.GuestIdentityEvidence{}, "", statErr
	} else {
		response, err = b.runMigrationAdoptionExecutor(ctx, executor, execution, paths)
		if err != nil {
			return migration.GuestIdentityEvidence{}, "", err
		}
	}
	receipt, err := readMigrationIdentityObservationReceipt(paths.GuestReceipt)
	if err != nil || receipt.MatchesRequest(probeOwner.Request) != nil ||
		receipt.Status != migration.AdoptionReceiptStatusCompleted || receipt.Identity == nil {
		return migration.GuestIdentityEvidence{}, "", errors.New(
			"source identity observation receipt is invalid",
		)
	}
	rootAfter, err := os.Lstat(rootPath)
	if err != nil {
		return migration.GuestIdentityEvidence{}, "", err
	}
	afterIdentity, err := platformMigrationStageFileIdentity(rootAfter)
	if err != nil || afterIdentity != rootIdentity {
		return migration.GuestIdentityEvidence{}, "", errors.New(
			"source snapshot root changed during identity observation",
		)
	}
	proof, err := migrationJSONDigest(struct {
		Schema         string                               `json:"schema"`
		SnapshotHandle migration.OpaqueID                   `json:"snapshotHandle"`
		SnapshotOwner  migration.Digest                     `json:"snapshotOwnerDigest"`
		Request        migration.IdentityObservationRequest `json:"request"`
		Receipt        migration.IdentityObservationReceipt `json:"receipt"`
		Response       vzexecutor.ExecutionResponse         `json:"response"`
		ExecutorDigest migration.Digest                     `json:"executorDigest"`
		RootIdentity   migrationStageFileIdentity           `json:"rootFileIdentity"`
	}{
		Schema:         "hideout.lima-migration-source-identity-proof/v1",
		SnapshotHandle: owner.SnapshotHandle, SnapshotOwner: ownerDigest,
		Request: probeOwner.Request, Receipt: receipt, Response: response,
		ExecutorDigest: probeOwner.ExecutorDigest, RootIdentity: rootIdentity,
	})
	if err != nil {
		return migration.GuestIdentityEvidence{}, "", err
	}
	if err := removeMigrationIdentityProbe(ctx, snapshotDir, probeOwner); err != nil {
		return migration.GuestIdentityEvidence{}, "", err
	}
	return *receipt.Identity, proof, nil
}

func buildMigrationIdentityProbeOwner(
	owner migrationSnapshotOwner,
	ownerDigest migration.Digest,
	selection backend.MigrationSourceSelection,
	root migrationSnapshotEntry,
	rootIdentity migrationStageFileIdentity,
	helper helperbin.LinuxMigrationAdoptResolution,
	executor helperbin.HostMigrationVZAdoptResolution,
) migrationIdentityProbeOwner {
	bindingText := string(owner.Binding.OperationID) + "\x00" +
		string(owner.Binding.EffectID) + "\x00" + string(selection.EnvironmentRef)
	request := migration.IdentityObservationRequest{
		Schema:      migration.IdentityObservationRequestSchema,
		OperationID: owner.Binding.OperationID, EnvironmentRef: selection.EnvironmentRef,
		RequestNonce: migrationOpaqueRef("idrequest", bindingText),
		ReceiptNonce: migrationOpaqueRef("idreceipt", bindingText),
		Helper: migration.HelperBinding{
			PackageID: migration.AdoptionHelperPackage,
			Version:   migrationAdoptionVersion,
			SHA256:    migration.Digest(helper.ExpectedDigest),
		},
	}
	return migrationIdentityProbeOwner{
		Schema:         migrationIdentityProbeOwnerSchema,
		SnapshotHandle: owner.SnapshotHandle, SnapshotOwner: ownerDigest,
		Binding: owner.Binding, EnvironmentRef: selection.EnvironmentRef,
		RootComponent: root.ComponentID, RootFileIdentity: rootIdentity,
		ProbeHandle:    migrationOpaqueRef("idprobe", bindingText),
		ProbeInstance:  string(migrationOpaqueRef("probe", bindingText)),
		Request:        request,
		ExecutionNonce: migrationOpaqueRef("idexecution", bindingText),
		ExecutorDigest: migration.Digest(executor.ExpectedDigest),
	}
}

func (probe migrationIdentityProbeOwner) validate() error {
	if probe.Schema != migrationIdentityProbeOwnerSchema ||
		!migrationValidOpaqueRef(probe.SnapshotHandle) ||
		probe.SnapshotOwner.Validate() != nil || probe.Binding.Validate() != nil ||
		probe.Request.Validate() != nil ||
		probe.Request.OperationID != probe.Binding.OperationID ||
		probe.Request.EnvironmentRef != probe.EnvironmentRef ||
		!migrationValidOpaqueRef(probe.EnvironmentRef) ||
		!migrationValidOpaqueRef(probe.RootComponent) ||
		probe.RootFileIdentity.Size <= 0 || probe.RootFileIdentity.Links == 0 ||
		probe.ExecutorDigest.Validate() != nil ||
		!migrationValidOpaqueRef(probe.ProbeHandle) ||
		!migrationValidOpaqueRef(probe.ExecutionNonce) ||
		!migrationProviderObjectName(probe.ProbeInstance) {
		return errors.New("source identity probe owner is invalid")
	}
	return nil
}

func (b Backend) prepareMigrationIdentityProbe(
	home, snapshotDir, rootPath string,
	expected migrationIdentityProbeOwner,
	helper helperbin.LinuxMigrationAdoptResolution,
) (string, vzexecutor.ExecutionRequest, vzexecutor.ExecutionPaths, error) {
	identityRoot, err := ensurePrivateMigrationDirectory(snapshotDir, "identity-probes")
	if err != nil {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	probeDir := filepath.Join(identityRoot, string(expected.EnvironmentRef))
	created := false
	if err := os.Mkdir(probeDir, 0o700); err == nil {
		created = true
		if err := writeMigrationJSONExclusive(filepath.Join(probeDir, "owner.json"), expected); err != nil {
			return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
		}
		if err := syncMigrationDirectory(probeDir); err != nil {
			return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
		}
		if err := syncMigrationDirectory(identityRoot); err != nil {
			return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
		}
	} else if !errors.Is(err, os.ErrExist) {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if _, err := protectedMigrationDirectory(snapshotDir, probeDir, probeDir); err != nil {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	var observed migrationIdentityProbeOwner
	if err := readMigrationJSONStrict(filepath.Join(probeDir, "owner.json"), &observed); err != nil ||
		observed.validate() != nil ||
		!reflect.DeepEqual(observed, expected) {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, errors.New(
			"source identity probe ownership changed",
		)
	}
	execution := vzexecutor.ExecutionRequest{
		Schema: vzexecutor.ExecutionRequestSchema, StageDirectory: probeDir,
		RootDiskRelativePath: filepath.Join("instances", expected.ProbeInstance, "disk"),
		ControlRelativePath: filepath.Join(
			"adoption", string(expected.EnvironmentRef), "control",
		),
		ExecutionNonce: expected.ExecutionNonce, CPUCount: 2, MemoryBytes: 1 << 30,
	}
	paths, err := execution.Paths()
	if err != nil {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if !created {
		return probeDir, execution, paths, nil
	}
	for _, directory := range []string{
		filepath.Join(probeDir, "instances"),
		filepath.Join(probeDir, "instances", expected.ProbeInstance),
		filepath.Join(probeDir, "adoption"),
		filepath.Join(probeDir, "adoption", string(expected.EnvironmentRef)),
		paths.Control, paths.RequestDirectory, paths.HelperDirectory, paths.ReceiptDirectory,
	} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
		}
	}
	if err := b.cloneMigrationFile(rootPath, paths.RootDisk); err != nil {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if err := syncMigrationRegularFile(paths.RootDisk); err != nil {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if err := writeMigrationJSONExclusive(paths.GuestRequest, expected.Request); err != nil {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if err := os.Chmod(paths.GuestRequest, 0o400); err != nil {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if err := helperbin.CopyVerifiedExecutable(
		helper.Path, paths.GuestHelper,
		strings.TrimPrefix(helper.ExpectedDigest, "sha256:"),
	); err != nil {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	if err := os.Chmod(paths.GuestHelper, 0o500); err != nil {
		return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
	}
	for _, directory := range []string{
		filepath.Dir(paths.RootDisk), paths.RequestDirectory, paths.HelperDirectory,
		paths.ReceiptDirectory, paths.Control, probeDir,
	} {
		if err := syncMigrationDirectory(directory); err != nil {
			return "", vzexecutor.ExecutionRequest{}, vzexecutor.ExecutionPaths{}, err
		}
	}
	return probeDir, execution, paths, nil
}

func readMigrationIdentityObservationReceipt(
	path string,
) (migration.IdentityObservationReceipt, error) {
	data, _, err := readStableMigrationFile(path, migrationAdoptionDocumentLimit)
	if err != nil {
		return migration.IdentityObservationReceipt{}, err
	}
	var receipt migration.IdentityObservationReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return migration.IdentityObservationReceipt{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return migration.IdentityObservationReceipt{}, errors.New(
			"identity observation receipt has trailing data",
		)
	}
	return receipt, receipt.Validate()
}

func migrationSnapshotRootForSelection(
	owner migrationSnapshotOwner,
	selection backend.MigrationSourceSelection,
) (migrationSnapshotEntry, error) {
	for _, entry := range owner.Entries {
		if entry.Role == migration.DiskRoleRoot &&
			entry.SourceObject == selection.ProviderInstance {
			return entry, nil
		}
	}
	return migrationSnapshotEntry{}, errors.New("source identity root component is absent")
}

func removeMigrationIdentityProbe(
	ctx context.Context,
	snapshotDir string,
	owner migrationIdentityProbeOwner,
) error {
	probeDir := filepath.Join(
		snapshotDir, "identity-probes", string(owner.EnvironmentRef),
	)
	allowed := migrationIdentityProbeAllowedFilesFromProbe(owner)
	files, directories, err := migrationCleanupAllowlistForFiles(allowed)
	if err != nil {
		return err
	}
	observedFiles, observedDirectories, err := inspectMigrationCleanupTree(
		ctx, probeDir, files, directories,
	)
	if err != nil {
		return err
	}
	if err := removeMigrationCleanupTree(
		ctx, probeDir, observedFiles, observedDirectories,
	); err != nil {
		return err
	}
	return syncMigrationDirectory(filepath.Dir(probeDir))
}

func migrationIdentityProbeAllowedFiles(
	owner migrationSnapshotOwner,
	environmentRef migration.OpaqueID,
) map[string]struct{} {
	bindingText := string(owner.Binding.OperationID) + "\x00" +
		string(owner.Binding.EffectID) + "\x00" + string(environmentRef)
	probe := migrationIdentityProbeOwner{
		EnvironmentRef: environmentRef,
		ProbeInstance:  string(migrationOpaqueRef("probe", bindingText)),
	}
	base := filepath.Join("identity-probes", string(environmentRef))
	files := make(map[string]struct{})
	for relative := range migrationIdentityProbeAllowedFilesFromProbe(probe) {
		files[filepath.Join(base, relative)] = struct{}{}
	}
	return files
}

func migrationIdentityProbeAllowedFilesFromProbe(
	owner migrationIdentityProbeOwner,
) map[string]struct{} {
	control := filepath.Join("adoption", string(owner.EnvironmentRef), "control")
	return map[string]struct{}{
		"owner.json": {},
		filepath.Join("instances", owner.ProbeInstance, "disk"):          {},
		filepath.Join(control, "request", "request.json"):                {},
		filepath.Join(control, "helper", vzexecutor.GuestHelperFilename): {},
		filepath.Join(control, "receipt", "receipt.json"):                {},
		filepath.Join(control, "executor-response.json"):                 {},
		filepath.Join(control, "runtime", "cidata-source", "meta-data"):  {},
		filepath.Join(control, "runtime", "cidata-source", "user-data"):  {},
		filepath.Join(control, "runtime", "cidata.iso"):                  {},
		filepath.Join(control, "runtime", "efi-variable-store"):          {},
		filepath.Join(control, "runtime", "machine-identifier"):          {},
		filepath.Join(control, "runtime", "serial.log"):                  {},
	}
}
