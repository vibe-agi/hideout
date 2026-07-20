package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	JournalSchema       = "hideout.lifecycle-journal/v1"
	maxJournalBytes     = 1 << 20
	maxJournalResources = 256
	maxJournalFacts     = 64
	maxResourceDeps     = 32
	journalDirName      = "lifecycle"
	journalFileName     = "journal.json"
)

type IdleDeadline struct {
	Incarnation      EnvironmentRef `json:"incarnation"`
	DaemonInstanceID string         `json:"daemonInstanceId"`
	ScheduledAt      time.Time      `json:"scheduledAt"`
	Deadline         time.Time      `json:"deadline"`
	Generation       uint64         `json:"generation"`
}

type StopAttempt struct {
	ID               string                      `json:"id"`
	Incarnation      EnvironmentRef              `json:"incarnation"`
	DaemonInstanceID string                      `json:"daemonInstanceId"`
	Mode             string                      `json:"mode"`
	State            string                      `json:"state"`
	StartedAt        time.Time                   `json:"startedAt"`
	Observation      *backendObservationSnapshot `json:"observation,omitempty"`
}

// backendObservationSnapshot avoids storing backend error text or handles.
type backendObservationSnapshot struct {
	State      string    `json:"state"`
	ObservedAt time.Time `json:"observedAt"`
	ReasonCode string    `json:"reasonCode,omitempty"`
}

type Reconciliation struct {
	DaemonInstanceID string    `json:"daemonInstanceId"`
	State            string    `json:"state"`
	ReasonCode       string    `json:"reasonCode,omitempty"`
	ObservedAt       time.Time `json:"observedAt"`
}

func blockedReconciliation(daemonID, reasonCode string, observedAt time.Time) Reconciliation {
	reasonCode = boundedReason(reasonCode)
	if !idPattern.MatchString(reasonCode) {
		reasonCode = "reconciliation-unproved"
	}
	return Reconciliation{
		DaemonInstanceID: daemonID,
		State:            "blocked",
		ReasonCode:       reasonCode,
		ObservedAt:       observedAt.UTC(),
	}
}

type Journal struct {
	Schema          string          `json:"schema"`
	EnvironmentID   string          `json:"environmentId"`
	StartGeneration uint64          `json:"startGeneration"`
	Incarnation     *EnvironmentRef `json:"incarnation,omitempty"`
	Resources       []Resource      `json:"resources,omitempty"`
	Facts           []Fact          `json:"facts,omitempty"`
	IdleDeadline    *IdleDeadline   `json:"idleDeadline,omitempty"`
	StopAttempt     *StopAttempt    `json:"stopAttempt,omitempty"`
	Reconciliation  Reconciliation  `json:"reconciliation"`
	UpdatedAt       time.Time       `json:"updatedAt"`
}

type JournalStore struct {
	Root string
}

func (s JournalStore) ListEnvironmentIDs() ([]string, error) {
	root, err := s.environmentDirRoot(false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !idPattern.MatchString(entry.Name()) {
			return nil, errors.New("lifecycle journal directory contains an invalid environment id")
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return nil, errors.New("lifecycle environment journal directory is unsafe")
		}
		ids = append(ids, entry.Name())
	}
	sort.Strings(ids)
	return ids, nil
}

func (s JournalStore) Load(environmentID string) (Journal, error) {
	dir, err := s.environmentDir(environmentID, false)
	if err != nil {
		return Journal{}, err
	}
	path := filepath.Join(dir, journalFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return Journal{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxJournalBytes {
		return Journal{}, errors.New("lifecycle journal must be a bounded regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Journal{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var journal Journal
	if err := decoder.Decode(&journal); err != nil {
		return Journal{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Journal{}, errors.New("lifecycle journal contains trailing data")
		}
		return Journal{}, err
	}
	if err := journal.Validate(); err != nil {
		return Journal{}, err
	}
	if journal.EnvironmentID != environmentID {
		return Journal{}, errors.New("lifecycle journal environment identity mismatch")
	}
	return journal, nil
}

func (s JournalStore) Write(journal Journal) error {
	if err := journal.Validate(); err != nil {
		return err
	}
	dir, err := s.environmentDir(journal.EnvironmentID, true)
	if err != nil {
		return err
	}
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	if len(data) > maxJournalBytes {
		return errors.New("lifecycle journal exceeds size bound")
	}
	target := filepath.Join(dir, journalFileName)
	if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
		return errors.New("lifecycle journal target is not a regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := os.CreateTemp(dir, ".journal-")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tempName)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, target); err != nil {
		return err
	}
	remove = false
	return syncDir(dir)
}

func (s JournalStore) Remove(environmentID string) error {
	dir, err := s.environmentDir(environmentID, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	path := filepath.Join(dir, journalFileName)
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("lifecycle journal target is not a regular file")
	} else if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if err := os.Remove(path); err != nil {
		return err
	}
	// A removed environment must not leave an empty lifecycle identity behind;
	// startup treats every directory here as a real journal candidate.
	if err := os.Remove(dir); err != nil {
		return fmt.Errorf("remove lifecycle environment directory: %w", err)
	}
	return syncDir(filepath.Dir(dir))
}

func (j Journal) Validate() error {
	if j.Schema != JournalSchema || !idPattern.MatchString(j.EnvironmentID) || j.StartGeneration == 0 || j.UpdatedAt.IsZero() {
		return errors.New("lifecycle journal identity is invalid")
	}
	if len(j.Resources) > maxJournalResources {
		return errors.New("lifecycle journal has too many resources")
	}
	if len(j.Facts) > maxJournalFacts {
		return errors.New("lifecycle journal has too many facts")
	}
	if j.Incarnation != nil {
		if err := j.Incarnation.Validate(j.Incarnation.BootID != ""); err != nil {
			return err
		}
		if j.Incarnation.EnvironmentID != j.EnvironmentID || j.Incarnation.StartGeneration != j.StartGeneration {
			return errors.New("lifecycle journal incarnation mismatch")
		}
	}
	for _, resource := range j.Resources {
		if len(resource.Dependencies) > maxResourceDeps || !resource.IsPossiblyLive() {
			return errors.New("lifecycle journal resource is invalid or terminal")
		}
		if resource.Ref.Generation != j.StartGeneration || resource.Owner.Generation != j.StartGeneration {
			return errors.New("lifecycle journal resource generation mismatch")
		}
		for _, dependency := range resource.Dependencies {
			if dependency.Ref.Generation != j.StartGeneration {
				return errors.New("lifecycle journal dependency generation mismatch")
			}
		}
		if resource.Ref.Kind == KindBackendIncarnation && resource.Ref.ID != j.EnvironmentID {
			return errors.New("lifecycle journal root identity mismatch")
		}
	}
	if len(j.Resources) != 0 {
		if err := ValidateGraph(j.Resources, true); err != nil {
			return err
		}
	}
	for _, fact := range j.Facts {
		if err := validateFact(fact); err != nil {
			return err
		}
		// Facts are bounded historical classifications, not live authority. They
		// may survive into later backend generations, but may never claim a
		// generation that has not existed yet.
		if fact.Generation > j.StartGeneration {
			return errors.New("lifecycle journal fact generation mismatch")
		}
	}
	if j.IdleDeadline != nil {
		if err := j.IdleDeadline.Incarnation.Validate(true); err != nil || j.IdleDeadline.Generation == 0 || !idPattern.MatchString(j.IdleDeadline.DaemonInstanceID) || j.IdleDeadline.ScheduledAt.IsZero() || !j.IdleDeadline.Deadline.After(j.IdleDeadline.ScheduledAt) {
			return errors.New("lifecycle idle deadline is invalid")
		}
		if j.Incarnation == nil || !j.IdleDeadline.Incarnation.Equal(*j.Incarnation) {
			return errors.New("lifecycle idle deadline incarnation mismatch")
		}
	}
	if j.StopAttempt != nil {
		if err := j.StopAttempt.Incarnation.Validate(true); err != nil || !idPattern.MatchString(j.StopAttempt.ID) || !idPattern.MatchString(j.StopAttempt.DaemonInstanceID) || !containsString([]string{"automatic", "explicit-recovery"}, j.StopAttempt.Mode) || !containsString([]string{"planned", "draining", "invoked", "observing", "committed", "unknown", "failed"}, j.StopAttempt.State) || j.StopAttempt.StartedAt.IsZero() {
			return errors.New("lifecycle stop attempt is invalid")
		}
		if j.StopAttempt.Incarnation.EnvironmentID != j.EnvironmentID || j.StopAttempt.Incarnation.StartGeneration != j.StartGeneration ||
			(j.Incarnation != nil && !j.StopAttempt.Incarnation.Equal(*j.Incarnation)) {
			return errors.New("lifecycle stop attempt incarnation mismatch")
		}
		if j.StopAttempt.Observation != nil {
			observation := j.StopAttempt.Observation
			if !containsString([]string{"running", "stopped", "absent", "unknown", "not-applicable"}, observation.State) || observation.ObservedAt.IsZero() ||
				(observation.ReasonCode != "" && !idPattern.MatchString(observation.ReasonCode)) ||
				(observation.State == "unknown" && observation.ReasonCode == "") {
				return errors.New("lifecycle stop observation is invalid")
			}
		}
	}
	if !containsString([]string{"pending", "complete", "blocked"}, j.Reconciliation.State) || !idPattern.MatchString(j.Reconciliation.DaemonInstanceID) || j.Reconciliation.ObservedAt.IsZero() ||
		(j.Reconciliation.ReasonCode != "" && !idPattern.MatchString(j.Reconciliation.ReasonCode)) {
		return errors.New("lifecycle reconciliation is invalid")
	}
	return nil
}

func (s JournalStore) environmentDir(environmentID string, create bool) (string, error) {
	if !idPattern.MatchString(environmentID) || !filepath.IsAbs(s.Root) {
		return "", errors.New("lifecycle journal path is invalid")
	}
	cleanRoot := filepath.Clean(s.Root)
	if err := requirePrivateDir(cleanRoot); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", err
	}
	if err := requirePrivateDir(root); err != nil {
		return "", err
	}
	dir, err := s.environmentDirRootFromResolved(root, create)
	if err != nil {
		return "", err
	}
	environmentDir := filepath.Join(dir, environmentID)
	if err := ensureJournalDir(environmentDir, create); err != nil {
		return "", err
	}
	return environmentDir, nil
}

func (s JournalStore) environmentDirRoot(create bool) (string, error) {
	if !filepath.IsAbs(s.Root) {
		return "", errors.New("lifecycle journal path is invalid")
	}
	cleanRoot := filepath.Clean(s.Root)
	if err := requirePrivateDir(cleanRoot); err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", err
	}
	if err := requirePrivateDir(root); err != nil {
		return "", err
	}
	return s.environmentDirRootFromResolved(root, create)
}

func (s JournalStore) environmentDirRootFromResolved(root string, create bool) (string, error) {
	dir := filepath.Join(root, journalDirName)
	if err := ensureJournalDir(dir, create); err != nil {
		return "", err
	}
	return dir, nil
}

func ensureJournalDir(path string, create bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && create {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("lifecycle journal directory must be private and not a symlink")
	}
	return nil
}

func requirePrivateDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return errors.New("store root must be a private directory")
	}
	return nil
}

func syncDir(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func containsString(values []string, value string) bool {
	value = strings.TrimSpace(value)
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func newJournal(environmentID, daemonID string, generation uint64, now time.Time) Journal {
	return Journal{Schema: JournalSchema, EnvironmentID: environmentID, StartGeneration: generation, Reconciliation: Reconciliation{DaemonInstanceID: daemonID, State: "pending", ObservedAt: now.UTC()}, UpdatedAt: now.UTC()}
}

func journalError(label string, err error) error {
	return fmt.Errorf("%s lifecycle journal: %w", label, err)
}

// BlockedEnvironmentReconciliations reports environments whose journal records
// a blocked reconciliation, keyed by environment id with the bounded reason
// code. Listing surfaces consult it so an environment that refuses attach and
// stop cannot present as healthy. Read-only and best-effort: an unreadable
// store or journal yields no entry rather than an error.
func BlockedEnvironmentReconciliations(storeRoot string) map[string]string {
	store := JournalStore{Root: storeRoot}
	ids, err := store.ListEnvironmentIDs()
	if err != nil || len(ids) == 0 {
		return nil
	}
	out := map[string]string{}
	for _, id := range ids {
		journal, err := store.Load(id)
		if err != nil || journal.Reconciliation.State != "blocked" {
			continue
		}
		reason := journal.Reconciliation.ReasonCode
		if reason == "" {
			reason = "reconciliation-blocked"
		}
		out[id] = reason
	}
	return out
}
