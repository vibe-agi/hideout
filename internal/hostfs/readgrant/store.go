package readgrant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/vibe-agi/hideout/internal/session"
)

const (
	GrantVersion     = "hideout.hostfs-read-grants/v1"
	ProviderVersion  = "hideout.hostfs-read-provider/v1"
	OperationRead    = "read"
	MaxGrantLifetime = 24 * time.Hour
)

type Grant struct {
	DecisionID       string    `json:"decisionId"`
	Revision         int       `json:"revision"`
	Operation        string    `json:"operation"`
	RequestedPath    string    `json:"requestedPath"`
	CanonicalPath    string    `json:"canonicalPath"`
	VisibilityRuleID string    `json:"visibilityRuleId"`
	VisibilitySource string    `json:"visibilitySource"`
	IssuedAt         time.Time `json:"issuedAt"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type Manifest struct {
	Version    string    `json:"version"`
	SessionID  string    `json:"sessionId"`
	Generation uint64    `json:"generation"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Grants     []Grant   `json:"grants"`
}

type RequestRecord struct {
	KeyDigest        string    `json:"keyDigest"`
	DecisionID       string    `json:"decisionId"`
	RequestedPath    string    `json:"requestedPath"`
	CanonicalPath    string    `json:"canonicalPath"`
	Operation        string    `json:"operation"`
	Profile          string    `json:"profile"`
	Backend          string    `json:"backend"`
	VisibilityRule   string    `json:"visibilityRuleId"`
	VisibilitySource string    `json:"visibilitySource"`
	VisibilityRoot   string    `json:"visibilityRoot"`
	VisibilityScope  string    `json:"visibilityScope"`
	UntrustedReason  string    `json:"untrustedReason,omitempty"`
	State            string    `json:"state"`
	Revision         int       `json:"revision"`
	SuppressedCount  uint64    `json:"suppressedCount,omitempty"`
	LastSuppressedAt time.Time `json:"lastSuppressedAt,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
	TimeoutAt        time.Time `json:"timeoutAt"`
}

type ProviderState struct {
	Version       string                   `json:"version"`
	SessionID     string                   `json:"sessionId"`
	Requests      map[string]RequestRecord `json:"requests"`
	CreationTimes []time.Time              `json:"creationTimes"`
	UpdatedAt     time.Time                `json:"updatedAt"`
}

type Store struct {
	dir          string
	sessionID    string
	providerLock string
	statePath    string
	grantsPath   string
	create       bool
}

func NewStore(dir, sessionID string) (*Store, error) {
	return storeAt(dir, sessionID, true)
}

func OpenStore(dir, sessionID string) (*Store, error) {
	return storeAt(dir, sessionID, false)
}

func storeAt(dir, sessionID string, create bool) (*Store, error) {
	if !session.ValidID(sessionID) {
		return nil, fmt.Errorf("invalid session id %q", sessionID)
	}
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "." || !filepath.IsAbs(dir) {
		return nil, errors.New("HostFS read store directory must be absolute")
	}
	if create {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return nil, err
		}
	} else {
		info, err := os.Lstat(dir)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("existing HostFS read store must be a private real directory")
		}
		lockInfo, err := os.Lstat(filepath.Join(dir, "provider.lock"))
		if err != nil {
			return nil, err
		}
		if !lockInfo.Mode().IsRegular() || lockInfo.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("existing HostFS read provider lock must be a regular file")
		}
	}
	return &Store{
		dir:          dir,
		sessionID:    sessionID,
		providerLock: filepath.Join(dir, "provider.lock"),
		statePath:    filepath.Join(dir, "state.json"),
		grantsPath:   filepath.Join(dir, "grants.json"),
		create:       create,
	}, nil
}

func (s *Store) GrantsPath() string {
	if s == nil {
		return ""
	}
	return s.grantsPath
}

func (s *Store) WithExclusive(fn func() error) error {
	return s.withLock(unix.LOCK_EX, fn)
}

func (s *Store) WithShared(fn func() error) error {
	return s.withLock(unix.LOCK_SH, fn)
}

func (s *Store) withLock(mode int, fn func() error) error {
	if s == nil || fn == nil {
		return errors.New("HostFS read store and lock callback are required")
	}
	flags := os.O_RDWR
	if s.create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(s.providerLock, flags, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), mode); err != nil {
		return err
	}
	defer unix.Flock(int(f.Fd()), unix.LOCK_UN)
	return fn()
}

func (s *Store) LoadManifest() (Manifest, error) {
	var out Manifest
	if err := strictReadJSON(s.grantsPath, &out); err != nil {
		return Manifest{}, err
	}
	if err := out.Validate(s.sessionID); err != nil {
		return Manifest{}, err
	}
	return out, nil
}

func (s *Store) SaveManifest(manifest Manifest) error {
	if err := manifest.Validate(s.sessionID); err != nil {
		return err
	}
	return atomicWriteJSON(s.grantsPath, manifest)
}

func (s *Store) LoadState() (ProviderState, error) {
	var out ProviderState
	err := strictReadJSON(s.statePath, &out)
	if errors.Is(err, os.ErrNotExist) {
		return ProviderState{Version: ProviderVersion, SessionID: s.sessionID, Requests: map[string]RequestRecord{}}, nil
	}
	if err != nil {
		return ProviderState{}, err
	}
	if err := out.Validate(s.sessionID); err != nil {
		return ProviderState{}, err
	}
	return out, nil
}

func (s *Store) SaveState(state ProviderState) error {
	if err := state.Validate(s.sessionID); err != nil {
		return err
	}
	return atomicWriteJSON(s.statePath, state)
}

func (m Manifest) Validate(sessionID string) error {
	if m.Version != GrantVersion {
		return fmt.Errorf("grant version must be %q", GrantVersion)
	}
	if m.SessionID != sessionID || !session.ValidID(m.SessionID) {
		return errors.New("grant sessionId does not match store session")
	}
	if m.Generation == 0 {
		return errors.New("grant generation must be positive")
	}
	if m.UpdatedAt.IsZero() {
		return errors.New("grant updatedAt is required")
	}
	seen := map[string]struct{}{}
	for i, grant := range m.Grants {
		if err := grant.Validate(); err != nil {
			return fmt.Errorf("grants[%d]: %w", i, err)
		}
		key := grant.Operation + "\x00" + grant.RequestedPath + "\x00" + grant.CanonicalPath
		if _, exists := seen[key]; exists {
			return fmt.Errorf("grants[%d]: duplicate exact authority", i)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (g Grant) Validate() error {
	if !strings.HasPrefix(g.DecisionID, "dec_hfr_") || len(strings.TrimPrefix(g.DecisionID, "dec_hfr_")) == 0 {
		return errors.New("decisionId must be an opaque HostFS read decision id")
	}
	if g.Revision < 1 {
		return errors.New("revision must be positive")
	}
	if g.Operation != OperationRead {
		return errors.New("operation must be read")
	}
	if err := validateAbsolutePath(g.RequestedPath); err != nil {
		return fmt.Errorf("requestedPath: %w", err)
	}
	if err := validateAbsolutePath(g.CanonicalPath); err != nil {
		return fmt.Errorf("canonicalPath: %w", err)
	}
	if strings.TrimSpace(g.VisibilityRuleID) == "" {
		return errors.New("visibilityRuleId is required")
	}
	if g.VisibilitySource != "profile" && g.VisibilitySource != "environment" && g.VisibilitySource != "run" {
		return fmt.Errorf("unsupported visibilitySource %q", g.VisibilitySource)
	}
	if g.IssuedAt.IsZero() || g.ExpiresAt.IsZero() || !g.ExpiresAt.After(g.IssuedAt) {
		return errors.New("issuedAt and later expiresAt are required")
	}
	if g.ExpiresAt.Sub(g.IssuedAt) > MaxGrantLifetime {
		return fmt.Errorf("grant lifetime exceeds %s", MaxGrantLifetime)
	}
	return nil
}

func (m Manifest) FindActive(operation, requestedPath, canonicalPath string, now time.Time) (Grant, bool) {
	if err := m.Validate(m.SessionID); err != nil {
		return Grant{}, false
	}
	for _, grant := range m.Grants {
		if grant.Operation == operation && grant.RequestedPath == requestedPath && grant.CanonicalPath == canonicalPath && now.Before(grant.ExpiresAt) && !now.Before(grant.IssuedAt) {
			return grant, true
		}
	}
	return Grant{}, false
}

func (s ProviderState) Validate(sessionID string) error {
	if s.Version != ProviderVersion || s.SessionID != sessionID || !session.ValidID(s.SessionID) {
		return errors.New("provider state version/session mismatch")
	}
	if s.Requests == nil {
		return errors.New("provider requests map is required")
	}
	for key, record := range s.Requests {
		if strings.TrimSpace(key) == "" || record.KeyDigest != key || !strings.HasPrefix(record.DecisionID, "dec_hfr_") || record.Operation != OperationRead || strings.TrimSpace(record.Profile) == "" || strings.TrimSpace(record.Backend) == "" || strings.TrimSpace(record.VisibilityRule) == "" || record.Revision < 1 || record.CreatedAt.IsZero() || record.TimeoutAt.IsZero() {
			return fmt.Errorf("invalid provider request %q", key)
		}
		if err := validateAbsolutePath(record.RequestedPath); err != nil {
			return fmt.Errorf("provider request %q requestedPath: %w", key, err)
		}
		if err := validateAbsolutePath(record.CanonicalPath); err != nil {
			return fmt.Errorf("provider request %q canonicalPath: %w", key, err)
		}
		if err := validateAbsolutePath(record.VisibilityRoot); err != nil {
			return fmt.Errorf("provider request %q visibilityRoot: %w", key, err)
		}
		if record.VisibilitySource != "profile" && record.VisibilitySource != "environment" && record.VisibilitySource != "run" {
			return fmt.Errorf("provider request %q has unsupported visibilitySource %q", key, record.VisibilitySource)
		}
		if record.VisibilityScope != "exact-file" && record.VisibilityScope != "dir" && record.VisibilityScope != "recursive-dir" {
			return fmt.Errorf("provider request %q has unsupported visibilityScope %q", key, record.VisibilityScope)
		}
		if len([]byte(record.UntrustedReason)) > 512 {
			return fmt.Errorf("provider request %q untrustedReason exceeds 512 bytes", key)
		}
		switch record.State {
		case "pending", "claimed", "applied", "denied", "timed-out", "failed", "stale":
		default:
			return fmt.Errorf("provider request %q has unsupported state %q", key, record.State)
		}
		if record.SuppressedCount > 0 && record.LastSuppressedAt.IsZero() {
			return fmt.Errorf("provider request %q suppressed count requires lastSuppressedAt", key)
		}
	}
	for _, createdAt := range s.CreationTimes {
		if createdAt.IsZero() {
			return errors.New("provider creationTimes contains zero timestamp")
		}
	}
	if s.UpdatedAt.IsZero() && (len(s.Requests) > 0 || len(s.CreationTimes) > 0) {
		return errors.New("provider updatedAt is required for non-empty state")
	}
	return nil
}

func validateAbsolutePath(path string) error {
	if strings.TrimSpace(path) != path || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.Contains(path, `\`) || strings.HasPrefix(path, "//") {
		return errors.New("path must be a clean absolute host path")
	}
	return nil
}

func strictReadJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("JSON document must contain exactly one value")
	}
	return nil
}

func atomicWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	keep := true
	defer func() {
		if keep {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
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
	keep = false
	return nil
}
