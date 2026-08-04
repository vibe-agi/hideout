package environment

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
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	batchActivationDirectory = ".activations"
	batchPendingFile         = ".pending-activation.json"
	batchOriginFile          = ".migration-origin.json"
	batchActivationSchema    = "hideout.environment-batch-activation/v1"
	batchRecordMarkerSchema  = "hideout.environment-batch-record/v1"
	maxBatchMetadataBytes    = 256 << 10
)

var (
	ErrBatchConflict             = errors.New("environment batch conflicts with existing state")
	ErrBatchVisibilityUnproved   = errors.New("environment batch visibility is unproved")
	ErrBatchFinalizationRequired = errors.New("environment batch activation must finish before mutation")
)

type BatchPublication struct {
	BatchID           string   `json:"batchId"`
	Digest            string   `json:"digest"`
	ParticipantDigest string   `json:"participantDigest,omitempty"`
	RecordIDs         []string `json:"recordIds"`
}

func (publication BatchPublication) Validate() error {
	if !validBatchID(publication.BatchID) ||
		!validEnvironmentDigest(publication.Digest) ||
		(publication.ParticipantDigest != "" &&
			!validEnvironmentDigest(publication.ParticipantDigest)) ||
		len(publication.RecordIDs) == 0 || len(publication.RecordIDs) > 256 {
		return ErrBatchVisibilityUnproved
	}
	for index, id := range publication.RecordIDs {
		if !ValidID(id) || (index > 0 && publication.RecordIDs[index-1] >= id) {
			return ErrBatchVisibilityUnproved
		}
	}
	return nil
}

// BatchParticipant binds non-environment records to the same atomic activation
// marker. BindingDigest must be deterministic from the intended records;
// Prepare leaves them hidden behind that marker and Finalize only removes
// pending metadata after the marker has made the complete batch visible.
type BatchParticipant interface {
	BindingDigest() (string, error)
	Preflight(BatchPublication) error
	Prepare(BatchPublication) error
	Finalize(BatchPublication) error
}

type batchRecordBinding struct {
	RecordID     string `json:"recordId"`
	RecordDigest string `json:"recordDigest"`
}

type batchActivation struct {
	Schema            string               `json:"schema"`
	BatchID           string               `json:"batchId"`
	Digest            string               `json:"digest"`
	ParticipantDigest string               `json:"participantDigest,omitempty"`
	Records           []batchRecordBinding `json:"records"`
}

type batchRecordMarker struct {
	Schema       string `json:"schema"`
	BatchID      string `json:"batchId"`
	BatchDigest  string `json:"batchDigest"`
	RecordID     string `json:"recordId"`
	RecordDigest string `json:"recordDigest"`
}

// PublishBatch makes all records logically visible at one atomic marker
// rename. Complete record directories exist first but carry a pending marker,
// so every Store reader sees either none of the batch or all of it. Marker
// cleanup is convergent and does not revoke visibility after the commit point.
func (s Store) PublishBatch(
	batchID string,
	records []Record,
) (BatchPublication, error) {
	return s.PublishBatchWithParticipant(batchID, records, nil)
}

// PublishBatchWithParticipant extends PublishBatch's single visibility point
// to one deterministic, independently validated participant.
func (s Store) PublishBatchWithParticipant(
	batchID string,
	records []Record,
	participant BatchParticipant,
) (BatchPublication, error) {
	records = append([]Record(nil), records...)
	participantDigest := ""
	if participant != nil {
		var err error
		participantDigest, err = participant.BindingDigest()
		if err != nil || !validEnvironmentDigest(participantDigest) {
			return BatchPublication{}, errors.Join(ErrBatchConflict, err)
		}
	}
	activation, encodedRecords, err := buildBatchActivation(
		batchID, records, participantDigest,
	)
	if err != nil {
		return BatchPublication{}, err
	}
	publication := batchPublicationFromActivation(activation)
	lock, err := s.lockCatalog(true)
	if err != nil {
		return BatchPublication{}, err
	}
	defer lock.Unlock()

	if err := s.preflightBatchUnlocked(activation, records, encodedRecords); err != nil {
		return BatchPublication{}, err
	}
	if participant != nil {
		if err := participant.Preflight(publication); err != nil {
			return BatchPublication{}, err
		}
		if err := participant.Prepare(publication); err != nil {
			return BatchPublication{}, err
		}
	}
	for index, binding := range activation.Records {
		final := s.dir(binding.RecordID)
		if _, err := os.Lstat(final); errors.Is(err, os.ErrNotExist) {
			marker := batchRecordMarker{
				Schema: batchRecordMarkerSchema, BatchID: activation.BatchID,
				BatchDigest: activation.Digest, RecordID: binding.RecordID,
				RecordDigest: binding.RecordDigest,
			}
			if err := s.prepareBatchRecordUnlocked(
				records[index], encodedRecords[index], marker,
			); err != nil {
				return BatchPublication{}, err
			}
			if s.batchCut != nil {
				if err := s.batchCut("record-prepared", index); err != nil {
					return BatchPublication{}, err
				}
			}
		} else if err != nil {
			return BatchPublication{}, err
		}
	}

	allFinalized, err := s.batchAlreadyFinalizedUnlocked(activation)
	if err != nil {
		return BatchPublication{}, err
	}
	if allFinalized {
		if participant != nil {
			if err := participant.Finalize(publication); err != nil {
				return BatchPublication{}, err
			}
		}
		if err := s.removeActivationIfExactUnlocked(activation); err != nil {
			return BatchPublication{}, err
		}
		return publication, nil
	}
	if err := s.publishActivationUnlocked(activation); err != nil {
		return BatchPublication{}, err
	}
	if s.batchCut != nil {
		if err := s.batchCut("activation-published", len(records)); err != nil {
			return BatchPublication{}, err
		}
	}
	if participant != nil {
		if err := participant.Finalize(publication); err != nil {
			return BatchPublication{}, err
		}
	}
	for index, binding := range activation.Records {
		markerPath := filepath.Join(s.dir(binding.RecordID), batchPendingFile)
		marker, exists, err := readBatchRecordMarker(markerPath)
		if err != nil {
			return BatchPublication{}, err
		}
		if exists {
			expected := batchMarkerForBinding(activation, binding)
			if marker != expected {
				return BatchPublication{}, ErrBatchVisibilityUnproved
			}
			if err := os.Remove(markerPath); err != nil {
				return BatchPublication{}, err
			}
			if err := syncEnvironmentDirectory(s.dir(binding.RecordID)); err != nil {
				return BatchPublication{}, err
			}
		}
		if s.batchCut != nil {
			if err := s.batchCut("record-finalized", index); err != nil {
				return BatchPublication{}, err
			}
		}
	}
	if err := s.removeActivationIfExactUnlocked(activation); err != nil {
		return BatchPublication{}, err
	}
	return publication, nil
}

// PreflightBatchWithParticipant proves that the exact environment/profile
// batch can be published without changing either catalog. Callers use it at a
// one-way effect boundary; PublishBatchWithParticipant repeats the preflight
// under its publication lock because another local actor may race the check.
func (s Store) PreflightBatchWithParticipant(
	batchID string,
	records []Record,
	participant BatchParticipant,
) (BatchPublication, error) {
	records = append([]Record(nil), records...)
	participantDigest := ""
	if participant != nil {
		var err error
		participantDigest, err = participant.BindingDigest()
		if err != nil || !validEnvironmentDigest(participantDigest) {
			return BatchPublication{}, errors.Join(ErrBatchConflict, err)
		}
	}
	activation, encodedRecords, err := buildBatchActivation(
		batchID, records, participantDigest,
	)
	if err != nil {
		return BatchPublication{}, err
	}
	publication := batchPublicationFromActivation(activation)
	lock, err := s.lockCatalog(false)
	if err != nil {
		return BatchPublication{}, err
	}
	defer lock.Unlock()
	if err := s.preflightBatchUnlocked(activation, records, encodedRecords); err != nil {
		return BatchPublication{}, err
	}
	if participant != nil {
		if err := participant.Preflight(publication); err != nil {
			return BatchPublication{}, err
		}
	}
	return publication, nil
}

func buildBatchActivation(
	batchID string,
	records []Record,
	participantDigest string,
) (batchActivation, [][]byte, error) {
	if !validBatchID(batchID) || len(records) == 0 || len(records) > 256 {
		return batchActivation{}, nil, ErrBatchConflict
	}
	recordsCopy := append([]Record(nil), records...)
	slices.SortFunc(recordsCopy, func(left, right Record) int {
		return strings.Compare(left.ID, right.ID)
	})
	copy(records, recordsCopy)
	encoded := make([][]byte, len(records))
	bindings := make([]batchRecordBinding, len(records))
	names := make(map[string]struct{}, len(records))
	for index, record := range records {
		data, err := marshalRecord(record)
		if err != nil {
			return batchActivation{}, nil, err
		}
		if index > 0 && records[index-1].ID == record.ID {
			return batchActivation{}, nil, ErrBatchConflict
		}
		name := strings.ToLower(record.Name)
		if _, duplicate := names[name]; duplicate {
			return batchActivation{}, nil, ErrBatchConflict
		}
		names[name] = struct{}{}
		encoded[index] = data
		bindings[index] = batchRecordBinding{
			RecordID: record.ID, RecordDigest: environmentBytesDigest(data),
		}
	}
	activation := batchActivation{
		Schema: batchActivationSchema, BatchID: batchID,
		ParticipantDigest: participantDigest, Records: bindings,
	}
	digest, err := batchActivationDigest(activation)
	if err != nil {
		return batchActivation{}, nil, err
	}
	activation.Digest = digest
	return activation, encoded, nil
}

func (s Store) preflightBatchUnlocked(
	activation batchActivation,
	records []Record,
	encoded [][]byte,
) error {
	intended := make(map[string]int, len(records))
	for index, record := range records {
		intended[record.ID] = index
		info, err := os.Lstat(s.dir(record.ID))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("%w: record %s path", ErrBatchConflict, record.ID)
		}
		if _, err := s.loadExact(record.ID); err != nil {
			return errors.Join(ErrBatchConflict, err)
		}
		if err := s.validateExistingBatchRecordUnlocked(
			activation, activation.Records[index], record, encoded[index],
		); err != nil {
			return err
		}
	}
	entries, err := os.ReadDir(s.environmentsDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, entry := range entries {
		if _, sameID := intended[entry.Name()]; sameID {
			continue
		}
		if !entry.IsDir() || !ValidID(entry.Name()) {
			continue
		}
		existing, err := s.loadExact(entry.Name())
		if err != nil {
			return err
		}
		for _, requested := range records {
			if strings.EqualFold(existing.Name, requested.Name) {
				return fmt.Errorf("%w: name %q", ErrBatchConflict, requested.Name)
			}
		}
	}
	return nil
}

func (s Store) validateExistingBatchRecordUnlocked(
	activation batchActivation,
	binding batchRecordBinding,
	expected Record,
	encoded []byte,
) error {
	origin, exists, err := readBatchRecordMarker(
		filepath.Join(s.dir(binding.RecordID), batchOriginFile),
	)
	if err != nil || !exists || origin != batchMarkerForBinding(activation, binding) {
		return fmt.Errorf("%w: record %s ownership", ErrBatchConflict, binding.RecordID)
	}
	pending, pendingExists, err := readBatchRecordMarker(
		filepath.Join(s.dir(binding.RecordID), batchPendingFile),
	)
	if err != nil {
		return err
	}
	if pendingExists {
		if pending != origin {
			return ErrBatchVisibilityUnproved
		}
		current, err := os.ReadFile(s.recordPath(binding.RecordID))
		if err != nil || !bytes.Equal(current, encoded) {
			return fmt.Errorf("%w: pending record %s changed", ErrBatchConflict, binding.RecordID)
		}
		return nil
	}
	current, err := s.loadExact(binding.RecordID)
	if err != nil || !batchImmutableRecordEqual(current, expected) {
		return fmt.Errorf("%w: committed record %s changed identity", ErrBatchConflict, binding.RecordID)
	}
	return nil
}

func (s Store) prepareBatchRecordUnlocked(
	record Record,
	encoded []byte,
	marker batchRecordMarker,
) error {
	parent := s.environmentsDir()
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".batching-"+record.ID+"-")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(staging)
		}
	}()
	if err := writeSynchronizedFile(filepath.Join(staging, recordFile), encoded); err != nil {
		return err
	}
	markerData, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	markerData = append(markerData, '\n')
	for _, name := range []string{batchOriginFile, batchPendingFile} {
		if err := writeSynchronizedFile(filepath.Join(staging, name), markerData); err != nil {
			return err
		}
	}
	if err := syncEnvironmentDirectory(staging); err != nil {
		return err
	}
	if err := os.Rename(staging, s.dir(record.ID)); err != nil {
		return err
	}
	published = true
	return syncEnvironmentDirectory(parent)
}

func (s Store) publishActivationUnlocked(activation batchActivation) error {
	dir := filepath.Join(s.environmentsDir(), batchActivationDirectory)
	if err := ensurePrivateEnvironmentDirectory(dir); err != nil {
		return err
	}
	path := s.activationPath(activation.BatchID)
	if current, exists, err := readBatchActivation(path); err != nil {
		return err
	} else if exists {
		if !reflect.DeepEqual(current, activation) {
			return ErrBatchConflict
		}
		return nil
	}
	data, err := json.Marshal(activation)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(dir, ".activation-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	keep := true
	defer func() {
		if keep {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if err := writeEnvironmentAll(temp, data); err != nil {
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
	keep = false
	return syncEnvironmentDirectory(dir)
}

func (s Store) batchAlreadyFinalizedUnlocked(
	activation batchActivation,
) (bool, error) {
	for _, binding := range activation.Records {
		_, exists, err := readBatchRecordMarker(
			filepath.Join(s.dir(binding.RecordID), batchPendingFile),
		)
		if err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
	}
	return true, nil
}

func (s Store) removeActivationIfExactUnlocked(activation batchActivation) error {
	path := s.activationPath(activation.BatchID)
	current, exists, err := readBatchActivation(path)
	if err != nil || !exists {
		return err
	}
	if !reflect.DeepEqual(current, activation) {
		return ErrBatchConflict
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncEnvironmentDirectory(filepath.Dir(path))
}

func (s Store) loadBatchActivationsUnlocked() (map[string]batchActivation, error) {
	dir := filepath.Join(s.environmentsDir(), batchActivationDirectory)
	if info, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return map[string]batchActivation{}, nil
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return nil, ErrBatchVisibilityUnproved
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	activations := make(map[string]batchActivation, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		activation, exists, err := readBatchActivation(filepath.Join(dir, entry.Name()))
		if err != nil || !exists {
			return nil, errors.Join(ErrBatchVisibilityUnproved, err)
		}
		if entry.Name() != activation.BatchID+".json" {
			return nil, ErrBatchVisibilityUnproved
		}
		activations[activation.BatchID] = activation
	}
	for _, activation := range activations {
		if err := s.validateActiveBatchClosureUnlocked(activation); err != nil {
			return nil, err
		}
	}
	return activations, nil
}

// BatchPublicationVisible proves that the exact activation marker is present
// and that every environment record in its closure still matches its digest.
// Participants use this only after releasing their own catalog locks.
func (s Store) BatchPublicationVisible(
	publication BatchPublication,
) (bool, error) {
	if err := publication.Validate(); err != nil {
		return false, err
	}
	lock, err := s.lockCatalog(false)
	if err != nil {
		return false, err
	}
	defer lock.Unlock()
	if lock.snapshotAbsent {
		return false, nil
	}
	activation, exists, err := readBatchActivation(s.activationPath(publication.BatchID))
	if err != nil || !exists {
		return false, err
	}
	if !reflect.DeepEqual(batchPublicationFromActivation(activation), publication) {
		return false, ErrBatchVisibilityUnproved
	}
	if err := s.validateActiveBatchClosureUnlocked(activation); err != nil {
		return false, err
	}
	return true, nil
}

func (s Store) validateActiveBatchClosureUnlocked(activation batchActivation) error {
	for _, binding := range activation.Records {
		expected := batchMarkerForBinding(activation, binding)
		origin, exists, err := readBatchRecordMarker(
			filepath.Join(s.dir(binding.RecordID), batchOriginFile),
		)
		if err != nil || !exists || origin != expected {
			return errors.Join(ErrBatchVisibilityUnproved, err)
		}
		pending, pendingExists, err := readBatchRecordMarker(
			filepath.Join(s.dir(binding.RecordID), batchPendingFile),
		)
		if err != nil || pendingExists && pending != expected {
			return errors.Join(ErrBatchVisibilityUnproved, err)
		}
		data, err := os.ReadFile(s.recordPath(binding.RecordID))
		if err != nil || environmentBytesDigest(data) != binding.RecordDigest {
			return errors.Join(ErrBatchVisibilityUnproved, err)
		}
	}
	return nil
}

func (s Store) batchRecordVisibleUnlocked(
	id string,
	activations map[string]batchActivation,
) (bool, error) {
	marker, exists, err := readBatchRecordMarker(
		filepath.Join(s.dir(id), batchPendingFile),
	)
	if err != nil || !exists {
		return !exists, err
	}
	activation, active := activations[marker.BatchID]
	if !active || marker.BatchDigest != activation.Digest {
		return false, nil
	}
	found := false
	for _, binding := range activation.Records {
		if binding.RecordID == id {
			found = binding.RecordDigest == marker.RecordDigest &&
				marker == batchMarkerForBinding(activation, binding)
			break
		}
	}
	if !found {
		return false, ErrBatchVisibilityUnproved
	}
	data, err := os.ReadFile(s.recordPath(id))
	if err != nil || environmentBytesDigest(data) != marker.RecordDigest {
		return false, errors.Join(ErrBatchVisibilityUnproved, err)
	}
	return true, nil
}

func (s Store) pendingBatchMutationCheckUnlocked(id string) error {
	_, exists, err := readBatchRecordMarker(filepath.Join(s.dir(id), batchPendingFile))
	if err != nil {
		return err
	}
	if exists {
		return ErrBatchFinalizationRequired
	}
	origin, originExists, err := readBatchRecordMarker(
		filepath.Join(s.dir(id), batchOriginFile),
	)
	if err != nil {
		return err
	}
	if originExists {
		_, activationExists, err := readBatchActivation(s.activationPath(origin.BatchID))
		if err != nil {
			return err
		}
		if activationExists {
			return ErrBatchFinalizationRequired
		}
	}
	return nil
}

func (s Store) lockCatalog(exclusive bool) (*Lock, error) {
	if strings.TrimSpace(s.Root) == "" {
		return nil, errors.New("environment store root is required")
	}
	dir := s.environmentsDir()
	if exclusive {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(dir)
	if !exclusive && errors.Is(err, os.ErrNotExist) {
		return &Lock{snapshotAbsent: true}, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("environment catalog directory must be real")
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
	return &Lock{file: file}, nil
}

func (activation batchActivation) validate() error {
	if activation.Schema != batchActivationSchema || !validBatchID(activation.BatchID) ||
		len(activation.Records) == 0 || len(activation.Records) > 256 ||
		!validEnvironmentDigest(activation.Digest) ||
		(activation.ParticipantDigest != "" &&
			!validEnvironmentDigest(activation.ParticipantDigest)) {
		return ErrBatchVisibilityUnproved
	}
	for index, binding := range activation.Records {
		if !ValidID(binding.RecordID) || !validEnvironmentDigest(binding.RecordDigest) ||
			(index > 0 && activation.Records[index-1].RecordID >= binding.RecordID) {
			return ErrBatchVisibilityUnproved
		}
	}
	digest, err := batchActivationDigest(activation)
	if err != nil || digest != activation.Digest {
		return ErrBatchVisibilityUnproved
	}
	return nil
}

func (marker batchRecordMarker) validate() error {
	if marker.Schema != batchRecordMarkerSchema || !validBatchID(marker.BatchID) ||
		!ValidID(marker.RecordID) || !validEnvironmentDigest(marker.BatchDigest) ||
		!validEnvironmentDigest(marker.RecordDigest) {
		return ErrBatchVisibilityUnproved
	}
	return nil
}

func batchActivationDigest(activation batchActivation) (string, error) {
	activation.Digest = ""
	data, err := json.Marshal(activation)
	if err != nil {
		return "", err
	}
	return environmentBytesDigest(append([]byte("hideout-environment-batch/v1\x00"), data...)), nil
}

func batchPublicationFromActivation(activation batchActivation) BatchPublication {
	ids := make([]string, len(activation.Records))
	for index, binding := range activation.Records {
		ids[index] = binding.RecordID
	}
	return BatchPublication{
		BatchID: activation.BatchID, Digest: activation.Digest,
		ParticipantDigest: activation.ParticipantDigest, RecordIDs: ids,
	}
}

func batchMarkerForBinding(
	activation batchActivation,
	binding batchRecordBinding,
) batchRecordMarker {
	return batchRecordMarker{
		Schema: batchRecordMarkerSchema, BatchID: activation.BatchID,
		BatchDigest: activation.Digest, RecordID: binding.RecordID,
		RecordDigest: binding.RecordDigest,
	}
}

func readBatchActivation(path string) (batchActivation, bool, error) {
	var activation batchActivation
	exists, err := readBatchJSON(path, &activation)
	if err != nil || !exists {
		return batchActivation{}, exists, err
	}
	if err := activation.validate(); err != nil {
		return batchActivation{}, false, err
	}
	return activation, true, nil
}

func readBatchRecordMarker(path string) (batchRecordMarker, bool, error) {
	var marker batchRecordMarker
	exists, err := readBatchJSON(path, &marker)
	if err != nil || !exists {
		return batchRecordMarker{}, exists, err
	}
	if err := marker.validate(); err != nil {
		return batchRecordMarker{}, false, err
	}
	return marker, true, nil
}

func readBatchJSON(path string, destination any) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > maxBatchMetadataBytes {
		return false, ErrBatchVisibilityUnproved
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return false, ErrBatchVisibilityUnproved
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return false, ErrBatchVisibilityUnproved
	}
	return true, nil
}

func writeSynchronizedFile(path string, data []byte) (err error) {
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
	if err := writeEnvironmentAll(file, data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	err = file.Close()
	closed = true
	return err
}

func writeEnvironmentAll(file *os.File, data []byte) error {
	for len(data) > 0 {
		written, err := file.Write(data)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	return nil
}

func ensurePrivateEnvironmentDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 ||
		info.Mode().Perm()&0o077 != 0 {
		return ErrBatchVisibilityUnproved
	}
	return nil
}

func syncEnvironmentDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func environmentBytesDigest(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validEnvironmentDigest(value string) bool {
	return strings.HasPrefix(value, "sha256:") &&
		isLowerHex(strings.TrimPrefix(value, "sha256:"), 64)
}

func validBatchID(value string) bool {
	if len(value) < 3 || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' ||
			char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func batchImmutableRecordEqual(left, right Record) bool {
	for _, record := range []*Record{&left, &right} {
		record.Status = ""
		record.LastSessionID = ""
		record.LastCommand = ""
		record.LastStartedAt = record.CreatedAt
		record.LastEndedAt = record.CreatedAt
	}
	return reflect.DeepEqual(left, right)
}

func (s Store) activationPath(batchID string) string {
	return filepath.Join(s.environmentsDir(), batchActivationDirectory, batchID+".json")
}
