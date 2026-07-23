package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/cmdproxy"
	"github.com/vibe-agi/hideout/internal/profile"
)

const maxProjectionReadinessManifestBytes = 256 << 10

type projectionCatalogReviewEntry struct {
	Name           string   `json:"name"`
	Aliases        []string `json:"aliases,omitempty"`
	Action         string   `json:"action"`
	ArgvSchema     string   `json:"argvSchema"`
	StreamPolicy   string   `json:"streamPolicy"`
	DefaultMode    string   `json:"defaultMode"`
	AllowedTargets []string `json:"allowedTargets"`
	OwnerType      string   `json:"ownerType,omitempty"`
	AdapterID      string   `json:"adapterId,omitempty"`
	BindingDigest  string   `json:"bindingDigest,omitempty"`
}

func BuildProjectionCatalogReviewDigest(registry cmdproxy.Registry) (string, error) {
	registrations := registry.Registrations()
	entries := make([]projectionCatalogReviewEntry, 0, len(registrations))
	for _, registration := range registrations {
		entry := projectionCatalogReviewEntry{
			Name: registration.Name, Aliases: append([]string(nil), registration.Aliases...),
			Action: registration.Action, ArgvSchema: registration.ArgvSchema,
			StreamPolicy: registration.StreamPolicy, DefaultMode: registration.DefaultMode,
			AllowedTargets: append([]string(nil), registration.AllowedTargets...),
			OwnerType:      registration.OwnerType, AdapterID: registration.AdapterID,
			BindingDigest: registration.BindingDigest,
		}
		sort.Strings(entry.Aliases)
		sort.Strings(entry.AllowedTargets)
		entries = append(entries, entry)
	}
	canonical, err := json.Marshal(struct {
		Schema  string                         `json:"schema"`
		Entries []projectionCatalogReviewEntry `json:"entries"`
	}{
		Schema: "hideout.projection-catalog-review/v1", Entries: entries,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (c Core) projectionCatalogReviewDigest(
	profileName, workspace string,
	runtimeProfile profile.Profile,
) (string, error) {
	registry, err := cmdproxy.RegistryFromProfile(runtimeProfile)
	if err != nil {
		return "", err
	}
	_, hostAppRegistrations, err := c.CompileHostAppCatalog(
		profileName, "review", []string{workspace},
	)
	if err != nil {
		return "", err
	}
	registry, err = cmdproxy.WithProjection(registry, hostAppRegistrations...)
	if err != nil {
		return "", err
	}
	return BuildProjectionCatalogReviewDigest(registry)
}

func BuildProjectionReadinessManifest(
	sessionID, environmentID, sessionSnapshotID, shimDir string,
	registry cmdproxy.Registry,
) (backend.ProjectionReadinessManifest, error) {
	names := append([]string{"hideout-shim"}, registry.ShimNames()...)
	sort.Strings(names)
	if len(names) == 0 || len(names) > backend.MaxProjectionReadinessEntries {
		return backend.ProjectionReadinessManifest{}, fmt.Errorf(
			"projection readiness catalog contains %d entries, limit %d",
			len(names), backend.MaxProjectionReadinessEntries,
		)
	}
	entries := make([]backend.ProjectionReadinessEntry, 0, len(names))
	previous := ""
	for _, name := range names {
		if name == previous {
			return backend.ProjectionReadinessManifest{}, fmt.Errorf("duplicate projection readiness entry %q", name)
		}
		previous = name
		entry, err := projectionReadinessEntry(shimDir, name)
		if err != nil {
			return backend.ProjectionReadinessManifest{}, err
		}
		entries = append(entries, entry)
	}
	manifest := backend.ProjectionReadinessManifest{
		Schema: backend.ProjectionReadinessManifestSchema, SessionID: sessionID,
		EnvironmentID: environmentID, SessionSnapshotID: sessionSnapshotID, Entries: entries,
	}
	catalogDigest, err := backend.ProjectionReadinessCatalogDigest(manifest)
	if err != nil {
		return backend.ProjectionReadinessManifest{}, err
	}
	manifest.CatalogDigest = catalogDigest
	if err := manifest.Validate(); err != nil {
		return backend.ProjectionReadinessManifest{}, err
	}
	return manifest, nil
}

func MaterializeProjectionReadiness(
	runtimeSessionDir, sessionID, environmentID, sessionSnapshotID string,
	targetCommand []string, registry cmdproxy.Registry,
) (*backend.ProjectionReadinessExpectation, error) {
	if len(targetCommand) == 0 {
		return nil, errors.New("projection readiness target command is required")
	}
	manifest, err := BuildProjectionReadinessManifest(
		sessionID, environmentID, sessionSnapshotID,
		filepath.Join(runtimeSessionDir, "shims"), registry,
	)
	if err != nil {
		return nil, err
	}
	if err := WriteProjectionReadinessManifest(
		filepath.Join(runtimeSessionDir, backend.ProjectionReadinessManifestFile),
		manifest,
	); err != nil {
		return nil, err
	}
	_, targetProjected := registry.LookupExact(targetCommand[0])
	expectation := &backend.ProjectionReadinessExpectation{
		Manifest: manifest, ManifestRelativePath: backend.ProjectionReadinessManifestFile,
		TargetProjected: targetProjected, Deadline: backend.MaxProjectionReadinessDeadline,
	}
	if err := expectation.Validate(); err != nil {
		return nil, err
	}
	return expectation, nil
}

func projectionReadinessEntry(shimDir, name string) (backend.ProjectionReadinessEntry, error) {
	path := filepath.Join(shimDir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return backend.ProjectionReadinessEntry{}, fmt.Errorf("projection readiness entry %q: %w", name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return backend.ProjectionReadinessEntry{}, fmt.Errorf("projection readiness entry %q must be a regular non-symlink executable", name)
	}
	file, err := os.Open(path)
	if err != nil {
		return backend.ProjectionReadinessEntry{}, fmt.Errorf("projection readiness entry %q: %w", name, err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(file, backend.MaxProjectionReadinessEntryBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return backend.ProjectionReadinessEntry{}, errors.Join(copyErr, closeErr)
	}
	if info.Size() > backend.MaxProjectionReadinessEntryBytes {
		return backend.ProjectionReadinessEntry{}, fmt.Errorf("projection readiness entry %q exceeds the bounded size", name)
	}
	kind := backend.ProjectionEntryCommand
	if name == "hideout-shim" {
		kind = backend.ProjectionEntryDispatcher
	}
	return backend.ProjectionReadinessEntry{
		Name: name, RelativePath: name,
		SHA256: "sha256:" + hex.EncodeToString(hash.Sum(nil)), Kind: kind,
	}, nil
}

func WriteProjectionReadinessManifest(path string, manifest backend.ProjectionReadinessManifest) (retErr error) {
	if err := manifest.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxProjectionReadinessManifestBytes {
		return errors.New("projection readiness manifest exceeds the bounded size")
	}
	dir := filepath.Dir(path)
	file, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	closed := false
	defer func() {
		if !closed {
			retErr = errors.Join(retErr, file.Close())
		}
		if removeErr := os.Remove(tempPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			retErr = errors.Join(retErr, removeErr)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Link(tempPath, path); err != nil {
		return fmt.Errorf("publish projection readiness manifest: %w", err)
	}
	return nil
}

func ReadProjectionReadinessManifest(path string) (backend.ProjectionReadinessManifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return backend.ProjectionReadinessManifest{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 ||
		info.Size() <= 0 || info.Size() > maxProjectionReadinessManifestBytes {
		return backend.ProjectionReadinessManifest{}, errors.New("projection readiness manifest must be a private bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return backend.ProjectionReadinessManifest{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxProjectionReadinessManifestBytes+1))
	decoder.DisallowUnknownFields()
	var manifest backend.ProjectionReadinessManifest
	if err := decoder.Decode(&manifest); err != nil {
		return backend.ProjectionReadinessManifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return backend.ProjectionReadinessManifest{}, errors.New("projection readiness manifest contains trailing content")
		}
		return backend.ProjectionReadinessManifest{}, err
	}
	if err := manifest.Validate(); err != nil {
		return backend.ProjectionReadinessManifest{}, err
	}
	if err := manifest.ValidateCatalogDigest(); err != nil {
		return backend.ProjectionReadinessManifest{}, err
	}
	return manifest, nil
}

func projectionReadinessReadyDetails(proof backend.SessionReadyProof) map[string]any {
	return map[string]any{
		"status":          backend.ProjectionReadinessReady,
		"catalogDigest":   proof.ProjectionCatalogDigest,
		"expectedEntries": proof.ProjectionExpectedEntries,
		"observedEntries": proof.ProjectionObservedEntries,
		"durationMs":      proof.ProjectionDurationMillis,
		"targetProjected": proof.ProjectionTargetProjected,
	}
}

func projectionReadinessFailureDetails(
	expectation *backend.ProjectionReadinessExpectation,
	readinessErr *backend.ProjectionReadinessError,
) map[string]any {
	details := map[string]any{
		"status":          backend.ProjectionReadinessRefused,
		"reasonCode":      backend.ProjectionReadinessEntryInvalid,
		"hint":            projectionReadinessRecoveryHint(backend.ProjectionReadinessEntryInvalid),
		"catalogDigest":   "",
		"expectedEntries": 0,
		"observedEntries": 0,
		"durationMs":      int64(0),
		"targetProjected": false,
	}
	if expectation != nil {
		details["catalogDigest"] = expectation.Manifest.CatalogDigest
		details["expectedEntries"] = len(expectation.Manifest.Entries)
		details["targetProjected"] = expectation.TargetProjected
	}
	if readinessErr != nil {
		details["status"] = readinessErr.Status
		details["reasonCode"] = readinessErr.ReasonCode
		details["hint"] = projectionReadinessRecoveryHint(readinessErr.ReasonCode)
	}
	return details
}

func projectionReadinessRecoveryHint(reason backend.ProjectionReadinessReason) string {
	switch reason {
	case backend.ProjectionReadinessManifestMissing,
		backend.ProjectionReadinessCatalogDrift,
		backend.ProjectionReadinessEntryMissing,
		backend.ProjectionReadinessEntryInvalid,
		backend.ProjectionReadinessDigestMismatch:
		return "rebuild the session projection and retry"
	case backend.ProjectionReadinessIdentityDrift:
		return "retry against the current environment identity"
	case backend.ProjectionReadinessTimeout:
		return "check environment projection visibility and retry"
	case backend.ProjectionReadinessCancellation:
		return "retry when session startup can complete"
	default:
		return "check the session projection and retry"
	}
}
