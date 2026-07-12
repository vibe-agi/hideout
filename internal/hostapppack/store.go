package hostapppack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/packsnapshot"
	"golang.org/x/sys/unix"
)

const maxRegistryPacks = 32
const maxRevisionsPerPack = 16

type Store struct {
	Root string
}

func NewStore(root string) Store { return Store{Root: filepath.Join(root, "host-app-packs")} }

func (s Store) RegistryPath() string { return filepath.Join(s.Root, "registry.json") }
func (s Store) LockPath() string     { return filepath.Join(s.Root, ".store.lock") }
func (s Store) PacksDir() string     { return filepath.Join(s.Root, "packs") }
func (s Store) SourceDir(packID, revisionID string) string {
	return filepath.Join(s.PacksDir(), packID, revisionID, "source")
}
func (s Store) TestResultPath(packID, revisionID string) string {
	return filepath.Join(s.PacksDir(), packID, revisionID, "test-result.json")
}
func (s Store) EnablementPath(profile, packID string) string {
	return filepath.Join(s.Root, "enablements", profile, packID+".json")
}

func (s Store) Install(request InstallRequest) (RegistryEntry, Revision, error) {
	if err := s.ensurePrivateRoot(); err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	stagingRoot := filepath.Join(s.Root, "staging")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	if err := os.Chmod(stagingRoot, 0o700); err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	staging, err := os.MkdirTemp(stagingRoot, ".candidate-*")
	if err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	defer os.RemoveAll(staging)
	sourceDir := filepath.Join(staging, "source")
	snapshot, err := packsnapshot.Acquire(packsnapshot.SourceSpec{
		Kind: request.Source.Kind, Path: request.Source.Path, URL: request.Source.URL, Commit: request.Source.Commit,
	}, sourceDir, packsnapshot.Options{
		Limits: packsnapshot.DefaultLimits(), DigestStyle: packsnapshot.DigestCanonicalV1, WorkRoot: stagingRoot,
	})
	if err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	manifest, rawManifest, err := LoadManifest(filepath.Join(sourceDir, ManifestFileName))
	if err != nil {
		return RegistryEntry{}, Revision{}, fmt.Errorf("load host-app pack manifest: %w", err)
	}
	baseFingerprint, err := BasePermissionFingerprint(manifest)
	if err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	sourceDigest := snapshot.Digest
	if request.ExpectedSourceDigest != "" && request.ExpectedSourceDigest != sourceDigest {
		return RegistryEntry{}, Revision{}, fmt.Errorf("host-app source digest mismatch: expected %s got %s", request.ExpectedSourceDigest, sourceDigest)
	}
	if request.ExpectedBasePermissionFingerprint != "" && request.ExpectedBasePermissionFingerprint != baseFingerprint {
		return RegistryEntry{}, Revision{}, fmt.Errorf("host-app base permission fingerprint mismatch: expected %s got %s", request.ExpectedBasePermissionFingerprint, baseFingerprint)
	}
	now := time.Now().UTC()
	revision := Revision{
		RevisionID:                packsnapshot.RevisionID(sourceDigest),
		PackID:                    manifest.ID,
		Source:                    snapshotSourceLock(snapshot.Source, now),
		SourceDigest:              sourceDigest,
		ManifestDigest:            packsnapshot.DigestBytes(rawManifest),
		BasePermissionFingerprint: baseFingerprint,
		ValidationStatus:          ValidationPassed,
		TestStatus:                TestNotRun,
		InstalledAt:               now,
		State:                     RevisionInstalled,
	}
	var installed RegistryEntry
	err = s.withLock(func() error {
		registry, err := s.loadRegistryUnlocked()
		if err != nil {
			return err
		}
		index := registryPackIndex(registry, manifest.ID)
		entry := RegistryEntry{ID: manifest.ID, State: PackInstalled, Revisions: []Revision{}}
		if index >= 0 {
			entry = registry.Packs[index]
			if entry.State == PackRemoved {
				return fmt.Errorf("host-app pack %q has a retained removal tombstone", manifest.ID)
			}
		}
		final := s.SourceDir(manifest.ID, revision.RevisionID)
		published := false
		if _, statErr := os.Stat(final); statErr == nil {
			digest, _, digestErr := packsnapshot.DigestTree(final, packsnapshot.DigestCanonicalV1, packsnapshot.DefaultLimits())
			if digestErr != nil || digest != revision.SourceDigest {
				return fmt.Errorf("installed host-app revision %q conflicts with candidate digest", revision.RevisionID)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		} else {
			if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
				return err
			}
			if err := os.Rename(sourceDir, final); err != nil {
				return err
			}
			published = true
		}
		entry.State = PackInstalled
		entry.ActiveRevisionID = revision.RevisionID
		if existingRevision := revisionIndex(entry, revision.RevisionID); existingRevision >= 0 {
			revision = entry.Revisions[existingRevision]
		} else {
			if len(entry.Revisions) >= maxRevisionsPerPack {
				if published {
					_ = os.RemoveAll(filepath.Dir(final))
				}
				return fmt.Errorf("host-app pack %q exceeds %d revisions", manifest.ID, maxRevisionsPerPack)
			}
			entry.Revisions = append(entry.Revisions, revision)
		}
		if index < 0 {
			if len(registry.Packs) >= maxRegistryPacks {
				if published {
					_ = os.RemoveAll(filepath.Dir(final))
				}
				return fmt.Errorf("host-app registry exceeds %d packs", maxRegistryPacks)
			}
			registry.Packs = append(registry.Packs, entry)
		} else {
			registry.Packs[index] = entry
		}
		if err := s.saveRegistryUnlocked(registry); err != nil {
			if published {
				_ = os.RemoveAll(filepath.Dir(final))
			}
			return err
		}
		installed = entry
		return nil
	})
	if err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	return installed, revision, nil
}

// InstallTestEnable publishes one exact snapshot, its Core quality result, and
// one profile enablement as a single store transaction. The registry is the
// final commit record; failures before that point restore prior test and
// enablement files and remove a newly published revision.
func (s Store) InstallTestEnable(request InstallRequest, enablement Enablement, context EffectivePermissionContext, recordedAt time.Time) (RegistryEntry, Revision, TestResult, error) {
	if err := s.ensurePrivateRoot(); err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}
	stagingRoot := filepath.Join(s.Root, "staging")
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}
	if err := os.Chmod(stagingRoot, 0o700); err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}
	staging, err := os.MkdirTemp(stagingRoot, ".candidate-*")
	if err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}
	defer os.RemoveAll(staging)
	sourceDir := filepath.Join(staging, "source")
	snapshot, err := packsnapshot.Acquire(packsnapshot.SourceSpec{
		Kind: request.Source.Kind, Path: request.Source.Path, URL: request.Source.URL, Commit: request.Source.Commit,
	}, sourceDir, packsnapshot.Options{
		Limits: packsnapshot.DefaultLimits(), DigestStyle: packsnapshot.DigestCanonicalV1, WorkRoot: stagingRoot,
	})
	if err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}
	manifest, rawManifest, err := LoadManifest(filepath.Join(sourceDir, ManifestFileName))
	if err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, fmt.Errorf("load host-app pack manifest: %w", err)
	}
	baseFingerprint, err := BasePermissionFingerprint(manifest)
	if err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}
	if request.ExpectedSourceDigest != "" && request.ExpectedSourceDigest != snapshot.Digest {
		return RegistryEntry{}, Revision{}, TestResult{}, fmt.Errorf("host-app source digest mismatch: expected %s got %s", request.ExpectedSourceDigest, snapshot.Digest)
	}
	if request.ExpectedBasePermissionFingerprint != "" && request.ExpectedBasePermissionFingerprint != baseFingerprint {
		return RegistryEntry{}, Revision{}, TestResult{}, fmt.Errorf("host-app base permission fingerprint mismatch: expected %s got %s", request.ExpectedBasePermissionFingerprint, baseFingerprint)
	}
	now := recordedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	revision := Revision{
		RevisionID: packsnapshot.RevisionID(snapshot.Digest), PackID: manifest.ID,
		Source: snapshotSourceLock(snapshot.Source, now), SourceDigest: snapshot.Digest,
		ManifestDigest: packsnapshot.DigestBytes(rawManifest), BasePermissionFingerprint: baseFingerprint,
		ValidationStatus: ValidationPassed, TestStatus: TestNotRun, InstalledAt: now, State: RevisionInstalled,
	}
	testResult, err := RunQualityTests(manifest, revision.RevisionID, now)
	if err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}
	if err := validateTestResult(testResult); err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}
	revision.TestStatus = testResult.Status
	if err := ValidateEnablement(enablement, revision, manifest, context); err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}

	var installed RegistryEntry
	err = s.withLock(func() error {
		registry, err := s.loadRegistryUnlocked()
		if err != nil {
			return err
		}
		index := registryPackIndex(registry, manifest.ID)
		entry := RegistryEntry{ID: manifest.ID, State: PackInstalled, Revisions: []Revision{}}
		if index >= 0 {
			entry = registry.Packs[index]
			if entry.State == PackRemoved {
				return fmt.Errorf("host-app pack %q has a retained removal tombstone", manifest.ID)
			}
		}
		if prior, loadErr := s.loadEnablementUnlocked(enablement.Profile, enablement.PackID); loadErr == nil && prior.State == EnablementRevoked && enablement.State != EnablementRevoked {
			return errors.New("revoked host-app enablement is terminal")
		} else if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
			return loadErr
		}

		final := s.SourceDir(manifest.ID, revision.RevisionID)
		published := false
		if _, statErr := os.Stat(final); statErr == nil {
			digest, _, digestErr := packsnapshot.DigestTree(final, packsnapshot.DigestCanonicalV1, packsnapshot.DefaultLimits())
			if digestErr != nil || digest != revision.SourceDigest {
				return fmt.Errorf("installed host-app revision %q conflicts with candidate digest", revision.RevisionID)
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		} else {
			if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
				return err
			}
			if err := os.Rename(sourceDir, final); err != nil {
				return err
			}
			published = true
		}

		entry.State = PackInstalled
		entry.ActiveRevisionID = revision.RevisionID
		if existingRevision := revisionIndex(entry, revision.RevisionID); existingRevision >= 0 {
			installedRevision := entry.Revisions[existingRevision]
			if installedRevision.SourceDigest != revision.SourceDigest {
				if published {
					_ = os.RemoveAll(filepath.Dir(final))
				}
				return fmt.Errorf("installed host-app revision %q changed identity", revision.RevisionID)
			}
			revision = installedRevision
			revision.TestStatus = testResult.Status
			entry.Revisions[existingRevision] = revision
		} else {
			if len(entry.Revisions) >= maxRevisionsPerPack {
				if published {
					_ = os.RemoveAll(filepath.Dir(final))
				}
				return fmt.Errorf("host-app pack %q exceeds %d revisions", manifest.ID, maxRevisionsPerPack)
			}
			entry.Revisions = append(entry.Revisions, revision)
		}
		if index < 0 {
			if len(registry.Packs) >= maxRegistryPacks {
				if published {
					_ = os.RemoveAll(filepath.Dir(final))
				}
				return fmt.Errorf("host-app registry exceeds %d packs", maxRegistryPacks)
			}
			registry.Packs = append(registry.Packs, entry)
		} else {
			registry.Packs[index] = entry
		}

		testPath := s.TestResultPath(manifest.ID, revision.RevisionID)
		enablementPath := s.EnablementPath(enablement.Profile, enablement.PackID)
		priorTest, priorTestErr := os.ReadFile(testPath)
		priorEnablement, priorEnablementErr := os.ReadFile(enablementPath)
		restore := func() {
			if published {
				_ = os.RemoveAll(filepath.Dir(final))
			} else if priorTestErr == nil {
				_ = os.WriteFile(testPath, priorTest, 0o600)
			} else {
				_ = os.Remove(testPath)
			}
			if priorEnablementErr == nil {
				_ = os.MkdirAll(filepath.Dir(enablementPath), 0o700)
				_ = os.WriteFile(enablementPath, priorEnablement, 0o600)
			} else {
				_ = os.Remove(enablementPath)
			}
		}
		if err := atomicWriteJSON(testPath, testResult); err != nil {
			restore()
			return err
		}
		if err := atomicWriteJSON(enablementPath, normalizeEnablement(enablement)); err != nil {
			restore()
			return err
		}
		if err := s.saveRegistryUnlocked(registry); err != nil {
			restore()
			return err
		}
		installed = entry
		return nil
	})
	if err != nil {
		return RegistryEntry{}, Revision{}, TestResult{}, err
	}
	return installed, revision, testResult, nil
}

func (s Store) LoadRegistry() (Registry, error) { return s.loadRegistryUnlocked() }

func (s Store) loadRegistryUnlocked() (Registry, error) {
	data, err := os.ReadFile(s.RegistryPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Registry{Schema: RegistryVersion, UpdatedAt: time.Now().UTC(), Packs: []RegistryEntry{}}, nil
		}
		return Registry{}, err
	}
	var registry Registry
	if err := decodeStrict(data, &registry); err != nil {
		return Registry{}, err
	}
	if err := ValidateRegistry(registry); err != nil {
		return Registry{}, err
	}
	return registry, nil
}

func (s Store) SaveRegistry(registry Registry) error {
	return s.withLock(func() error { return s.saveRegistryUnlocked(registry) })
}

func (s Store) saveRegistryUnlocked(registry Registry) error {
	registry.Schema = RegistryVersion
	registry.UpdatedAt = time.Now().UTC()
	normalizeRegistry(&registry)
	if err := ValidateRegistry(registry); err != nil {
		return err
	}
	return atomicWriteJSON(s.RegistryPath(), registry)
}

func ValidateRegistry(registry Registry) error {
	if registry.Schema != RegistryVersion {
		return fmt.Errorf("unsupported host-app registry schema %q", registry.Schema)
	}
	if registry.UpdatedAt.IsZero() {
		return errors.New("registry updatedAt is required")
	}
	if registry.Packs == nil || len(registry.Packs) > maxRegistryPacks {
		return fmt.Errorf("registry packs must contain at most %d entries", maxRegistryPacks)
	}
	seenPacks := map[string]struct{}{}
	for i, entry := range registry.Packs {
		if err := validatePackID(entry.ID); err != nil {
			return fmt.Errorf("packs[%d].id: %w", i, err)
		}
		if _, exists := seenPacks[entry.ID]; exists {
			return fmt.Errorf("packs[%d].id duplicates %q", i, entry.ID)
		}
		seenPacks[entry.ID] = struct{}{}
		switch entry.State {
		case PackInstalled, PackRevoked, PackRemoved:
		default:
			return fmt.Errorf("packs[%d].state %q is unsupported", i, entry.State)
		}
		if len(entry.Revisions) == 0 || len(entry.Revisions) > maxRevisionsPerPack {
			return fmt.Errorf("packs[%d].revisions must contain 1-%d entries", i, maxRevisionsPerPack)
		}
		seenRevisions := map[string]struct{}{}
		for j, revision := range entry.Revisions {
			if revision.PackID != entry.ID {
				return fmt.Errorf("packs[%d].revisions[%d].packId mismatch", i, j)
			}
			if _, exists := seenRevisions[revision.RevisionID]; exists {
				return fmt.Errorf("packs[%d].revisions[%d] duplicates %q", i, j, revision.RevisionID)
			}
			seenRevisions[revision.RevisionID] = struct{}{}
			if err := validateRevision(revision); err != nil {
				return fmt.Errorf("packs[%d].revisions[%d]: %w", i, j, err)
			}
		}
		if entry.ActiveRevisionID != "" {
			index := revisionIndex(entry, entry.ActiveRevisionID)
			if index < 0 || entry.Revisions[index].State != RevisionInstalled {
				return fmt.Errorf("packs[%d].activeRevisionId must name an installed revision", i)
			}
		}
	}
	return nil
}

func (s Store) ResolveRevision(packID, revisionID string) (Revision, error) {
	revision, _, err := s.ResolveRevisionManifest(packID, revisionID)
	return revision, err
}

// ResolveRevisionManifest returns revision metadata and a manifest decoded
// from the same verified private snapshot.
func (s Store) ResolveRevisionManifest(packID, revisionID string) (Revision, Manifest, error) {
	registry, err := s.LoadRegistry()
	if err != nil {
		return Revision{}, Manifest{}, err
	}
	resolved, err := s.resolveRevisionSnapshot(registry, packID, revisionID)
	if err != nil {
		return Revision{}, Manifest{}, err
	}
	return resolved.revision, resolved.manifest, nil
}

type resolvedRevisionSnapshot struct {
	revision Revision
	manifest Manifest
}

func (s Store) resolveRevisionSnapshot(registry Registry, packID, revisionID string) (resolvedRevisionSnapshot, error) {
	packIndex := registryPackIndex(registry, packID)
	if packIndex < 0 || registry.Packs[packIndex].State != PackInstalled {
		return resolvedRevisionSnapshot{}, fmt.Errorf("host-app pack %q is not installed", packID)
	}
	entry := registry.Packs[packIndex]
	revIndex := revisionIndex(entry, revisionID)
	if revIndex < 0 || entry.Revisions[revIndex].State != RevisionInstalled {
		return resolvedRevisionSnapshot{}, fmt.Errorf("host-app pack %q revision %q is not installed", packID, revisionID)
	}
	revision := entry.Revisions[revIndex]
	scratch, err := os.MkdirTemp(s.Root, ".resolve-*")
	if err != nil {
		return resolvedRevisionSnapshot{}, err
	}
	defer os.RemoveAll(scratch)
	if err := os.Chmod(scratch, 0o700); err != nil {
		return resolvedRevisionSnapshot{}, err
	}
	root := filepath.Join(scratch, "source")
	snapshot, err := packsnapshot.Acquire(packsnapshot.SourceSpec{
		Kind: packsnapshot.SourceLocal,
		Path: s.SourceDir(packID, revisionID),
	}, root, packsnapshot.Options{
		Limits: packsnapshot.DefaultLimits(), DigestStyle: packsnapshot.DigestCanonicalV1, WorkRoot: scratch,
	})
	if err != nil {
		return resolvedRevisionSnapshot{}, err
	}
	if snapshot.Digest != revision.SourceDigest {
		return resolvedRevisionSnapshot{}, fmt.Errorf("host-app pack %q revision %q source digest mismatch", packID, revisionID)
	}
	raw, err := os.ReadFile(filepath.Join(root, ManifestFileName))
	if err != nil {
		return resolvedRevisionSnapshot{}, err
	}
	if packsnapshot.DigestBytes(raw) != revision.ManifestDigest {
		return resolvedRevisionSnapshot{}, fmt.Errorf("host-app pack %q revision %q manifest identity mismatch", packID, revisionID)
	}
	manifest, err := DecodeManifest(raw)
	if err != nil {
		return resolvedRevisionSnapshot{}, err
	}
	if manifest.ID != packID {
		return resolvedRevisionSnapshot{}, fmt.Errorf("host-app pack %q revision %q manifest identity mismatch", packID, revisionID)
	}
	fingerprint, err := BasePermissionFingerprint(manifest)
	if err != nil {
		return resolvedRevisionSnapshot{}, err
	}
	if fingerprint != revision.BasePermissionFingerprint {
		return resolvedRevisionSnapshot{}, fmt.Errorf("host-app pack %q revision %q permission fingerprint mismatch", packID, revisionID)
	}
	return resolvedRevisionSnapshot{revision: revision, manifest: manifest}, nil
}

func (s Store) SaveTestResult(result TestResult) error {
	if err := validateTestResult(result); err != nil {
		return err
	}
	return s.withLock(func() error {
		registry, err := s.loadRegistryUnlocked()
		if err != nil {
			return err
		}
		packIndex := registryPackIndex(registry, result.PackID)
		if packIndex < 0 || registry.Packs[packIndex].State != PackInstalled {
			return fmt.Errorf("host-app pack %q is not installed", result.PackID)
		}
		revIndex := revisionIndex(registry.Packs[packIndex], result.RevisionID)
		if revIndex < 0 || registry.Packs[packIndex].Revisions[revIndex].State != RevisionInstalled {
			return fmt.Errorf("host-app revision %q is not installed", result.RevisionID)
		}
		if err := atomicWriteJSON(s.TestResultPath(result.PackID, result.RevisionID), result); err != nil {
			return err
		}
		registry.Packs[packIndex].Revisions[revIndex].TestStatus = result.Status
		return s.saveRegistryUnlocked(registry)
	})
}

func (s Store) LoadTestResult(packID, revisionID string) (TestResult, error) {
	if err := validatePackID(packID); err != nil {
		return TestResult{}, err
	}
	if err := validateStorageID("revision", revisionID); err != nil {
		return TestResult{}, err
	}
	data, err := os.ReadFile(s.TestResultPath(packID, revisionID))
	if err != nil {
		return TestResult{}, err
	}
	var result TestResult
	if err := decodeStrict(data, &result); err != nil {
		return TestResult{}, err
	}
	if err := validateTestResult(result); err != nil {
		return TestResult{}, err
	}
	return result, nil
}

func (s Store) SaveEnablement(enablement Enablement, context EffectivePermissionContext) error {
	_, _, err := s.SaveEnablementSnapshot(enablement, context)
	return err
}

// SaveEnablementSnapshot validates and persists an enablement against one
// private immutable revision snapshot, then returns the exact revision and
// manifest consumed by that commit. Callers must not reopen the installed
// manifest after this method returns.
func (s Store) SaveEnablementSnapshot(enablement Enablement, context EffectivePermissionContext) (Revision, Manifest, error) {
	if err := validateStorageID("profile", enablement.Profile); err != nil {
		return Revision{}, Manifest{}, err
	}
	if err := validatePackID(enablement.PackID); err != nil {
		return Revision{}, Manifest{}, err
	}
	var committed resolvedRevisionSnapshot
	err := s.withLock(func() error {
		registry, err := s.loadRegistryUnlocked()
		if err != nil {
			return err
		}
		resolved, err := s.resolveRevisionSnapshot(registry, enablement.PackID, enablement.RevisionID)
		if err != nil {
			return err
		}
		if err := ValidateEnablement(enablement, resolved.revision, resolved.manifest, context); err != nil {
			return err
		}
		if prior, err := s.loadEnablementUnlocked(enablement.Profile, enablement.PackID); err == nil && prior.State == EnablementRevoked && enablement.State != EnablementRevoked {
			return errors.New("revoked host-app enablement is terminal")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := atomicWriteJSON(s.EnablementPath(enablement.Profile, enablement.PackID), normalizeEnablement(enablement)); err != nil {
			return err
		}
		committed = resolved
		return nil
	})
	if err != nil {
		return Revision{}, Manifest{}, err
	}
	return committed.revision, committed.manifest, nil
}

func ValidateEnablement(enablement Enablement, revision Revision, manifest Manifest, context EffectivePermissionContext) error {
	if enablement.Schema != EnablementVersion {
		return fmt.Errorf("unsupported host-app enablement schema %q", enablement.Schema)
	}
	if enablement.PackID != revision.PackID || enablement.RevisionID != revision.RevisionID || enablement.SourceDigest != revision.SourceDigest || enablement.BasePermissionFingerprint != revision.BasePermissionFingerprint {
		return errors.New("enablement must bind the exact installed revision")
	}
	if context.Access != enablement.Access {
		return errors.New("enablement access does not match effective permission context")
	}
	if len(context.BindingIDs) == 0 {
		context.BindingIDs = append([]string(nil), enablement.BindingIDs...)
	}
	if context.ConflictReplacements == nil {
		context.ConflictReplacements = make(map[string]string, len(enablement.ConflictReplacements))
		for command, owner := range enablement.ConflictReplacements {
			context.ConflictReplacements[command] = owner
		}
	}
	if !slices.Equal(sortedCopy(context.BindingIDs), sortedCopy(enablement.BindingIDs)) ||
		!maps.Equal(context.ConflictReplacements, enablement.ConflictReplacements) {
		return errors.New("enablement authority does not match effective permission context")
	}
	effective, err := EffectivePermissionFingerprint(manifest, context)
	if err != nil {
		return err
	}
	if enablement.PermissionFingerprint != effective {
		return errors.New("enablement permission fingerprint does not bind its exact bindings, replacements, access, and Core safety profile")
	}
	if len(enablement.BindingIDs) == 0 || len(enablement.BindingIDs) > MaxBindings {
		return errors.New("enablement bindingIds is required")
	}
	available := map[string]struct{}{}
	for _, binding := range manifest.Bindings {
		available[binding.ID] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, bindingID := range enablement.BindingIDs {
		if _, exists := available[bindingID]; !exists {
			return fmt.Errorf("enablement binding %q is not in the revision", bindingID)
		}
		if _, exists := seen[bindingID]; exists {
			return fmt.Errorf("enablement binding %q is duplicated", bindingID)
		}
		seen[bindingID] = struct{}{}
	}
	switch enablement.Access {
	case AccessSafe, AccessAskEachRun:
	default:
		return fmt.Errorf("enablement access %q is unsupported", enablement.Access)
	}
	switch enablement.State {
	case EnablementEnabled, EnablementSuspended, EnablementDisabled, EnablementRevoked:
	default:
		return fmt.Errorf("enablement state %q is unsupported", enablement.State)
	}
	if !validDigest(enablement.ObservedIdentityDigest) || enablement.ConflictReplacements == nil || enablement.EnabledAt.IsZero() {
		return errors.New("enablement identity, conflicts, and enabledAt are required")
	}
	if err := ValidateUnverifiedAppTrustSet(enablement.UnverifiedAppTrust); err != nil {
		return err
	}
	if err := validateText("enablement reason", enablement.Reason, 1, MaxDescriptionBytes); err != nil {
		return err
	}
	return nil
}

func (s Store) LoadEnablement(profile, packID string) (Enablement, error) {
	if err := validateStorageID("profile", profile); err != nil {
		return Enablement{}, err
	}
	if err := validatePackID(packID); err != nil {
		return Enablement{}, err
	}
	return s.loadEnablementUnlocked(profile, packID)
}

func (s Store) ResolveEnablement(profile, packID string, context EffectivePermissionContext) (Enablement, error) {
	enablement, err := s.LoadEnablement(profile, packID)
	if err != nil {
		return Enablement{}, err
	}
	registry, err := s.LoadRegistry()
	if err != nil {
		return Enablement{}, err
	}
	resolved, err := s.resolveRevisionSnapshot(registry, packID, enablement.RevisionID)
	if err != nil {
		return Enablement{}, err
	}
	if err := ValidateEnablement(enablement, resolved.revision, resolved.manifest, context); err != nil {
		return Enablement{}, err
	}
	return enablement, nil
}

func (s Store) loadEnablementUnlocked(profile, packID string) (Enablement, error) {
	data, err := os.ReadFile(s.EnablementPath(profile, packID))
	if err != nil {
		return Enablement{}, err
	}
	var enablement Enablement
	if err := decodeStrict(data, &enablement); err != nil {
		return Enablement{}, err
	}
	if err := validateEnablementShape(enablement); err != nil {
		return Enablement{}, err
	}
	return enablement, nil
}

func (s Store) ListEnablements(profile string) ([]Enablement, error) {
	if err := validateStorageID("profile", profile); err != nil {
		return nil, err
	}
	dir := filepath.Dir(s.EnablementPath(profile, "placeholder.pack"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Enablement{}, nil
		}
		return nil, err
	}
	out := make([]Enablement, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var enablement Enablement
		if err := decodeStrict(data, &enablement); err != nil {
			return nil, err
		}
		if err := validateEnablementShape(enablement); err != nil {
			return nil, err
		}
		out = append(out, enablement)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PackID < out[j].PackID })
	return out, nil
}

func (s Store) RevokeRevision(packID, revisionID, reason string) error {
	if err := validateText("reason", reason, 1, MaxDescriptionBytes); err != nil {
		return err
	}
	return s.withLock(func() error {
		registry, err := s.loadRegistryUnlocked()
		if err != nil {
			return err
		}
		packIndex := registryPackIndex(registry, packID)
		if packIndex < 0 {
			return fmt.Errorf("host-app pack %q is not installed", packID)
		}
		revIndex := revisionIndex(registry.Packs[packIndex], revisionID)
		if revIndex < 0 {
			return fmt.Errorf("host-app revision %q is not installed", revisionID)
		}
		registry.Packs[packIndex].Revisions[revIndex].State = RevisionRevoked
		if registry.Packs[packIndex].ActiveRevisionID == revisionID {
			registry.Packs[packIndex].ActiveRevisionID = ""
			registry.Packs[packIndex].State = PackRevoked
		}
		if err := s.saveRegistryUnlocked(registry); err != nil {
			return err
		}
		return s.revokeEnablementsUnlocked(packID, revisionID, reason)
	})
}

func (s Store) RemovePack(packID, reason string) error {
	if err := validateText("reason", reason, 1, MaxDescriptionBytes); err != nil {
		return err
	}
	return s.withLock(func() error {
		registry, err := s.loadRegistryUnlocked()
		if err != nil {
			return err
		}
		packIndex := registryPackIndex(registry, packID)
		if packIndex < 0 {
			return fmt.Errorf("host-app pack %q is not installed", packID)
		}
		packDir := filepath.Join(s.PacksDir(), packID)
		trash := filepath.Join(s.Root, fmt.Sprintf(".remove-%s-%d", packID, time.Now().UnixNano()))
		moved := false
		if err := os.Rename(packDir, trash); err == nil {
			moved = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		registry.Packs[packIndex].State = PackRemoved
		registry.Packs[packIndex].ActiveRevisionID = ""
		if err := s.saveRegistryUnlocked(registry); err != nil {
			if moved {
				_ = os.Rename(trash, packDir)
			}
			return err
		}
		if err := s.revokePackEnablementsUnlocked(packID, reason); err != nil {
			return err
		}
		if moved {
			return os.RemoveAll(trash)
		}
		return nil
	})
}

func (s Store) revokePackEnablementsUnlocked(packID, reason string) error {
	root := filepath.Join(s.Root, "enablements")
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var enablement Enablement
		if err := decodeStrict(data, &enablement); err != nil {
			return err
		}
		if enablement.PackID != packID {
			return nil
		}
		enablement.State = EnablementRevoked
		enablement.Reason = reason
		return atomicWriteJSON(path, enablement)
	})
}

func (s Store) revokeEnablementsUnlocked(packID, revisionID, reason string) error {
	root := filepath.Join(s.Root, "enablements")
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var enablement Enablement
		if err := decodeStrict(data, &enablement); err != nil {
			return err
		}
		if enablement.PackID != packID || enablement.RevisionID != revisionID {
			return nil
		}
		enablement.State = EnablementRevoked
		enablement.Reason = reason
		return atomicWriteJSON(path, enablement)
	})
}

func (s Store) ensurePrivateRoot() error {
	if strings.TrimSpace(s.Root) == "" {
		return errors.New("host-app pack store root is required")
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return err
	}
	return os.Chmod(s.Root, 0o700)
}

func (s Store) withLock(fn func() error) error {
	if err := s.ensurePrivateRoot(); err != nil {
		return err
	}
	lock, err := os.OpenFile(s.LockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := os.Chmod(s.LockPath(), 0o600); err != nil {
		return err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return err
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	return fn()
}

func atomicWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".atomic-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	cleanup = false
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON file must contain one value")
		}
		return fmt.Errorf("JSON file has malformed trailing data: %w", err)
	}
	return nil
}

func validateRevision(revision Revision) error {
	if revision.RevisionID == "" || revision.PackID == "" || !validDigest(revision.SourceDigest) || !validDigest(revision.ManifestDigest) || !validDigest(revision.BasePermissionFingerprint) {
		return errors.New("revision identity and digests are required")
	}
	if err := validateSourceLock(revision.Source); err != nil {
		return err
	}
	if revision.ValidationStatus != ValidationPassed && revision.ValidationStatus != ValidationFailed {
		return fmt.Errorf("validationStatus %q is unsupported", revision.ValidationStatus)
	}
	if revision.TestStatus != TestNotRun && revision.TestStatus != TestPassed && revision.TestStatus != TestFailed {
		return fmt.Errorf("testStatus %q is unsupported", revision.TestStatus)
	}
	if revision.State != RevisionInstalled && revision.State != RevisionRevoked {
		return fmt.Errorf("state %q is unsupported", revision.State)
	}
	if revision.InstalledAt.IsZero() {
		return errors.New("installedAt is required")
	}
	return nil
}

func validateSourceLock(source SourceLock) error {
	if source.AcquiredAt.IsZero() {
		return errors.New("source acquiredAt is required")
	}
	switch source.Kind {
	case packsnapshot.SourceLocal:
		if source.LocalPath == "" || source.URL != "" || source.Commit != "" {
			return errors.New("local source lock requires only localPath")
		}
	case packsnapshot.SourceGit:
		if source.URL == "" || !packsnapshot.IsFullCommit(source.Commit) || source.LocalPath != "" {
			return errors.New("git source lock requires url and exact commit")
		}
	default:
		return fmt.Errorf("source kind %q is unsupported", source.Kind)
	}
	return nil
}

func validateTestResult(result TestResult) error {
	if result.SchemaVersion != TestResultVersion || result.RecordedAt.IsZero() {
		return errors.New("test result identity is incomplete")
	}
	if err := validateStorageID("test result id", result.ID); err != nil {
		return err
	}
	if err := validatePackID(result.PackID); err != nil {
		return fmt.Errorf("test result packId: %w", err)
	}
	if err := validateStorageID("test result revisionId", result.RevisionID); err != nil {
		return err
	}
	if result.Passed < 0 || result.Passed > MaxTests || result.Failed < 0 || result.Failed > MaxTests || result.Passed+result.Failed > MaxTests {
		return fmt.Errorf("test result counts must describe at most %d vectors", MaxTests)
	}
	if len(result.Results) > MaxTests || len(result.Failures) > MaxTests {
		return fmt.Errorf("test result details must contain at most %d entries", MaxTests)
	}
	switch result.Status {
	case TestNotRun:
		if result.Passed != 0 || result.Failed != 0 || len(result.Results) != 0 || len(result.Failures) != 0 {
			return errors.New("not-run test result must not claim executed vectors")
		}
	case TestPassed:
		if result.Passed == 0 || result.Failed != 0 || len(result.Failures) != 0 {
			return errors.New("passed test result counts contradict status")
		}
	case TestFailed:
		if result.Failed == 0 || len(result.Failures) > result.Failed {
			return errors.New("failed test result counts contradict status")
		}
	default:
		return fmt.Errorf("test result status %q is unsupported", result.Status)
	}
	if len(result.Results) > 0 && len(result.Results) != result.Passed+result.Failed {
		return errors.New("test result outcome count contradicts summary counts")
	}
	seen := make(map[string]struct{}, len(result.Results))
	passed, failed := 0, 0
	for i, outcome := range result.Results {
		if err := validateLocalID(outcome.ID); err != nil {
			return fmt.Errorf("test result results[%d].id: %w", i, err)
		}
		if _, exists := seen[outcome.ID]; exists {
			return fmt.Errorf("test result results[%d].id duplicates %q", i, outcome.ID)
		}
		seen[outcome.ID] = struct{}{}
		switch outcome.Status {
		case TestPassed:
			if outcome.Reason != "" {
				return fmt.Errorf("test result results[%d] passed with a failure reason", i)
			}
			passed++
		case TestFailed:
			if err := validateText(fmt.Sprintf("test result results[%d].reason", i), outcome.Reason, 1, MaxDescriptionBytes); err != nil {
				return err
			}
			failed++
		default:
			return fmt.Errorf("test result results[%d].status %q is unsupported", i, outcome.Status)
		}
	}
	if len(result.Results) > 0 && (passed != result.Passed || failed != result.Failed) {
		return errors.New("test result outcome statuses contradict summary counts")
	}
	for i, failure := range result.Failures {
		if err := validateText(fmt.Sprintf("test result failures[%d]", i), failure, 1, MaxDescriptionBytes); err != nil {
			return err
		}
	}
	return nil
}

func validateEnablementShape(enablement Enablement) error {
	if enablement.Schema != EnablementVersion || enablement.Profile == "" || enablement.PackID == "" || enablement.RevisionID == "" || len(enablement.BindingIDs) == 0 {
		return errors.New("enablement identity is incomplete")
	}
	if !validDigest(enablement.SourceDigest) || !validDigest(enablement.BasePermissionFingerprint) || !validDigest(enablement.PermissionFingerprint) || !validDigest(enablement.ObservedIdentityDigest) || enablement.ConflictReplacements == nil || enablement.EnabledAt.IsZero() {
		return errors.New("enablement digests, conflicts, and enabledAt are required")
	}
	if err := ValidateUnverifiedAppTrustSet(enablement.UnverifiedAppTrust); err != nil {
		return err
	}
	if err := validateText("enablement reason", enablement.Reason, 1, MaxDescriptionBytes); err != nil {
		return err
	}
	return nil
}

func normalizeEnablement(enablement Enablement) Enablement {
	enablement.BindingIDs = sortedCopy(enablement.BindingIDs)
	SortUnverifiedAppTrust(enablement.UnverifiedAppTrust)
	return enablement
}

func normalizeRegistry(registry *Registry) {
	for i := range registry.Packs {
		sort.Slice(registry.Packs[i].Revisions, func(a, b int) bool {
			return registry.Packs[i].Revisions[a].RevisionID < registry.Packs[i].Revisions[b].RevisionID
		})
	}
	sort.Slice(registry.Packs, func(i, j int) bool { return registry.Packs[i].ID < registry.Packs[j].ID })
}

func registryPackIndex(registry Registry, packID string) int {
	for i := range registry.Packs {
		if registry.Packs[i].ID == packID {
			return i
		}
	}
	return -1
}

func revisionIndex(entry RegistryEntry, revisionID string) int {
	for i := range entry.Revisions {
		if entry.Revisions[i].RevisionID == revisionID {
			return i
		}
	}
	return -1
}

func snapshotSourceLock(source packsnapshot.SourceLock, acquiredAt time.Time) SourceLock {
	return SourceLock{Kind: source.Kind, LocalPath: source.Path, URL: source.URL, Commit: source.Commit, AcquiredAt: acquiredAt}
}

func validateStorageID(label, value string) error {
	if len(value) == 0 || len(value) > MaxStorageIDBytes || strings.TrimSpace(value) != value || strings.ContainsAny(value, `/\\`) || value == "." || value == ".." || !safePrintable(value) {
		return fmt.Errorf("%s must be a bounded storage-safe identifier", label)
	}
	return nil
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, r := range value[len("sha256:"):] {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return false
	}
	return true
}
