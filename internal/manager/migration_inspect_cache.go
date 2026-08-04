package manager

import (
	"errors"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/migration"
)

var ErrMigrationInspectionRequired = errors.New("migration bundle must be inspected again")

const (
	defaultMigrationInspectionCacheTTL = 30 * time.Minute
	maximumMigrationInspectionCacheTTL = 2 * time.Hour
	defaultMigrationInspectionEntries  = 32
	maximumMigrationInspectionEntries  = 128
)

type MigrationInspectionCacheOptions struct {
	Now        func() time.Time
	TTL        time.Duration
	MaxEntries int
}

type migrationInspectionCacheEntry struct {
	inspection migration.SealedBundleInspection
	bundleFile MigrationBundleFileBinding
	expiresAt  time.Time
}

// MigrationInspectionCache retains only an authenticated, secret-free manifest
// in daemon memory. It never stores a passphrase or record plaintext and has no
// serialization method; restart intentionally requires inspection again.
type MigrationInspectionCache struct {
	mu         sync.Mutex
	entries    map[string]migrationInspectionCacheEntry
	now        func() time.Time
	ttl        time.Duration
	maxEntries int
	closed     bool
}

func NewMigrationInspectionCache(
	options MigrationInspectionCacheOptions,
) *MigrationInspectionCache {
	ttl := options.TTL
	if ttl <= 0 {
		ttl = defaultMigrationInspectionCacheTTL
	}
	if ttl > maximumMigrationInspectionCacheTTL {
		ttl = maximumMigrationInspectionCacheTTL
	}
	maximum := options.MaxEntries
	if maximum <= 0 {
		maximum = defaultMigrationInspectionEntries
	}
	if maximum > maximumMigrationInspectionEntries {
		maximum = maximumMigrationInspectionEntries
	}
	return &MigrationInspectionCache{
		entries: make(map[string]migrationInspectionCacheEntry),
		now:     options.Now, ttl: ttl, maxEntries: maximum,
	}
}

func (cache *MigrationInspectionCache) Put(
	inspection migration.SealedBundleInspection,
	bundleFile MigrationBundleFileBinding,
) error {
	if cache == nil || bundleFile.Validate() != nil ||
		validateSealedBundleInspection(inspection) != nil {
		return ErrMigrationInspectionRequired
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.closed {
		return ErrMigrationInspectionRequired
	}
	now := cache.nowUTC()
	cache.purgeExpiredLocked(now)
	key := migrationInspectionCacheKey(inspection.Binding)
	if _, exists := cache.entries[key]; !exists && len(cache.entries) >= cache.maxEntries {
		cache.evictEarliestLocked()
	}
	cache.entries[key] = migrationInspectionCacheEntry{
		inspection: cloneSealedBundleInspection(inspection),
		bundleFile: bundleFile, expiresAt: now.Add(cache.ttl),
	}
	return nil
}

func (cache *MigrationInspectionCache) Get(
	binding migration.BundleBinding,
	bundleFile MigrationBundleFileBinding,
) (migration.SealedBundleInspection, error) {
	if cache == nil || validateMigrationBundleBinding(binding) != nil ||
		bundleFile.Validate() != nil {
		return migration.SealedBundleInspection{}, ErrMigrationInspectionRequired
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.closed {
		return migration.SealedBundleInspection{}, ErrMigrationInspectionRequired
	}
	now := cache.nowUTC()
	cache.purgeExpiredLocked(now)
	entry, exists := cache.entries[migrationInspectionCacheKey(binding)]
	if !exists || entry.bundleFile != bundleFile || entry.inspection.Binding != binding {
		return migration.SealedBundleInspection{}, ErrMigrationInspectionRequired
	}
	return cloneSealedBundleInspection(entry.inspection), nil
}

func (cache *MigrationInspectionCache) Close() {
	if cache == nil {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for key := range cache.entries {
		delete(cache.entries, key)
	}
	cache.closed = true
}

func (cache *MigrationInspectionCache) purgeExpiredLocked(now time.Time) {
	for key, entry := range cache.entries {
		if !now.Before(entry.expiresAt) {
			delete(cache.entries, key)
		}
	}
}

func (cache *MigrationInspectionCache) evictEarliestLocked() {
	var selected string
	var earliest time.Time
	for key, entry := range cache.entries {
		if selected == "" || entry.expiresAt.Before(earliest) ||
			(entry.expiresAt.Equal(earliest) && key < selected) {
			selected, earliest = key, entry.expiresAt
		}
	}
	if selected != "" {
		delete(cache.entries, selected)
	}
}

func (cache *MigrationInspectionCache) nowUTC() time.Time {
	if cache.now != nil {
		return cache.now().UTC()
	}
	return time.Now().UTC()
}

func migrationInspectionCacheKey(binding migration.BundleBinding) string {
	return string(binding.BundleID) + "\x00" + string(binding.FileDigest) + "\x00" +
		string(binding.ManifestDigest) + "\x00" + string(binding.CompletionDigest)
}

func cloneSealedBundleInspection(
	inspection migration.SealedBundleInspection,
) migration.SealedBundleInspection {
	out := inspection
	out.Manifest = cloneMigrationManifest(inspection.Manifest)
	return out
}

func cloneMigrationManifest(manifest migration.Manifest) migration.Manifest {
	out := manifest
	out.Environments = make([]migration.EnvironmentSnapshot, len(manifest.Environments))
	for index, environment := range manifest.Environments {
		out.Environments[index] = environment
		if environment.ImageProvenance != nil {
			provenance := *environment.ImageProvenance
			out.Environments[index].ImageProvenance = &provenance
		}
		out.Environments[index].WorkspaceProposals = append(
			[]migration.WorkspaceProposal(nil), environment.WorkspaceProposals...,
		)
		out.Environments[index].AuthorityProposalRefs = append(
			[]migration.OpaqueID(nil), environment.AuthorityProposalRefs...,
		)
		out.Environments[index].GuestIdentityEvidence.SSHHostKeyDigests = append(
			[]migration.Digest(nil), environment.GuestIdentityEvidence.SSHHostKeyDigests...,
		)
		out.Environments[index].DiskRefs = append(
			[]migration.OpaqueID(nil), environment.DiskRefs...,
		)
	}
	out.DiskObjects = make([]migration.DiskObject, len(manifest.DiskObjects))
	for index, disk := range manifest.DiskObjects {
		out.DiskObjects[index] = disk
		out.DiskObjects[index].Provider.Features = append(
			[]string(nil), disk.Provider.Features...,
		)
	}
	out.DiskEdges = append([]migration.DiskEdge(nil), manifest.DiskEdges...)
	out.SecretEntries = append([]migration.SecretEntry(nil), manifest.SecretEntries...)
	out.AuthorityProposals = append(
		[]migration.AuthorityProposal(nil), manifest.AuthorityProposals...,
	)
	out.ComponentIndex = append(
		[]migration.ComponentIndexEntry(nil), manifest.ComponentIndex...,
	)
	out.ExcludedClasses = append([]string(nil), manifest.ExcludedClasses...)
	out.RequiredCapabilities = append(
		[]migration.RequiredCapability(nil), manifest.RequiredCapabilities...,
	)
	return out
}
