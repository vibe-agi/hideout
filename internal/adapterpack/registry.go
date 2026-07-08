package adapterpack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Store struct {
	Root string
}

func NewStore(root string) Store {
	return Store{Root: filepath.Join(root, "adapter-packs")}
}

func (s Store) RegistryPath() string {
	return filepath.Join(s.Root, "registry.json")
}

func (s Store) SourceDir(packID, revisionID string) string {
	return filepath.Join(s.Root, "packs", packID, revisionID, "source")
}

func (s Store) TestResultPath(packID, revisionID string) string {
	return filepath.Join(s.Root, "packs", packID, revisionID, "test-result.json")
}

func (s Store) Load() (Registry, error) {
	data, err := os.ReadFile(s.RegistryPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Registry{SchemaVersion: RegistryVersion, Packs: map[string]RegistryEntry{}}, nil
		}
		return Registry{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var registry Registry
	if err := dec.Decode(&registry); err != nil {
		return Registry{}, err
	}
	if err := ValidateRegistry(registry); err != nil {
		return Registry{}, err
	}
	return registry, nil
}

func (s Store) Save(registry Registry) error {
	if err := ValidateRegistry(registry); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Root, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(s.Root, ".registry-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, s.RegistryPath()); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func ValidateRegistry(registry Registry) error {
	if registry.SchemaVersion == "" {
		registry.SchemaVersion = RegistryVersion
	}
	if registry.SchemaVersion != RegistryVersion {
		return fmt.Errorf("unsupported adapter pack registry schema %q", registry.SchemaVersion)
	}
	for packID, entry := range registry.Packs {
		if err := ValidatePackID(packID); err != nil {
			return fmt.Errorf("packs.%s: %w", packID, err)
		}
		if entry.PackID != packID {
			return fmt.Errorf("packs.%s.packId must match key", packID)
		}
		switch entry.State {
		case StateInstalled, StateRevoked, StateBuiltin:
		default:
			return fmt.Errorf("packs.%s.state %q is unsupported", packID, entry.State)
		}
		if !entry.BuiltIn && len(entry.Revisions) == 0 {
			return fmt.Errorf("packs.%s.revisions is required", packID)
		}
		for revisionID, rev := range entry.Revisions {
			if revisionID == "" || rev.RevisionID != revisionID {
				return fmt.Errorf("packs.%s.revisions.%s.revisionId must match key", packID, revisionID)
			}
			if rev.SourceDigest == "" || rev.ManifestDigest == "" {
				return fmt.Errorf("packs.%s.revisions.%s digests are required", packID, revisionID)
			}
		}
	}
	return nil
}

func (s Store) Install(spec SourceSpec) (RegistryEntry, Revision, error) {
	if strings.TrimSpace(s.Root) == "" {
		return RegistryEntry{}, Revision{}, errors.New("adapter pack store root is required")
	}
	registry, err := s.Load()
	if err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	staging := filepath.Join(s.Root, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	tmpDest := filepath.Join(staging, "source")
	manifest, rev, err := LockSource(s.Root, spec, tmpDest)
	if err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	rev.InstalledAt = time.Now().UTC()
	entry := registry.Packs[manifest.ID]
	if entry.PackID == "" {
		entry = RegistryEntry{PackID: manifest.ID, State: StateInstalled, Revisions: map[string]Revision{}, Description: manifest.Description}
	}
	if entry.BuiltIn {
		return RegistryEntry{}, Revision{}, fmt.Errorf("adapter pack %q is a built-in metadata entry and cannot be overwritten", manifest.ID)
	}
	entry.State = StateInstalled
	entry.ActiveRevisionID = rev.RevisionID
	entry.Description = manifest.Description
	if entry.Revisions == nil {
		entry.Revisions = map[string]Revision{}
	}
	final := s.SourceDir(manifest.ID, rev.RevisionID)
	_ = os.RemoveAll(final)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	if err := os.Rename(tmpDest, final); err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	_ = os.RemoveAll(staging)
	entry.Revisions[rev.RevisionID] = rev
	registry.Packs[manifest.ID] = entry
	if err := s.Save(registry); err != nil {
		return RegistryEntry{}, Revision{}, err
	}
	return entry, rev, nil
}

func (s Store) Inspect(packID string) (RegistryEntry, error) {
	registry, err := s.Load()
	if err != nil {
		return RegistryEntry{}, err
	}
	entry, ok := registry.Packs[packID]
	if !ok {
		return RegistryEntry{}, fmt.Errorf("adapter pack %q is not installed", packID)
	}
	return entry, nil
}

func (s Store) List() ([]RegistryEntry, error) {
	registry, err := s.Load()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(registry.Packs))
	for id := range registry.Packs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]RegistryEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, registry.Packs[id])
	}
	return out, nil
}

func (s Store) SaveTestResult(packID, revisionID string, result TestResult) error {
	path := s.TestResultPath(packID, revisionID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	registry, err := s.Load()
	if err != nil {
		return err
	}
	entry := registry.Packs[packID]
	rev := entry.Revisions[revisionID]
	rev.TestResultID = result.ID
	rev.TestStatus = result.Status
	entry.Revisions[revisionID] = rev
	registry.Packs[packID] = entry
	return s.Save(registry)
}

func (s Store) ResolveRuntime(packID, revisionID, adapterID, lockDigest string) (RuntimeSource, error) {
	entry, err := s.Inspect(packID)
	if err != nil {
		return RuntimeSource{}, err
	}
	if entry.State == StateRevoked {
		return RuntimeSource{}, fmt.Errorf("adapter pack %q is revoked", packID)
	}
	rev, ok := entry.Revisions[revisionID]
	if !ok {
		return RuntimeSource{}, fmt.Errorf("adapter pack %q revision %q is not installed", packID, revisionID)
	}
	if lockDigest != "" && lockDigest != rev.SourceDigest {
		return RuntimeSource{}, fmt.Errorf("adapter pack %q revision %q digest mismatch", packID, revisionID)
	}
	root := s.SourceDir(packID, revisionID)
	if got, err := DigestTree(root); err != nil {
		return RuntimeSource{}, err
	} else if got != rev.SourceDigest {
		return RuntimeSource{}, fmt.Errorf("adapter pack %q revision %q source digest mismatch: expected %s got %s", packID, revisionID, rev.SourceDigest, got)
	}
	manifest, _, err := LoadManifest(filepath.Join(root, ManifestFileName))
	if err != nil {
		return RuntimeSource{}, err
	}
	adapter, ok := FindAdapter(manifest, adapterID)
	if !ok {
		return RuntimeSource{}, fmt.Errorf("adapter pack %q revision %q adapter %q is not present", packID, revisionID, adapterID)
	}
	path := filepath.Join(root, filepath.FromSlash(adapter.Script))
	source, err := os.ReadFile(path)
	if err != nil {
		return RuntimeSource{}, err
	}
	digest := DigestBytes(source)
	if expected := rev.AdapterDigests[adapterID]; expected != "" && expected != digest {
		return RuntimeSource{}, fmt.Errorf("adapter pack %q adapter %q digest mismatch: expected %s got %s", packID, adapterID, expected, digest)
	}
	return RuntimeSource{
		Source:                      string(source),
		Digest:                      digest,
		Path:                        path,
		Entrypoint:                  firstNonEmpty(adapter.Entrypoint, "decideCommandAdapter"),
		Commands:                    append([]string(nil), adapter.Commands...),
		AllowedProposalCapabilities: append([]string(nil), adapter.AllowedProposalCapabilities...),
		Description:                 adapter.Description,
		PackID:                      packID,
		RevisionID:                  revisionID,
		AdapterID:                   adapterID,
		LockDigest:                  rev.SourceDigest,
	}, nil
}

func FindAdapter(manifest Manifest, adapterID string) (AdapterSpec, bool) {
	for _, adapter := range manifest.Adapters {
		if adapter.ID == adapterID {
			return adapter, true
		}
	}
	return AdapterSpec{}, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
