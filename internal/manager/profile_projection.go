package manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

const (
	ProfileProjectionSchema = "hideout.profile-projection.v1"
	profileRevisionSchema   = "hideout.profile-revision/v1"
	maxProfileRevisionBytes = 32 << 10

	EffectiveNotObserved = "not-observed"
	EffectiveCurrent     = "effective"
	EffectiveTransition  = "transitioning"
	EffectiveBlocked     = "blocked"
	EffectiveFailed      = "failed"
	EffectiveUnproved    = "unproved"
)

var (
	ErrStaleProfileRevision = errors.New("profile projection revision is stale")
	profileDigestPattern    = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
)

type ProfileProjection struct {
	Schema        string             `json:"schema"`
	Profile       string             `json:"profile"`
	Revision      uint64             `json:"revision"`
	ContentDigest string             `json:"contentDigest"`
	Desired       profile.Profile    `json:"desired"`
	Effective     ProfileEffective   `json:"effective"`
	Transition    *ProfileTransition `json:"transition,omitempty"`
	UpdatedAt     time.Time          `json:"updatedAt"`
}

type ProfileEffective struct {
	Status   string                     `json:"status"`
	Network  *EffectiveNetwork          `json:"network,omitempty"`
	Sessions []EffectiveSessionSnapshot `json:"sessions"`
}

type EffectiveNetwork struct {
	Mode             string    `json:"mode,omitempty"`
	ProxySecretRef   string    `json:"proxySecretRef,omitempty"`
	SecretGeneration uint64    `json:"secretGeneration,omitempty"`
	DNS              string    `json:"dns,omitempty"`
	ObservedAt       time.Time `json:"observedAt,omitempty"`
}

type EffectiveSessionSnapshot struct {
	SessionID       string `json:"sessionId"`
	SnapshotID      string `json:"snapshotId"`
	ProfileRevision uint64 `json:"profileRevision,omitempty"`
	Current         bool   `json:"current"`
}

type ProfileTransition struct {
	OperationID string    `json:"operationId"`
	Kind        string    `json:"kind"`
	Phase       string    `json:"phase"`
	Blockers    []string  `json:"blockers,omitempty"`
	StartedAt   time.Time `json:"startedAt"`
}

// Validate enforces the shared Manager/TUI/Web projection contract. Effective
// state is evidence, not configuration intent: invalid or internally
// contradictory evidence must be rejected instead of rendered as authoritative.
func (projection ProfileProjection) Validate() error {
	if projection.Schema != ProfileProjectionSchema {
		return fmt.Errorf("unsupported profile projection schema %q", projection.Schema)
	}
	if err := profile.ValidateName(projection.Profile); err != nil {
		return err
	}
	if projection.Revision == 0 ||
		!profileDigestPattern.MatchString(projection.ContentDigest) ||
		projection.Desired.Name != projection.Profile ||
		projection.UpdatedAt.IsZero() {
		return errors.New("profile projection identity is incomplete")
	}
	switch projection.Effective.Status {
	case EffectiveNotObserved, EffectiveCurrent, EffectiveTransition,
		EffectiveBlocked, EffectiveFailed, EffectiveUnproved:
	default:
		return errors.New("profile effective status is invalid")
	}
	if projection.Effective.Sessions == nil ||
		len(projection.Effective.Sessions) > 256 {
		return errors.New("profile effective sessions are invalid")
	}
	seenSessions := make(map[string]struct{}, len(projection.Effective.Sessions))
	for _, snapshot := range projection.Effective.Sessions {
		if !operatorSessionIDPattern.MatchString(snapshot.SessionID) ||
			!environment.ValidConfigurationID(snapshot.SnapshotID) {
			return errors.New("profile effective session identity is invalid")
		}
		if _, exists := seenSessions[snapshot.SessionID]; exists {
			return errors.New("profile effective session is duplicated")
		}
		seenSessions[snapshot.SessionID] = struct{}{}
		if snapshot.Current {
			if snapshot.ProfileRevision != projection.Revision {
				return errors.New("current effective session revision is invalid")
			}
		} else if snapshot.ProfileRevision > projection.Revision {
			return errors.New("historical effective session revision is invalid")
		}
	}
	if err := projection.Effective.validateNetwork(); err != nil {
		return err
	}
	if projection.Transition != nil {
		if err := projection.Transition.validate(); err != nil {
			return err
		}
	}
	return nil
}

func (effective ProfileEffective) validateNetwork() error {
	if effective.Network == nil {
		if effective.Status == EffectiveCurrent {
			return errors.New("effective profile status requires network evidence")
		}
		return nil
	}
	if effective.Status == EffectiveNotObserved {
		return errors.New("not-observed profile status cannot carry network evidence")
	}
	network := effective.Network
	if network.ObservedAt.IsZero() ||
		len(network.DNS) == 0 || len(network.DNS) > 256 ||
		strings.IndexByte(network.DNS, 0) >= 0 {
		return errors.New("profile effective network observation is invalid")
	}
	switch network.Mode {
	case "direct":
		if network.ProxySecretRef != "" || network.SecretGeneration != 0 {
			return errors.New("direct effective network carries proxy identity")
		}
	case "proxy":
		if err := secrets.ValidateRef(network.ProxySecretRef); err != nil ||
			network.SecretGeneration == 0 {
			return errors.New("proxy effective network identity is invalid")
		}
	case "unknown":
		if network.ProxySecretRef != "" || network.SecretGeneration != 0 {
			return errors.New("unknown effective network carries proxy identity")
		}
	default:
		return errors.New("profile effective network mode is invalid")
	}
	return nil
}

func (transition ProfileTransition) validate() error {
	if !operationIDPattern.MatchString(transition.OperationID) ||
		!operationCodePattern.MatchString(transition.Kind) ||
		transition.StartedAt.IsZero() ||
		len(transition.Blockers) > 256 {
		return errors.New("profile transition identity is invalid")
	}
	switch transition.Phase {
	case OperationStaging, OperationActivating, "draining",
		OperationProving, OperationRollingBack, OperationRecoveryRequired:
	default:
		return errors.New("profile transition phase is invalid")
	}
	for _, blocker := range transition.Blockers {
		if len(blocker) == 0 || len(blocker) > 1024 ||
			strings.IndexByte(blocker, 0) >= 0 {
			return errors.New("profile transition blocker is invalid")
		}
	}
	return nil
}

type ProfileProjectionService struct {
	Store profile.Store
	Now   func() time.Time
}

type profileRevisionRecord struct {
	Schema        string    `json:"schema"`
	Profile       string    `json:"profile"`
	Revision      uint64    `json:"revision"`
	ContentDigest string    `json:"contentDigest"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (service ProfileProjectionService) Load(name string) (ProfileProjection, error) {
	name, err := normalizeManagerProfileName(name)
	if err != nil {
		return ProfileProjection{}, err
	}
	var projection ProfileProjection
	err = (Core{Store: service.Store}).withProfileMutationLock(name, func() error {
		var loadErr error
		projection, loadErr = service.loadLocked(name)
		return loadErr
	})
	return projection, err
}

func (service ProfileProjectionService) CheckCAS(name string, revision uint64, digest string) error {
	name, err := normalizeManagerProfileName(name)
	if err != nil {
		return err
	}
	return (Core{Store: service.Store}).withProfileMutationLock(name, func() error {
		projection, loadErr := service.loadLocked(name)
		if loadErr != nil {
			return loadErr
		}
		if revision != projection.Revision || digest != projection.ContentDigest {
			return fmt.Errorf(
				"%w: current revision=%d digest=%s",
				ErrStaleProfileRevision,
				projection.Revision,
				projection.ContentDigest,
			)
		}
		return nil
	})
}

func (service ProfileProjectionService) loadLocked(name string) (ProfileProjection, error) {
	desired, err := service.Store.Load(name)
	if err != nil {
		return ProfileProjection{}, err
	}
	digest, err := CanonicalDigest(CanonicalDomainProfileProjection, desired)
	if err != nil {
		return ProfileProjection{}, fmt.Errorf("digest profile projection: %w", err)
	}
	record, err := service.loadOrAdvanceRevision(name, digest)
	if err != nil {
		return ProfileProjection{}, err
	}
	return ProfileProjection{
		Schema:        ProfileProjectionSchema,
		Profile:       name,
		Revision:      record.Revision,
		ContentDigest: record.ContentDigest,
		Desired:       desired,
		Effective: ProfileEffective{
			Status:   EffectiveNotObserved,
			Sessions: []EffectiveSessionSnapshot{},
		},
		UpdatedAt: record.UpdatedAt,
	}, nil
}

func (service ProfileProjectionService) loadOrAdvanceRevision(name, digest string) (profileRevisionRecord, error) {
	path := service.Store.ProfileRevisionPath(name)
	record, err := loadProfileRevisionRecord(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		record = profileRevisionRecord{
			Schema:        profileRevisionSchema,
			Profile:       name,
			Revision:      1,
			ContentDigest: digest,
			UpdatedAt:     service.now(),
		}
		if err := writeProfileRevisionRecord(service.Store.ProfileDir(name), path, record); err != nil {
			return profileRevisionRecord{}, err
		}
		return record, nil
	case err != nil:
		return profileRevisionRecord{}, err
	}
	if record.Profile != name {
		return profileRevisionRecord{}, errors.New("profile revision sidecar identity mismatch")
	}
	if record.ContentDigest == digest {
		return record, nil
	}
	if record.Revision == math.MaxUint64 {
		return profileRevisionRecord{}, errors.New("profile revision exhausted")
	}
	record.Revision++
	record.ContentDigest = digest
	record.UpdatedAt = service.now()
	if err := writeProfileRevisionRecord(service.Store.ProfileDir(name), path, record); err != nil {
		return profileRevisionRecord{}, err
	}
	return record, nil
}

func (service ProfileProjectionService) now() time.Time {
	if service.Now != nil {
		return service.Now().UTC()
	}
	return time.Now().UTC()
}

func loadProfileRevisionRecord(path string) (profileRevisionRecord, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return profileRevisionRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return profileRevisionRecord{}, errors.New("profile revision sidecar must be a private regular file")
	}
	if info.Size() <= 0 || info.Size() > maxProfileRevisionBytes {
		return profileRevisionRecord{}, errors.New("profile revision sidecar size is invalid")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return profileRevisionRecord{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record profileRevisionRecord
	if err := decoder.Decode(&record); err != nil {
		return profileRevisionRecord{}, fmt.Errorf("decode profile revision sidecar: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return profileRevisionRecord{}, errors.New("profile revision sidecar contains trailing data")
		}
		return profileRevisionRecord{}, err
	}
	if err := record.validate(); err != nil {
		return profileRevisionRecord{}, err
	}
	return record, nil
}

func (record profileRevisionRecord) validate() error {
	if record.Schema != profileRevisionSchema {
		return fmt.Errorf("unsupported profile revision schema %q", record.Schema)
	}
	if err := profile.ValidateName(record.Profile); err != nil {
		return err
	}
	if record.Revision == 0 || !profileDigestPattern.MatchString(record.ContentDigest) || record.UpdatedAt.IsZero() {
		return errors.New("profile revision sidecar is incomplete")
	}
	return nil
}

func writeProfileRevisionRecord(profileDir, path string, record profileRevisionRecord) error {
	if err := record.validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := ensurePrivateProjectionDirectory(profileDir, dir); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return errors.New("profile revision sidecar target must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > maxProfileRevisionBytes {
		return errors.New("profile revision sidecar exceeds size bound")
	}
	temp, err := os.CreateTemp(dir, ".projection-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = os.Remove(tempPath)
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
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	keepTemp = false
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func ensurePrivateProjectionDirectory(profileDir, dir string) error {
	for _, candidate := range []string{profileDir, dir} {
		info, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) && candidate == dir {
			if err := os.Mkdir(candidate, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(candidate)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("profile projection directory %q must be a private real directory", candidate)
		}
	}
	return nil
}
