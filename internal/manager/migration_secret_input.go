package manager

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"
	"time"

	"github.com/vibe-agi/hideout/internal/migration"
)

const (
	defaultMigrationSecretTTL     = 2 * time.Minute
	maximumMigrationSecretTTL     = 10 * time.Minute
	defaultMigrationSecretEntries = 128
	maximumMigrationSecretEntries = 1024
	migrationSecretHandleBytes    = 24
)

var (
	ErrMigrationSecretInputRequired = errors.New("migration secret input is required")
	ErrMigrationSecretInputExpired  = errors.New("migration secret input expired")
	ErrMigrationSecretInputMismatch = errors.New("migration secret input binding changed")
	ErrMigrationSecretInputCapacity = errors.New("migration secret input capacity exceeded")
	ErrMigrationSecretInputInvalid  = errors.New("migration secret input is invalid")

	migrationSecretHandlePattern = regexp.MustCompile(`^migh_[A-Za-z0-9_-]{32}$`)
)

type MigrationSecretPurpose string

const (
	MigrationSecretPurposeExportCreate MigrationSecretPurpose = "export-create"
	MigrationSecretPurposeExportResume MigrationSecretPurpose = "export-resume"
	MigrationSecretPurposeInspect      MigrationSecretPurpose = "inspect"
	MigrationSecretPurposeImport       MigrationSecretPurpose = "import"
)

// MigrationBundleFileBinding contains stable, non-path file identity. The path
// itself is hashed by the caller after it has performed its symlink and regular
// file checks.
type MigrationBundleFileBinding struct {
	PathDigest       migration.Digest `json:"pathDigest"`
	HeaderDigest     migration.Digest `json:"headerDigest"`
	Device           uint64           `json:"device"`
	Inode            uint64           `json:"inode"`
	Size             int64            `json:"size"`
	ModifiedUnixNano int64            `json:"modifiedUnixNano"`
}

func (binding MigrationBundleFileBinding) Validate() error {
	if binding.PathDigest.Validate() != nil || binding.HeaderDigest.Validate() != nil ||
		binding.Device == 0 || binding.Inode == 0 || binding.Size <= 0 ||
		binding.ModifiedUnixNano <= 0 {
		return ErrMigrationSecretInputInvalid
	}
	return nil
}

type MigrationSecretInputRequest struct {
	Purpose       MigrationSecretPurpose
	ClientBinding string
	BundleID      migration.BundleID
	BundleFile    *MigrationBundleFileBinding
	Passphrase    []byte
}

type MigrationSecretInputUse struct {
	Handle        string
	Purpose       MigrationSecretPurpose
	ClientBinding string
	BundleID      migration.BundleID
	BundleFile    *MigrationBundleFileBinding
}

// MigrationSecretInputLookup proves that a one-shot handle is currently bound
// to the requesting client and file without exposing or consuming its secret.
// Long-running apply uses this to create durable operation state, then the
// daemon worker consumes the same handle before deriving bundle keys.
type MigrationSecretInputLookup struct {
	Handle        string
	Purpose       MigrationSecretPurpose
	ClientBinding string
	BundleFile    *MigrationBundleFileBinding
}

type MigrationSecretInputHandle struct {
	Handle        string                 `json:"handle"`
	Purpose       MigrationSecretPurpose `json:"purpose"`
	BundleID      migration.BundleID     `json:"bundleId"`
	ExpiresAt     time.Time              `json:"expiresAt"`
	UsesRemaining uint8                  `json:"usesRemaining"`
}

func (handle MigrationSecretInputHandle) Validate() error {
	if !migrationSecretHandlePattern.MatchString(handle.Handle) ||
		!validMigrationSecretPurpose(handle.Purpose) ||
		handle.UsesRemaining != 1 ||
		!validMigrationTime(handle.ExpiresAt) {
		return ErrMigrationSecretInputInvalid
	}
	if _, err := migration.ParseBundleID(string(handle.BundleID)); err != nil {
		return ErrMigrationSecretInputInvalid
	}
	return nil
}

// MigrationSecretInputBinding is the non-secret portion of a protected input
// capability. It lets the daemon enqueue an exact resume worker without
// exposing or consuming the passphrase held by MigrationSecretInputStore.
type MigrationSecretInputBinding struct {
	Handle     MigrationSecretInputHandle
	BundleFile *MigrationBundleFileBinding
}

type MigrationSecretInputStoreOptions struct {
	Now        func() time.Time
	Random     io.Reader
	TTL        time.Duration
	MaxEntries int
}

type migrationSecretInputEntry struct {
	purpose      MigrationSecretPurpose
	clientDigest [sha256.Size]byte
	bundleID     migration.BundleID
	bundleFile   *MigrationBundleFileBinding
	expiresAt    time.Time
	passphrase   *migration.SensitiveBuffer
}

// MigrationSecretInputStore is intentionally memory-only. It exposes no list or
// serialization method, and Close (including daemon shutdown) clears every live
// passphrase before discarding the handles.
type MigrationSecretInputStore struct {
	mu         sync.Mutex
	entries    map[string]*migrationSecretInputEntry
	now        func() time.Time
	random     io.Reader
	ttl        time.Duration
	maxEntries int
	closed     bool
}

func NewMigrationSecretInputStore(
	options MigrationSecretInputStoreOptions,
) *MigrationSecretInputStore {
	ttl := options.TTL
	if ttl <= 0 {
		ttl = defaultMigrationSecretTTL
	}
	if ttl > maximumMigrationSecretTTL {
		ttl = maximumMigrationSecretTTL
	}
	maximum := options.MaxEntries
	if maximum <= 0 {
		maximum = defaultMigrationSecretEntries
	}
	if maximum > maximumMigrationSecretEntries {
		maximum = maximumMigrationSecretEntries
	}
	randomSource := options.Random
	if randomSource == nil {
		randomSource = rand.Reader
	}
	return &MigrationSecretInputStore{
		entries: make(map[string]*migrationSecretInputEntry),
		now:     options.Now, random: randomSource, ttl: ttl, maxEntries: maximum,
	}
}

func (store *MigrationSecretInputStore) Create(
	request MigrationSecretInputRequest,
) (MigrationSecretInputHandle, error) {
	if store == nil || !validMigrationSecretPurpose(request.Purpose) ||
		!validClientBinding(request.ClientBinding) ||
		len(request.Passphrase) == 0 || len(request.Passphrase) > migration.MaxPassphraseBytes {
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputInvalid
	}
	if _, err := migration.ParseBundleID(string(request.BundleID)); err != nil ||
		!migrationSecretFileBindingMatchesPurpose(request.Purpose, request.BundleFile) {
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputInvalid
	}
	passphrase, err := migration.NewSensitiveBuffer(request.Passphrase)
	if err != nil {
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputInvalid
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		passphrase.Clear()
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputRequired
	}
	now := store.nowUTC()
	store.purgeExpiredLocked(now)
	if len(store.entries) >= store.maxEntries {
		passphrase.Clear()
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputCapacity
	}
	handle, err := store.newHandleLocked()
	if err != nil {
		passphrase.Clear()
		return MigrationSecretInputHandle{}, err
	}
	expiresAt := now.Add(store.ttl)
	entry := &migrationSecretInputEntry{
		purpose: request.Purpose, clientDigest: migrationClientDigest(request.ClientBinding),
		bundleID: request.BundleID, bundleFile: cloneBundleFileBinding(request.BundleFile),
		expiresAt: expiresAt, passphrase: passphrase,
	}
	store.entries[handle] = entry
	return MigrationSecretInputHandle{
		Handle: handle, Purpose: request.Purpose, BundleID: request.BundleID,
		ExpiresAt: expiresAt, UsesRemaining: 1,
	}, nil
}

func (store *MigrationSecretInputStore) Lookup(
	lookup MigrationSecretInputLookup,
) (MigrationSecretInputHandle, error) {
	if store == nil || !migrationSecretHandlePattern.MatchString(lookup.Handle) ||
		!validMigrationSecretPurpose(lookup.Purpose) ||
		!validClientBinding(lookup.ClientBinding) ||
		!migrationSecretFileBindingMatchesPurpose(lookup.Purpose, lookup.BundleFile) {
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputRequired
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputRequired
	}
	entry, exists := store.entries[lookup.Handle]
	if !exists {
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputRequired
	}
	now := store.nowUTC()
	if !now.Before(entry.expiresAt) {
		entry.passphrase.Clear()
		delete(store.entries, lookup.Handle)
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputExpired
	}
	if entry.purpose != lookup.Purpose ||
		entry.clientDigest != migrationClientDigest(lookup.ClientBinding) ||
		!bundleFileBindingsEqual(entry.bundleFile, lookup.BundleFile) {
		entry.passphrase.Clear()
		delete(store.entries, lookup.Handle)
		return MigrationSecretInputHandle{}, ErrMigrationSecretInputMismatch
	}
	return MigrationSecretInputHandle{
		Handle: lookup.Handle, Purpose: entry.purpose, BundleID: entry.bundleID,
		ExpiresAt: entry.expiresAt, UsesRemaining: 1,
	}, nil
}

// ResolveBinding validates the same client, purpose, and optional caller-known
// file binding as Lookup, then returns a defensive copy of the handle's
// non-secret bundle identity. The one-shot passphrase remains memory-only and
// is consumed later by the worker.
func (store *MigrationSecretInputStore) ResolveBinding(
	lookup MigrationSecretInputLookup,
) (MigrationSecretInputBinding, error) {
	if store == nil || !migrationSecretHandlePattern.MatchString(lookup.Handle) ||
		!validMigrationSecretPurpose(lookup.Purpose) ||
		!validClientBinding(lookup.ClientBinding) {
		return MigrationSecretInputBinding{}, ErrMigrationSecretInputRequired
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return MigrationSecretInputBinding{}, ErrMigrationSecretInputRequired
	}
	entry, exists := store.entries[lookup.Handle]
	if !exists {
		return MigrationSecretInputBinding{}, ErrMigrationSecretInputRequired
	}
	now := store.nowUTC()
	if !now.Before(entry.expiresAt) {
		entry.passphrase.Clear()
		delete(store.entries, lookup.Handle)
		return MigrationSecretInputBinding{}, ErrMigrationSecretInputExpired
	}
	if entry.purpose != lookup.Purpose ||
		entry.clientDigest != migrationClientDigest(lookup.ClientBinding) ||
		(lookup.BundleFile != nil &&
			!bundleFileBindingsEqual(entry.bundleFile, lookup.BundleFile)) {
		entry.passphrase.Clear()
		delete(store.entries, lookup.Handle)
		return MigrationSecretInputBinding{}, ErrMigrationSecretInputMismatch
	}
	return MigrationSecretInputBinding{
		Handle: MigrationSecretInputHandle{
			Handle: lookup.Handle, Purpose: entry.purpose, BundleID: entry.bundleID,
			ExpiresAt: entry.expiresAt, UsesRemaining: 1,
		},
		BundleFile: cloneBundleFileBinding(entry.bundleFile),
	}, nil
}

// Consume removes the handle before invoking callback. Success, callback error,
// or panic therefore all consume the capability exactly once; SensitiveBuffer
// clears the backing bytes when callback leaves.
func (store *MigrationSecretInputStore) Consume(
	use MigrationSecretInputUse,
	callback func([]byte) error,
) error {
	if store == nil || callback == nil || !migrationSecretHandlePattern.MatchString(use.Handle) ||
		!validMigrationSecretPurpose(use.Purpose) || !validClientBinding(use.ClientBinding) {
		return ErrMigrationSecretInputRequired
	}
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return ErrMigrationSecretInputRequired
	}
	entry, exists := store.entries[use.Handle]
	if !exists {
		store.mu.Unlock()
		return ErrMigrationSecretInputRequired
	}
	delete(store.entries, use.Handle)
	now := store.nowUTC()
	store.mu.Unlock()

	if !now.Before(entry.expiresAt) {
		entry.passphrase.Clear()
		return ErrMigrationSecretInputExpired
	}
	if entry.purpose != use.Purpose ||
		entry.clientDigest != migrationClientDigest(use.ClientBinding) ||
		entry.bundleID != use.BundleID ||
		!bundleFileBindingsEqual(entry.bundleFile, use.BundleFile) {
		entry.passphrase.Clear()
		return ErrMigrationSecretInputMismatch
	}
	return entry.passphrase.Use(callback)
}

func (store *MigrationSecretInputStore) InvalidateClient(clientBinding string) int {
	if store == nil || !validClientBinding(clientBinding) {
		return 0
	}
	digest := migrationClientDigest(clientBinding)
	store.mu.Lock()
	defer store.mu.Unlock()
	removed := 0
	for handle, entry := range store.entries {
		if entry.clientDigest != digest {
			continue
		}
		entry.passphrase.Clear()
		delete(store.entries, handle)
		removed++
	}
	return removed
}

func (store *MigrationSecretInputStore) PurgeExpired() int {
	if store == nil {
		return 0
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.purgeExpiredLocked(store.nowUTC())
}

func (store *MigrationSecretInputStore) Close() {
	if store == nil {
		return
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return
	}
	for handle, entry := range store.entries {
		entry.passphrase.Clear()
		delete(store.entries, handle)
	}
	store.closed = true
}

func (store *MigrationSecretInputStore) purgeExpiredLocked(now time.Time) int {
	removed := 0
	for handle, entry := range store.entries {
		if now.Before(entry.expiresAt) {
			continue
		}
		entry.passphrase.Clear()
		delete(store.entries, handle)
		removed++
	}
	return removed
}

func (store *MigrationSecretInputStore) newHandleLocked() (string, error) {
	for range 8 {
		value := make([]byte, migrationSecretHandleBytes)
		if _, err := io.ReadFull(store.random, value); err != nil {
			clear(value)
			return "", fmt.Errorf("create migration secret-input handle: %w", err)
		}
		handle := "migh_" + base64.RawURLEncoding.EncodeToString(value)
		clear(value)
		if _, exists := store.entries[handle]; !exists {
			return handle, nil
		}
	}
	return "", errors.New("create migration secret-input handle: random collision")
}

func (store *MigrationSecretInputStore) nowUTC() time.Time {
	if store.now != nil {
		return store.now().UTC()
	}
	return time.Now().UTC()
}

func validMigrationSecretPurpose(purpose MigrationSecretPurpose) bool {
	switch purpose {
	case MigrationSecretPurposeExportCreate, MigrationSecretPurposeExportResume,
		MigrationSecretPurposeInspect, MigrationSecretPurposeImport:
		return true
	default:
		return false
	}
}

func migrationSecretFileBindingMatchesPurpose(
	purpose MigrationSecretPurpose,
	binding *MigrationBundleFileBinding,
) bool {
	if purpose == MigrationSecretPurposeExportCreate {
		return binding == nil
	}
	return binding != nil && binding.Validate() == nil
}

func cloneBundleFileBinding(
	binding *MigrationBundleFileBinding,
) *MigrationBundleFileBinding {
	if binding == nil {
		return nil
	}
	cloned := *binding
	return &cloned
}

func bundleFileBindingsEqual(
	left, right *MigrationBundleFileBinding,
) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right && right.Validate() == nil
}

func migrationClientDigest(clientBinding string) [sha256.Size]byte {
	return sha256.Sum256([]byte("hideout-migration-client/v1\x00" + clientBinding))
}

func validClientBinding(value string) bool {
	return value != "" && len(value) <= 4096
}
