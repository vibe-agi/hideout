package profile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/environment"
	"github.com/vibe-agi/hideout/internal/profilestate"
	"golang.org/x/sys/unix"
)

const (
	profileBatchPendingFile      = ".pending-environment-batch.json"
	profileBatchOriginFile       = ".migration-origin.json"
	profileBatchMarkerSchema     = "hideout.profile-environment-batch/v1"
	maxProfileBatchMetadataBytes = 256 << 10
)

var (
	ErrBatchConflict             = errors.New("profile batch conflicts with existing state")
	ErrBatchVisibilityUnproved   = errors.New("profile batch visibility is unproved")
	ErrBatchFinalizationRequired = errors.New("profile batch activation must finish before mutation")
)

// EnvironmentBatchParticipant stages destination-owned profiles behind the
// same activation marker as their environment records. Profiles may either be
// metadata-free (the participant generates fresh IDs exactly once) or carry the
// strict destination metadata deterministically derived by an import operation.
// Source lineage/identity metadata is never accepted.
type EnvironmentBatchParticipant struct {
	Store          Store
	Profiles       []Profile
	ImportedStates []ImportedState
}

type ImportedState struct {
	ProfileName string
	StagePath   string
	Owner       profilestate.Owner
}

type profileBatchInput struct {
	Profile Profile
	Digest  string
	State   *ImportedState
}

type profileBatchBinding struct {
	Name        string `json:"name"`
	InputDigest string `json:"inputDigest"`
}

type profileBatchDigestInput struct {
	Profile Profile             `json:"profile"`
	State   *profilestate.Owner `json:"state,omitempty"`
}

type profileBatchMarker struct {
	Schema       string                       `json:"schema"`
	Publication  environment.BatchPublication `json:"publication"`
	ProfileName  string                       `json:"profileName"`
	InputDigest  string                       `json:"inputDigest"`
	RecordDigest string                       `json:"recordDigest"`
}

type profileCatalogLock struct {
	file           *os.File
	snapshotAbsent bool
}

func (participant EnvironmentBatchParticipant) BindingDigest() (string, error) {
	inputs, err := participant.normalizedInputs()
	if err != nil {
		return "", err
	}
	bindings := make([]profileBatchBinding, len(inputs))
	for index, input := range inputs {
		bindings[index] = profileBatchBinding{
			Name: input.Profile.Name, InputDigest: input.Digest,
		}
	}
	encoded, err := json.Marshal(bindings)
	if err != nil {
		return "", err
	}
	return profileBatchDigest(append(
		[]byte("hideout-profile-batch-binding/v1\x00"), encoded...,
	)), nil
}

func (participant EnvironmentBatchParticipant) Prepare(
	publication environment.BatchPublication,
) error {
	if strings.TrimSpace(participant.Store.Root) == "" || publication.Validate() != nil {
		return ErrBatchConflict
	}
	inputs, err := participant.normalizedInputs()
	if err != nil {
		return err
	}
	bindingDigest, err := participant.BindingDigest()
	if err != nil || bindingDigest != publication.ParticipantDigest {
		return errors.Join(ErrBatchConflict, err)
	}
	lock, err := participant.Store.lockCatalog(true)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	for _, input := range inputs {
		if err := participant.Store.prepareBatchProfileUnlocked(
			publication, input,
		); err != nil {
			return fmt.Errorf("prepare profile %q for environment batch: %w", input.Profile.Name, err)
		}
	}
	return nil
}

func (participant EnvironmentBatchParticipant) Preflight(
	publication environment.BatchPublication,
) error {
	if strings.TrimSpace(participant.Store.Root) == "" || publication.Validate() != nil {
		return ErrBatchConflict
	}
	inputs, err := participant.normalizedInputs()
	if err != nil {
		return err
	}
	bindingDigest, err := participant.BindingDigest()
	if err != nil || bindingDigest != publication.ParticipantDigest {
		return errors.Join(ErrBatchConflict, err)
	}
	lock, err := participant.Store.lockCatalog(false)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	if lock.snapshotAbsent {
		if len(participant.ImportedStates) != 0 {
			return ErrBatchConflict
		}
		return nil
	}
	for _, input := range inputs {
		info, err := os.Lstat(participant.Store.ProfileDir(input.Profile.Name))
		if errors.Is(err, os.ErrNotExist) {
			if input.State != nil {
				if verifyErr := profilestate.VerifyStage(
					input.State.StagePath, input.State.Owner,
				); verifyErr != nil {
					return errors.Join(ErrBatchConflict, verifyErr)
				}
			}
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
			info.Mode().Perm()&0o077 != 0 {
			return errors.Join(ErrBatchConflict, err)
		}
		if _, err := participant.Store.validatePreparedBatchProfileUnlocked(
			publication, input,
		); err != nil {
			return fmt.Errorf("preflight profile %q for environment batch: %w", input.Profile.Name, err)
		}
		if input.State != nil && profilestate.VerifyContent(
			participant.Store.ProfileDir(input.Profile.Name), input.State.Owner,
		) != nil {
			return ErrBatchConflict
		}
	}
	return nil
}

func (participant EnvironmentBatchParticipant) Finalize(
	publication environment.BatchPublication,
) error {
	if strings.TrimSpace(participant.Store.Root) == "" || publication.Validate() != nil {
		return ErrBatchVisibilityUnproved
	}
	inputs, err := participant.normalizedInputs()
	if err != nil {
		return err
	}
	bindingDigest, err := participant.BindingDigest()
	if err != nil || bindingDigest != publication.ParticipantDigest {
		return errors.Join(ErrBatchVisibilityUnproved, err)
	}
	lock, err := participant.Store.lockCatalog(true)
	if err != nil {
		return err
	}
	defer lock.Unlock()
	for _, input := range inputs {
		pending, err := participant.Store.validatePreparedBatchProfileUnlocked(
			publication, input,
		)
		if err != nil {
			return fmt.Errorf("finalize profile %q for environment batch: %w", input.Profile.Name, err)
		}
		if !pending {
			continue
		}
		if input.State != nil {
			if err := profilestate.VerifyContent(
				participant.Store.ProfileDir(input.Profile.Name), input.State.Owner,
			); err != nil {
				return errors.Join(ErrBatchVisibilityUnproved, err)
			}
			for _, name := range profilestate.MarkerNames() {
				if err := os.Remove(filepath.Join(
					participant.Store.ProfileDir(input.Profile.Name), name,
				)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
		}
		if err := os.Remove(filepath.Join(
			participant.Store.ProfileDir(input.Profile.Name), profileBatchPendingFile,
		)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := syncProfileDirectory(participant.Store.ProfileDir(input.Profile.Name)); err != nil {
			return err
		}
	}
	return nil
}

func (participant EnvironmentBatchParticipant) normalizedInputs() ([]profileBatchInput, error) {
	if len(participant.Profiles) == 0 || len(participant.Profiles) > 256 {
		return nil, ErrBatchConflict
	}
	states := make(map[string]ImportedState, len(participant.ImportedStates))
	profilesRoot := filepath.Join(participant.Store.Root, "profiles")
	for _, value := range participant.ImportedStates {
		expected, err := profilestate.StagePath(profilesRoot, value.Owner)
		if err != nil || ValidateName(value.ProfileName) != nil ||
			value.ProfileName != value.Owner.ProfileName ||
			filepath.Clean(value.StagePath) != expected {
			return nil, errors.Join(ErrBatchConflict, err)
		}
		if _, duplicate := states[value.ProfileName]; duplicate {
			return nil, ErrBatchConflict
		}
		states[value.ProfileName] = value
	}
	inputs := make([]profileBatchInput, len(participant.Profiles))
	for index, value := range participant.Profiles {
		candidate := cloneProfile(value)
		if len(candidate.Metadata) != 0 {
			if err := validateBatchProvidedMetadata(candidate.Metadata, ""); err != nil {
				return nil, fmt.Errorf(
					"%w: profile %q has invalid destination metadata: %v",
					ErrBatchConflict, candidate.Name, err,
				)
			}
		} else {
			candidate.Metadata = nil
		}
		if err := candidate.Validate(); err != nil {
			return nil, errors.Join(ErrBatchConflict, err)
		}
		state, hasState := states[candidate.Name]
		var stateOwner *profilestate.Owner
		if hasState {
			owner := state.Owner
			stateOwner = &owner
		}
		encoded, err := json.Marshal(profileBatchDigestInput{
			Profile: candidate, State: stateOwner,
		})
		if err != nil {
			return nil, err
		}
		inputs[index] = profileBatchInput{
			Profile: candidate,
			Digest: profileBatchDigest(append(
				[]byte("hideout-profile-batch-input/v1\x00"), encoded...,
			)),
		}
		if hasState {
			copyState := state
			inputs[index].State = &copyState
			delete(states, candidate.Name)
		}
	}
	if len(states) != 0 {
		return nil, ErrBatchConflict
	}
	sort.Slice(inputs, func(left, right int) bool {
		return inputs[left].Profile.Name < inputs[right].Profile.Name
	})
	for index := range inputs {
		if index > 0 && strings.EqualFold(
			inputs[index-1].Profile.Name, inputs[index].Profile.Name,
		) {
			return nil, ErrBatchConflict
		}
	}
	return inputs, nil
}

func (s Store) prepareBatchProfileUnlocked(
	publication environment.BatchPublication,
	input profileBatchInput,
) error {
	final := s.ProfileDir(input.Profile.Name)
	if info, err := os.Lstat(final); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return ErrBatchConflict
		}
		_, err := s.validatePreparedBatchProfileUnlocked(publication, input)
		if err == nil && input.State != nil {
			err = profilestate.VerifyContent(final, input.State.Owner)
		}
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Join(s.Root, "profiles")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging := ""
	cleanup := func() {}
	if input.State != nil {
		if err := profilestate.VerifyStage(input.State.StagePath, input.State.Owner); err != nil {
			return errors.Join(ErrBatchConflict, err)
		}
		staging = input.State.StagePath
		cleanup = func() {
			_ = profilestate.RemoveStage(staging, input.State.Owner)
		}
	} else {
		var err error
		staging, err = os.MkdirTemp(parent, ".batching-"+input.Profile.Name+"-")
		if err != nil {
			return err
		}
		cleanup = func() { _ = os.RemoveAll(staging) }
	}
	published := false
	defer func() {
		if !published {
			cleanup()
		}
	}()
	actual := cloneProfile(input.Profile)
	if len(actual.Metadata) == 0 {
		if err := ensureMetadata(&actual, "migration", publication.BatchID); err != nil {
			return err
		}
	} else if err := validateBatchProvidedMetadata(
		actual.Metadata, publication.BatchID,
	); err != nil {
		return errors.Join(ErrBatchConflict, err)
	}
	if err := actual.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(staging, "policy"), 0o700); err != nil {
		return err
	}
	if err := MaterializeIdentityState(staging, actual); err != nil {
		return err
	}
	if input.State != nil {
		snapshot, err := profilestate.Capture(staging)
		if err != nil || snapshot.Digest() != input.State.Owner.ContentDigest ||
			snapshot.LogicalBytes() != input.State.Owner.LogicalBytes {
			return errors.Join(ErrBatchConflict, err)
		}
	}
	data, err := json.MarshalIndent(actual, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	marker := profileBatchMarker{
		Schema: profileBatchMarkerSchema, Publication: publication,
		ProfileName: input.Profile.Name, InputDigest: input.Digest,
		RecordDigest: profileBatchDigest(data),
	}
	markerData, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	markerData = append(markerData, '\n')
	if err := writeProfileBatchFile(filepath.Join(staging, "profile.json"), data); err != nil {
		return err
	}
	for _, name := range []string{profileBatchOriginFile, profileBatchPendingFile} {
		if err := writeProfileBatchFile(filepath.Join(staging, name), markerData); err != nil {
			return err
		}
	}
	if err := syncProfileDirectory(staging); err != nil {
		return err
	}
	if err := os.Rename(staging, final); err != nil {
		return err
	}
	published = true
	return syncProfileDirectory(parent)
}

func validateBatchProvidedMetadata(metadata map[string]string, batchID string) error {
	allowed := map[string]struct{}{
		"profileId": {}, "identityId": {}, "machineId": {},
		"createdAt": {}, "lineageMode": {}, "createdFrom": {},
	}
	if len(metadata) != len(allowed) || metadata["lineageMode"] != "migration" ||
		metadata["createdFrom"] == "" || len(metadata["createdFrom"]) > 128 {
		return errors.New("migration profile metadata is incomplete or contains source lineage")
	}
	for key := range metadata {
		if _, exists := allowed[key]; !exists {
			return errors.New("migration profile metadata contains an unsupported field")
		}
	}
	if batchID != "" && metadata["createdFrom"] != batchID {
		return errors.New("migration profile metadata belongs to another batch")
	}
	createdAt, err := time.Parse(time.RFC3339, metadata["createdAt"])
	if err != nil || metadata["createdAt"] != createdAt.UTC().Format(time.RFC3339) {
		return errors.New("migration profile creation time is not canonical UTC")
	}
	if validateMetadata(metadata) != nil ||
		metadata["profileId"] == "" || metadata["identityId"] == "" ||
		metadata["machineId"] == "" {
		return errors.New("migration profile identity metadata is invalid")
	}
	return nil
}

func (s Store) validatePreparedBatchProfileUnlocked(
	publication environment.BatchPublication,
	input profileBatchInput,
) (bool, error) {
	dir := s.ProfileDir(input.Profile.Name)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return false, errors.Join(ErrBatchConflict, err)
	}
	origin, exists, err := readProfileBatchMarker(filepath.Join(dir, profileBatchOriginFile))
	if err != nil || !exists {
		return false, errors.Join(ErrBatchConflict, err)
	}
	expected := origin
	if origin.Publication.BatchID != publication.BatchID ||
		!reflect.DeepEqual(origin.Publication, publication) ||
		origin.ProfileName != input.Profile.Name || origin.InputDigest != input.Digest {
		return false, ErrBatchConflict
	}
	pending, pendingExists, err := readProfileBatchMarker(
		filepath.Join(dir, profileBatchPendingFile),
	)
	if err != nil || pendingExists && !reflect.DeepEqual(pending, expected) {
		return false, errors.Join(ErrBatchVisibilityUnproved, err)
	}
	data, err := os.ReadFile(s.ProfilePath(input.Profile.Name))
	if err != nil || profileBatchDigest(data) != origin.RecordDigest {
		return false, errors.Join(ErrBatchConflict, err)
	}
	current, err := decodeProfileBatchRecord(data)
	if err != nil || profileNeedsMetadata(current) {
		return false, errors.Join(ErrBatchConflict, err)
	}
	if len(input.Profile.Metadata) == 0 {
		current.Metadata = nil
	} else if err := validateBatchProvidedMetadata(
		current.Metadata, publication.BatchID,
	); err != nil {
		return false, errors.Join(ErrBatchConflict, err)
	}
	var stateOwner *profilestate.Owner
	if input.State != nil {
		owner := input.State.Owner
		stateOwner = &owner
	}
	encodedCurrent, err := json.Marshal(profileBatchDigestInput{
		Profile: current, State: stateOwner,
	})
	if err != nil {
		return false, errors.Join(ErrBatchConflict, err)
	}
	currentInputDigest := profileBatchDigest(append(
		[]byte("hideout-profile-batch-input/v1\x00"), encodedCurrent...,
	))
	if currentInputDigest != input.Digest {
		return false, ErrBatchConflict
	}
	return pendingExists, nil
}

func (s Store) readProfileData(name string) ([]byte, error) {
	lock, err := s.lockCatalog(false)
	if err != nil {
		return nil, err
	}
	if lock.snapshotAbsent {
		_ = lock.Unlock()
		return nil, os.ErrNotExist
	}
	data, readErr := os.ReadFile(s.ProfilePath(name))
	marker, pending, markerErr := readProfileBatchMarker(
		filepath.Join(s.ProfileDir(name), profileBatchPendingFile),
	)
	unlockErr := lock.Unlock()
	if readErr != nil || markerErr != nil || unlockErr != nil {
		return nil, errors.Join(readErr, markerErr, unlockErr)
	}
	if !pending {
		return data, nil
	}
	if marker.ProfileName != name || profileBatchDigest(data) != marker.RecordDigest {
		return nil, ErrBatchVisibilityUnproved
	}
	visible, err := (environment.Store{Root: s.Root}).BatchPublicationVisible(marker.Publication)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, fmt.Errorf("profile %q is pending activation: %w", name, os.ErrNotExist)
	}
	return data, nil
}

func (s Store) pendingBatchMutationCheckUnlocked(name string) error {
	_, exists, err := readProfileBatchMarker(
		filepath.Join(s.ProfileDir(name), profileBatchPendingFile),
	)
	if err != nil {
		return err
	}
	if exists {
		return ErrBatchFinalizationRequired
	}
	return nil
}

func (s Store) lockCatalog(exclusive bool) (*profileCatalogLock, error) {
	if strings.TrimSpace(s.Root) == "" {
		return nil, errors.New("profile store root is required")
	}
	dir := filepath.Join(s.Root, "profiles")
	if exclusive {
		for _, path := range []string{s.Root, dir} {
			if err := os.MkdirAll(path, 0o700); err != nil {
				return nil, err
			}
			if err := os.Chmod(path, 0o700); err != nil {
				return nil, err
			}
		}
	}
	info, err := os.Lstat(dir)
	if !exclusive && errors.Is(err, os.ErrNotExist) {
		return &profileCatalogLock{snapshotAbsent: true}, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return nil, errors.Join(ErrBatchVisibilityUnproved, err)
	}
	file, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	if err := unix.Flock(int(file.Fd()), mode); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &profileCatalogLock{file: file}, nil
}

func (lock *profileCatalogLock) Unlock() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
}

func (marker profileBatchMarker) validate() error {
	if marker.Schema != profileBatchMarkerSchema || marker.Publication.Validate() != nil ||
		validateProfileName(marker.ProfileName) != nil ||
		!validProfileBatchDigest(marker.InputDigest) ||
		!validProfileBatchDigest(marker.RecordDigest) ||
		marker.Publication.ParticipantDigest == "" {
		return ErrBatchVisibilityUnproved
	}
	return nil
}

func readProfileBatchMarker(path string) (profileBatchMarker, bool, error) {
	var marker profileBatchMarker
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return marker, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 ||
		info.Size() > maxProfileBatchMetadataBytes {
		return marker, false, errors.Join(ErrBatchVisibilityUnproved, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return marker, false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&marker); err != nil {
		return marker, false, ErrBatchVisibilityUnproved
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return marker, false, ErrBatchVisibilityUnproved
	}
	if err := marker.validate(); err != nil {
		return marker, false, err
	}
	return marker, true, nil
}

func decodeProfileBatchRecord(data []byte) (Profile, error) {
	var value Profile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return Profile{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Profile{}, errors.New("profile must contain exactly one JSON value")
	}
	if err := value.Validate(); err != nil {
		return Profile{}, err
	}
	return value, nil
}

func writeProfileBatchFile(path string, data []byte) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, file.Close())
		}
	}()
	for len(data) > 0 {
		written, writeErr := file.Write(data)
		if writeErr != nil {
			return writeErr
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	if err := file.Sync(); err != nil {
		return err
	}
	err = file.Close()
	closed = true
	return err
}

func syncProfileDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func profileBatchDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validProfileBatchDigest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") {
		return false
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(raw) == sha256.Size && value == strings.ToLower(value)
}

var _ environment.BatchParticipant = EnvironmentBatchParticipant{}
