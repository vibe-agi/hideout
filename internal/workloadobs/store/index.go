package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	workloadquery "github.com/vibe-agi/hideout/internal/workloadobs/query"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type SegmentIndex struct {
	Schema    string       `json:"schema"`
	SegmentID string       `json:"segmentId"`
	OwnerKey  string       `json:"ownerKey"`
	Entries   []IndexEntry `json:"entries"`
}

type IndexEntry struct {
	Ordinal     uint64    `json:"ordinal"`
	RecordID    string    `json:"recordId"`
	SessionID   string    `json:"sessionId"`
	Kind        string    `json:"kind"`
	Operation   string    `json:"operation"`
	ExecutionID string    `json:"executionId,omitempty"`
	Path        string    `json:"path,omitempty"`
	TargetPath  string    `json:"targetPath,omitempty"`
	Domain      string    `json:"domain,omitempty"`
	IP          string    `json:"ip,omitempty"`
	FirstAt     time.Time `json:"firstAt"`
	LastAt      time.Time `json:"lastAt"`
}

// NewQueryService binds the store's exact-owner snapshots to the shared signed
// cursor contract. Cursor payloads are authenticated and rejected when replayed
// against another owner, filter set, or store revision.
func (store *Store) NewQueryService(
	cursorKey []byte,
) (*workloadquery.Service, error) {
	if store == nil {
		return nil, workloadquery.ErrInvalidOptions
	}
	return workloadquery.NewService(workloadquery.Options{
		Source: store, CursorKey: cursorKey,
	})
}

func buildSegmentIndex(
	segmentID string,
	owner workloadtypes.ActivityOwner,
	entries []segmentEntry,
) (SegmentIndex, []byte, string, error) {
	index := SegmentIndex{
		Schema:    indexSchema,
		SegmentID: segmentID, OwnerKey: owner.Key(),
		Entries: make([]IndexEntry, 0, len(entries)),
	}
	for _, entry := range entries {
		if entry.Kind != entryActivity || entry.Activity == nil {
			continue
		}
		record := entry.Activity
		item := IndexEntry{
			Ordinal: uint64(len(index.Entries)), RecordID: record.ID,
			SessionID: record.SessionID, Kind: record.Kind,
			Operation: record.Operation, FirstAt: record.FirstAt.UTC(),
			LastAt: record.LastAt.UTC(),
		}
		if record.Actor != nil {
			item.ExecutionID = record.Actor.ExecutionID
		}
		switch subject := record.Subject.(type) {
		case workloadtypes.ProcessSubject:
			item.ExecutionID = subject.ExecutionID
		case *workloadtypes.ProcessSubject:
			if subject != nil {
				item.ExecutionID = subject.ExecutionID
			}
		case workloadtypes.FileSubject:
			item.Path, item.TargetPath = subject.Path, subject.TargetPath
		case *workloadtypes.FileSubject:
			if subject != nil {
				item.Path, item.TargetPath = subject.Path, subject.TargetPath
			}
		case workloadtypes.NetworkSubject:
			item.Domain = normalizeDomain(subject.Domain)
			item.IP = normalizeIP(subject.IP)
		case *workloadtypes.NetworkSubject:
			if subject != nil {
				item.Domain = normalizeDomain(subject.Domain)
				item.IP = normalizeIP(subject.IP)
			}
		case workloadtypes.DNSSubject:
			item.Domain = normalizeDomain(subject.Query)
			item.IP = firstAddress(subject.Answers)
		case *workloadtypes.DNSSubject:
			if subject != nil {
				item.Domain = normalizeDomain(subject.Query)
				item.IP = firstAddress(subject.Answers)
			}
		}
		index.Entries = append(index.Entries, item)
	}
	if err := validateSegmentIndex(index, segmentID, owner); err != nil {
		return SegmentIndex{}, nil, "", err
	}
	data, err := canonicalJSON(index)
	if err != nil {
		return SegmentIndex{}, nil, "", err
	}
	if len(data) > maxIndexEncodedBytes {
		return SegmentIndex{}, nil, "", ErrStoreCorrupt
	}
	sum := sha256.Sum256(data)
	return index, data, hex.EncodeToString(sum[:]), nil
}

func validateSegmentIndex(
	index SegmentIndex,
	segmentID string,
	owner workloadtypes.ActivityOwner,
) error {
	if index.Schema != indexSchema ||
		index.SegmentID != segmentID ||
		index.OwnerKey != owner.Key() ||
		len(index.Entries) > maxIndexEntries {
		return ErrStoreCorrupt
	}
	for ordinal, entry := range index.Entries {
		if entry.Ordinal != uint64(ordinal) ||
			entry.RecordID == "" || entry.SessionID == "" ||
			entry.Kind == "" || entry.Operation == "" ||
			entry.FirstAt.IsZero() || entry.LastAt.Before(entry.FirstAt) ||
			len(entry.ExecutionID) > 160 ||
			len(entry.Path) > 4096 || len(entry.TargetPath) > 4096 ||
			len(entry.Domain) > 253 || len(entry.IP) > 64 {
			return ErrStoreCorrupt
		}
	}
	return nil
}

func verifyOrRebuildIndex(
	ownerRoot string,
	manifest SegmentManifest,
	expected SegmentIndex,
	expectedData []byte,
	expectedDigest string,
) (bool, error) {
	if manifest.IndexDigest != expectedDigest {
		return false, ErrStoreCorrupt
	}
	path := indexPath(ownerRoot, manifest.ID)
	data, err := readPrivateFile(path, maxIndexEncodedBytes)
	if err == nil {
		sum := sha256.Sum256(data)
		var actual SegmentIndex
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		decodeErr := decoder.Decode(&actual)
		if hex.EncodeToString(sum[:]) == expectedDigest &&
			decodeErr == nil &&
			validateSegmentIndex(actual, manifest.ID, manifest.Owner) == nil &&
			reflect.DeepEqual(actual, expected) {
			return false, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		// Security failures are not repaired by replacing an attacker-controlled
		// path. Only a missing or malformed regular private index is rebuildable.
		return false, err
	}
	if err := writeAtomicBytes(
		filepath.Join(ownerRoot, indexDirectory),
		manifest.ID+indexSuffix,
		expectedData,
	); err != nil {
		return false, err
	}
	return true, nil
}

func normalizeDomain(value string) string {
	return strings.ToLower(strings.TrimSuffix(value, "."))
}

func normalizeIP(value string) string {
	address, err := netip.ParseAddr(value)
	if err != nil {
		return ""
	}
	return address.Unmap().String()
}

func firstAddress(values []string) string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if address := normalizeIP(value); address != "" {
			normalized = append(normalized, address)
		}
	}
	slices.Sort(normalized)
	if len(normalized) == 0 {
		return ""
	}
	return normalized[0]
}

func sortedIndexEntries(entries []IndexEntry) []IndexEntry {
	result := append([]IndexEntry(nil), entries...)
	sort.Slice(result, func(left, right int) bool {
		return result[left].Ordinal < result[right].Ordinal
	})
	return result
}
