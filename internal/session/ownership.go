package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vibe-agi/hideout/internal/audit"
	"golang.org/x/sys/unix"
)

const (
	ActiveSessionSchema = "hideout.active-session/v1"

	ownerRecordFile = "session.json"
	ownerLockFile   = "owner.lock"
)

type OwnerState string

const (
	OwnerStatePreparing OwnerState = "preparing"
	OwnerStateRunning   OwnerState = "running"
	OwnerStateCleaning  OwnerState = "cleaning"
	OwnerStateFailed    OwnerState = "failed"
)

type TerminalMode string

const (
	TerminalNone TerminalMode = "none"
	TerminalPTY  TerminalMode = "pty"
)

type OwnerStatus string

const (
	OwnerLive       OwnerStatus = "live"
	OwnerStale      OwnerStatus = "stale"
	OwnerUnprovable OwnerStatus = "unprovable"
)

var (
	ErrOwnerAlreadyLive   = errors.New("session owner is already live")
	ErrOwnerUnprovable    = errors.New("session owner state is unprovable")
	ErrOwnerCleanupFailed = errors.New("session cleanup is not proved complete")

	ownerEnvironmentIDPattern = regexp.MustCompile(`^env_[a-z0-9]+$`)
	ownerWorkspaceIDPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
	ownerCommandClassPattern  = regexp.MustCompile(`^[A-Za-z0-9._+-]{1,128}$`)
)

// OwnerRecord is durable, non-secret metadata. Process identity and lock paths
// are deliberately absent: owner liveness comes only from the kernel lock.
type OwnerRecord struct {
	Schema        string       `json:"schema"`
	SessionID     string       `json:"id"`
	EnvironmentID string       `json:"environmentId"`
	Profile       string       `json:"profile"`
	Backend       string       `json:"backend"`
	WorkspaceID   string       `json:"workspaceId,omitempty"`
	State         OwnerState   `json:"state"`
	TerminalMode  TerminalMode `json:"terminalMode"`
	StartedAt     time.Time    `json:"startedAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
	CommandClass  string       `json:"commandClass,omitempty"`
	CleanupError  string       `json:"cleanupError,omitempty"`
}

type ActiveSessionSummary struct {
	Schema        string       `json:"schema"`
	ID            string       `json:"id"`
	EnvironmentID string       `json:"environmentId"`
	Profile       string       `json:"profile"`
	Backend       string       `json:"backend"`
	WorkspaceID   string       `json:"workspaceId,omitempty"`
	State         OwnerState   `json:"state"`
	OwnerStatus   OwnerStatus  `json:"ownerStatus"`
	TerminalMode  TerminalMode `json:"terminalMode"`
	StartedAt     time.Time    `json:"startedAt"`
	UpdatedAt     time.Time    `json:"updatedAt,omitempty"`
	CommandClass  string       `json:"commandClass,omitempty"`
	CleanupError  string       `json:"cleanupError,omitempty"`
}

func (r OwnerRecord) Summary(status OwnerStatus) ActiveSessionSummary {
	return ActiveSessionSummary{
		Schema:        ActiveSessionSchema,
		ID:            r.SessionID,
		EnvironmentID: r.EnvironmentID,
		Profile:       r.Profile,
		Backend:       r.Backend,
		WorkspaceID:   r.WorkspaceID,
		State:         r.State,
		OwnerStatus:   status,
		TerminalMode:  r.TerminalMode,
		StartedAt:     r.StartedAt,
		UpdatedAt:     r.UpdatedAt,
		CommandClass:  r.CommandClass,
		CleanupError:  r.CleanupError,
	}
}

type OwnerObservation struct {
	SessionID string
	Status    OwnerStatus
	Record    OwnerRecord
}

type Owner struct {
	mu     sync.Mutex
	root   string
	dir    string
	record OwnerRecord
	file   *os.File
}

func AcquireOwner(root string, record OwnerRecord) (*Owner, error) {
	if err := validateOwnerRecord(record); err != nil {
		return nil, err
	}
	dir, err := ownerDir(root, record.SessionID, true)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, ownerLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("session %s: %w", record.SessionID, ErrOwnerAlreadyLive)
		}
		return nil, err
	}
	if info, err := os.Lstat(filepath.Join(dir, ownerRecordFile)); err == nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		if info.Mode().IsRegular() {
			return nil, fmt.Errorf("session %s has stale metadata requiring reconciliation: %w", record.SessionID, ErrOwnerUnprovable)
		}
		return nil, fmt.Errorf("session %s owner record is not regular: %w", record.SessionID, ErrOwnerUnprovable)
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	if err := writeOwnerRecord(dir, record); err != nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	return &Owner{root: root, dir: dir, record: record, file: file}, nil
}

func (o *Owner) Update(state OwnerState, cleanupError string) error {
	if o == nil {
		return errors.New("session owner is nil")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.file == nil {
		return ErrOwnerUnprovable
	}
	o.record.State = state
	cleanupError = strings.ReplaceAll(cleanupError, filepath.Dir(o.root), "[environment-state]")
	o.record.CleanupError = sanitizeOwnerText(audit.RedactString(cleanupError), 512)
	now := time.Now().UTC()
	if now.Before(o.record.StartedAt) {
		now = o.record.StartedAt
	}
	o.record.UpdatedAt = now
	if err := validateOwnerRecord(o.record); err != nil {
		return err
	}
	return writeOwnerRecord(o.dir, o.record)
}

func (o *Owner) Record() OwnerRecord {
	if o == nil {
		return OwnerRecord{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.record
}

func (o *Owner) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.file == nil {
		return nil
	}
	file := o.file
	o.file = nil
	unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN)
	closeErr := file.Close()
	removeErr := removeOwnerFiles(o.root, o.record.SessionID)
	return errors.Join(unlockErr, closeErr, removeErr)
}

// Release drops only the OS-backed liveness proof while retaining the strict
// owner record for doctor/recovery. It is used when cleanup cannot be proved;
// callers must update the record to failed before releasing it.
func (o *Owner) Release() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.file == nil {
		return nil
	}
	if o.record.State != OwnerStateFailed {
		return errors.New("session owner may be retained only in failed state")
	}
	file := o.file
	o.file = nil
	return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
}

func ProbeOwner(root, sessionID string) (OwnerObservation, error) {
	dir, err := ownerDir(root, sessionID, false)
	if err != nil {
		return OwnerObservation{SessionID: sessionID, Status: OwnerUnprovable}, errors.Join(ErrOwnerUnprovable, err)
	}
	record, err := readOwnerRecord(dir)
	if err != nil {
		return OwnerObservation{SessionID: sessionID, Status: OwnerUnprovable}, errors.Join(ErrOwnerUnprovable, err)
	}
	if record.SessionID != sessionID {
		return OwnerObservation{SessionID: sessionID, Status: OwnerUnprovable, Record: record}, errors.Join(
			ErrOwnerUnprovable,
			fmt.Errorf("owner directory identity %q does not match record identity %q", sessionID, record.SessionID),
		)
	}
	lockPath := filepath.Join(dir, ownerLockFile)
	info, err := os.Lstat(lockPath)
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = errors.New("owner lock is not a regular file")
		}
		return OwnerObservation{SessionID: sessionID, Status: OwnerUnprovable, Record: record}, errors.Join(ErrOwnerUnprovable, err)
	}
	file, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if err != nil {
		return OwnerObservation{SessionID: sessionID, Status: OwnerUnprovable, Record: record}, errors.Join(ErrOwnerUnprovable, err)
	}
	defer file.Close()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return OwnerObservation{SessionID: sessionID, Status: OwnerLive, Record: record}, nil
		}
		return OwnerObservation{SessionID: sessionID, Status: OwnerUnprovable, Record: record}, errors.Join(ErrOwnerUnprovable, err)
	}
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	return OwnerObservation{SessionID: sessionID, Status: OwnerStale, Record: record}, nil
}

func ListOwners(root string) ([]OwnerObservation, error) {
	if err := validateOwnerRoot(root, false); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	observed := make([]OwnerObservation, 0, len(entries))
	for _, entry := range entries {
		if !ValidID(entry.Name()) {
			continue
		}
		if !entry.IsDir() {
			observed = append(observed, OwnerObservation{SessionID: entry.Name(), Status: OwnerUnprovable})
			continue
		}
		item, probeErr := ProbeOwner(root, entry.Name())
		observed = append(observed, item)
		if probeErr != nil && !errors.Is(probeErr, ErrOwnerUnprovable) {
			return nil, probeErr
		}
	}
	slices.SortFunc(observed, func(a, b OwnerObservation) int {
		return strings.Compare(a.SessionID, b.SessionID)
	})
	return observed, nil
}

func ReconcileStaleOwners(root string) ([]string, error) {
	return ReconcileStaleOwnersWithCleanup(root, nil)
}

// ReconcileStaleOwnersWithCleanup removes an ownership proof only after the
// caller has cleaned the exact session runtime identified by the directory.
// The directory identity remains authoritative even if a record is corrupt.
func ReconcileStaleOwnersWithCleanup(root string, cleanup func(OwnerObservation) error) ([]string, error) {
	return reconcileStaleOwnersWithCleanup(root, cleanup, false)
}

// RecoverStaleOwnersWithCleanup removes stale failed/cleaning records only
// after the caller has independently removed the authority they represent.
// It is intended for explicit recovery operations such as stopping the owning
// VM, not for ordinary attach-time reconciliation.
func RecoverStaleOwnersWithCleanup(root string, cleanup func(OwnerObservation) error) ([]string, error) {
	if cleanup == nil {
		return nil, errors.New("failed owner recovery requires an authority cleanup proof")
	}
	return reconcileStaleOwnersWithCleanup(root, cleanup, true)
}

func reconcileStaleOwnersWithCleanup(root string, cleanup func(OwnerObservation) error, recoverFailed bool) ([]string, error) {
	items, err := ListOwners(root)
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0)
	for _, item := range items {
		switch item.Status {
		case OwnerLive:
			continue
		case OwnerStale:
			if !recoverFailed && (item.Record.State == OwnerStateFailed || item.Record.State == OwnerStateCleaning) {
				return removed, fmt.Errorf("session %s: %w", item.SessionID, ErrOwnerCleanupFailed)
			}
			if cleanup != nil {
				if cleanupErr := cleanup(item); cleanupErr != nil {
					markErr := markOwnerCleanupFailed(root, item, cleanupErr)
					return removed, errors.Join(
						fmt.Errorf("session %s cleanup: %w", item.SessionID, ErrOwnerCleanupFailed),
						cleanupErr,
						markErr,
					)
				}
			}
			if err := removeOwnerFiles(root, item.SessionID); err != nil {
				return removed, err
			}
			removed = append(removed, item.SessionID)
		case OwnerUnprovable:
			return removed, fmt.Errorf("session %s: %w", item.SessionID, ErrOwnerUnprovable)
		}
	}
	return removed, nil
}

func markOwnerCleanupFailed(root string, item OwnerObservation, cleanupErr error) error {
	dir, err := ownerDir(root, item.SessionID, false)
	if err != nil {
		return err
	}
	record := item.Record
	record.SessionID = item.SessionID
	record.State = OwnerStateFailed
	record.CleanupError = sanitizeOwnerText(audit.RedactString(cleanupErr.Error()), 512)
	now := time.Now().UTC()
	if now.Before(record.StartedAt) {
		now = record.StartedAt
	}
	record.UpdatedAt = now
	return writeOwnerRecord(dir, record)
}

func writeOwnerRecord(dir string, record OwnerRecord) error {
	if err := validateOwnerRecord(record); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("owner directory is not a real directory: %w", err)
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := filepath.Join(dir, ownerRecordFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, ownerRecordFile)); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readOwnerRecord(dir string) (OwnerRecord, error) {
	path := filepath.Join(dir, ownerRecordFile)
	info, err := os.Lstat(path)
	if err != nil {
		return OwnerRecord{}, err
	}
	if !info.Mode().IsRegular() {
		return OwnerRecord{}, errors.New("owner record is not a regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return OwnerRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record OwnerRecord
	if err := decoder.Decode(&record); err != nil {
		return OwnerRecord{}, err
	}
	if err := requireOwnerEOF(decoder); err != nil {
		return OwnerRecord{}, err
	}
	if err := validateOwnerRecord(record); err != nil {
		return OwnerRecord{}, err
	}
	return record, nil
}

func requireOwnerEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("owner record contains trailing data")
		}
		return err
	}
	return nil
}

func validateOwnerRecord(record OwnerRecord) error {
	if record.Schema != ActiveSessionSchema {
		return fmt.Errorf("unsupported active-session schema %q", record.Schema)
	}
	if !ValidID(record.SessionID) {
		return fmt.Errorf("invalid session id %q", record.SessionID)
	}
	if !ownerEnvironmentIDPattern.MatchString(record.EnvironmentID) {
		return fmt.Errorf("invalid environment id %q", record.EnvironmentID)
	}
	if strings.TrimSpace(record.Profile) == "" || len(record.Profile) > 128 || strings.TrimSpace(record.Profile) != record.Profile {
		return errors.New("owner profile is required and bounded")
	}
	if strings.TrimSpace(record.Backend) == "" || len(record.Backend) > 32 || strings.TrimSpace(record.Backend) != record.Backend {
		return errors.New("owner backend is required and bounded")
	}
	if record.WorkspaceID != "" && !ownerWorkspaceIDPattern.MatchString(record.WorkspaceID) {
		return errors.New("owner workspaceId must be a lowercase SHA-256")
	}
	switch record.State {
	case OwnerStatePreparing, OwnerStateRunning, OwnerStateCleaning, OwnerStateFailed:
	default:
		return fmt.Errorf("invalid owner state %q", record.State)
	}
	if record.TerminalMode != TerminalNone && record.TerminalMode != TerminalPTY {
		return fmt.Errorf("invalid terminal mode %q", record.TerminalMode)
	}
	if record.StartedAt.IsZero() || record.UpdatedAt.IsZero() || record.UpdatedAt.Before(record.StartedAt) {
		return errors.New("owner timestamps are invalid")
	}
	if record.CommandClass != "" && !ownerCommandClassPattern.MatchString(record.CommandClass) {
		return errors.New("owner command class is invalid")
	}
	if len(record.CleanupError) > 512 || sanitizeOwnerText(record.CleanupError, 512) != record.CleanupError {
		return errors.New("owner cleanup error is not safely bounded")
	}
	return nil
}

func ownerDir(root, sessionID string, create bool) (string, error) {
	if !ValidID(sessionID) {
		return "", fmt.Errorf("invalid session id %q", sessionID)
	}
	if create {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", err
		}
	}
	if err := validateOwnerRoot(root, create); err != nil {
		return "", err
	}
	dir := filepath.Join(root, sessionID)
	if create {
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("owner path is not a real directory")
	}
	return dir, nil
}

func validateOwnerRoot(root string, _ bool) error {
	if strings.TrimSpace(root) == "" || !filepath.IsAbs(root) {
		return errors.New("owner root must be absolute")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("owner root is not a real directory")
	}
	return nil
}

func removeOwnerFiles(root, sessionID string) error {
	dir, err := ownerDir(root, sessionID, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, name := range []string{ownerRecordFile + ".tmp", ownerRecordFile, ownerLockFile} {
		path := filepath.Join(dir, name)
		if info, statErr := os.Lstat(path); statErr == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("refusing to remove non-regular owner file %s", path)
			}
			if err := os.Remove(path); err != nil {
				return err
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	}
	return os.Remove(dir)
}

func sanitizeOwnerText(value string, maxBytes int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) && r != '\u001b' {
			return r
		}
		return -1
	}, value)
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
