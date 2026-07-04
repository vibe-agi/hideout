package environment

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	recordFile = "environment.json"
	lockFile   = ".lock"
	version    = "hideout.environment/v1"
)

type Store struct {
	Root string
}

type Lock struct {
	file *os.File
}

type Spec struct {
	Profile              string
	Backend              string
	BackendConfigVersion string
	Workspace            string
	GuestWorkspace       string
	ProfileID            string
	IdentityID           string
	User                 string
	Hostname             string
	ToolsHash            string
	InstanceName         string
}

type Record struct {
	Version              string    `json:"version"`
	ID                   string    `json:"id"`
	Profile              string    `json:"profile"`
	Backend              string    `json:"backend"`
	BackendConfigVersion string    `json:"backendConfigVersion,omitempty"`
	Workspace            string    `json:"workspace"`
	GuestWorkspace       string    `json:"guestWorkspace"`
	ProfileID            string    `json:"profileId,omitempty"`
	IdentityID           string    `json:"identityId,omitempty"`
	User                 string    `json:"user,omitempty"`
	Hostname             string    `json:"hostname,omitempty"`
	ToolsHash            string    `json:"toolsHash,omitempty"`
	InstanceName         string    `json:"instanceName,omitempty"`
	Status               string    `json:"status"`
	LastSessionID        string    `json:"lastSessionId,omitempty"`
	LastCommand          string    `json:"lastCommand,omitempty"`
	CreatedAt            time.Time `json:"createdAt"`
	LastStartedAt        time.Time `json:"lastStartedAt,omitempty"`
	LastEndedAt          time.Time `json:"lastEndedAt,omitempty"`
}

func (s Store) Create(spec Spec) (Record, error) {
	id, err := newID()
	if err != nil {
		return Record{}, err
	}
	now := time.Now().UTC()
	rec := Record{
		Version:              version,
		ID:                   id,
		Profile:              spec.Profile,
		Backend:              spec.Backend,
		BackendConfigVersion: spec.BackendConfigVersion,
		Workspace:            filepath.Clean(spec.Workspace),
		GuestWorkspace:       filepath.Clean(spec.GuestWorkspace),
		ProfileID:            spec.ProfileID,
		IdentityID:           spec.IdentityID,
		User:                 spec.User,
		Hostname:             spec.Hostname,
		ToolsHash:            spec.ToolsHash,
		InstanceName:         spec.InstanceName,
		Status:               "ready",
		CreatedAt:            now,
	}
	if err := s.Save(rec); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func (s Store) Save(rec Record) error {
	if !ValidID(rec.ID) {
		return fmt.Errorf("invalid environment id %q", rec.ID)
	}
	if rec.Version == "" {
		rec.Version = version
	}
	if rec.Status == "" {
		rec.Status = "ready"
	}
	path := s.recordPath(rec.ID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s Store) Load(id string) (Record, error) {
	resolved, err := s.ResolveID(id)
	if err != nil {
		return Record{}, err
	}
	return s.loadExact(resolved)
}

func (s Store) ResolveID(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", errors.New("environment id is required")
	}
	if ValidID(id) {
		if _, err := os.Stat(s.recordPath(id)); err == nil {
			return id, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	records, err := s.List()
	if err != nil {
		return "", err
	}
	var matches []string
	for _, rec := range records {
		if strings.HasPrefix(rec.ID, id) || strings.HasPrefix(strings.TrimPrefix(rec.ID, "env_"), id) {
			matches = append(matches, rec.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("environment %q not found", id)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("environment %q is ambiguous", id)
	}
}

func (s Store) Latest(spec Spec) (Record, bool, error) {
	records, err := s.List()
	if err != nil {
		return Record{}, false, err
	}
	for _, rec := range records {
		if rec.Profile == spec.Profile &&
			rec.Backend == spec.Backend &&
			rec.BackendConfigVersion == spec.BackendConfigVersion &&
			filepath.Clean(rec.Workspace) == filepath.Clean(spec.Workspace) &&
			filepath.Clean(rec.GuestWorkspace) == filepath.Clean(spec.GuestWorkspace) &&
			rec.ProfileID == spec.ProfileID &&
			rec.IdentityID == spec.IdentityID &&
			rec.User == spec.User &&
			rec.Hostname == spec.Hostname &&
			rec.ToolsHash == spec.ToolsHash {
			return rec, true, nil
		}
	}
	return Record{}, false, nil
}

func (s Store) List() ([]Record, error) {
	dir := s.environmentsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	records := make([]Record, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !ValidID(entry.Name()) {
			continue
		}
		rec, err := s.loadExact(entry.Name())
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	slices.SortFunc(records, func(a, b Record) int {
		at := sortTime(a)
		bt := sortTime(b)
		if at.Equal(bt) {
			return strings.Compare(a.ID, b.ID)
		}
		if at.After(bt) {
			return -1
		}
		return 1
	})
	return records, nil
}

func (s Store) Remove(id string) error {
	resolved, err := s.ResolveID(id)
	if err != nil {
		return err
	}
	return os.RemoveAll(s.dir(resolved))
}

func (s Store) Lock(id string) (*Lock, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("invalid environment id %q", id)
	}
	dir := s.dir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, lockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("environment %s is already in use", id)
		}
		return nil, err
	}
	return &Lock{file: file}, nil
}

func (l *Lock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
}

func (s Store) RuntimeDir(id string) string {
	return filepath.Join(s.dir(id), "runtime")
}

func (s Store) ShimDir(id string) string {
	return filepath.Join(s.RuntimeDir(id), "shims")
}

func (s Store) PrepareRuntime(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid environment id %q", id)
	}
	return s.ClearRuntime(id)
}

func (s Store) ClearRuntime(id string) error {
	if !ValidID(id) {
		return fmt.Errorf("invalid environment id %q", id)
	}
	runtimeDir := s.RuntimeDir(id)
	allowedDirs := map[string]bool{
		"tmp":       true,
		"shims":     true,
		"network":   true,
		"bootstrap": true,
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(runtimeDir, entry.Name())
		if allowedDirs[entry.Name()] && entry.IsDir() {
			if err := clearDir(path); err != nil {
				return err
			}
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	for name := range allowedDirs {
		if err := os.MkdirAll(filepath.Join(runtimeDir, name), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (s Store) loadExact(id string) (Record, error) {
	if !ValidID(id) {
		return Record{}, fmt.Errorf("invalid environment id %q", id)
	}
	data, err := os.ReadFile(s.recordPath(id))
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, err
	}
	if rec.ID == "" {
		rec.ID = id
	}
	if rec.Version == "" {
		rec.Version = version
	}
	return rec, nil
}

func (s Store) recordPath(id string) string {
	return filepath.Join(s.dir(id), recordFile)
}

func (s Store) dir(id string) string {
	return filepath.Join(s.environmentsDir(), id)
}

func (s Store) environmentsDir() string {
	return filepath.Join(s.Root, "environments")
}

func clearDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func sortTime(rec Record) time.Time {
	if !rec.LastStartedAt.IsZero() {
		return rec.LastStartedAt
	}
	return rec.CreatedAt
}

func ValidID(id string) bool {
	if strings.TrimSpace(id) != id || !strings.HasPrefix(id, "env_") {
		return false
	}
	if len(id) <= len("env_") || strings.ContainsAny(id, `/\`) {
		return false
	}
	for _, r := range strings.TrimPrefix(id, "env_") {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func newID() (string, error) {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "env_" + time.Now().UTC().Format("20060102t150405z") + hex.EncodeToString(b[:]), nil
}
