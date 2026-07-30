package file

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

type EventKind string

const (
	EventOpen     EventKind = "file.open"
	EventRead     EventKind = "file.read"
	EventWrite    EventKind = "file.write"
	EventMmap     EventKind = "file.mmap"
	EventCreate   EventKind = "file.create"
	EventTruncate EventKind = "file.truncate"
	EventRename   EventKind = "file.rename"
	EventUnlink   EventKind = "file.unlink"
	EventMetadata EventKind = "file.metadata"
	EventHardlink EventKind = "file.hardlink"
	EventSymlink  EventKind = "file.symlink"
	EventMkdir    EventKind = "file.mkdir"
	EventRmdir    EventKind = "file.rmdir"
)

var (
	ErrBoundaryMismatch = errors.New("file event does not match the workload boundary")
	ErrInvalidEvent     = errors.New("file event is invalid")
)

type Boundary struct {
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	CgroupID           uint64
	ObserverGeneration uint64
}

func (boundary Boundary) Validate() error {
	if boundary.Owner.Validate() != nil ||
		!validSessionID(boundary.SessionID) ||
		boundary.CgroupID == 0 ||
		boundary.ObserverGeneration == 0 {
		return ErrBoundaryMismatch
	}
	return nil
}

type Event struct {
	Kind               EventKind
	Owner              workloadtypes.ActivityOwner
	SessionID          string
	CgroupID           uint64
	ObserverGeneration uint64
	Sequence           uint64
	CPU                uint64
	MonotonicNS        uint64
	At                 time.Time

	Actor       workloadtypes.Actor
	Attribution string
	Path        string
	TargetPath  string
	PathState   string
	PathClass   string
	FileType    string
	Device      uint64
	Inode       uint64
	MountID     uint64
	Bytes       uint64
	Outcome     workloadtypes.Outcome
	CoverageID  string
	Limitations []string
}

type Normalizer struct {
	boundary Boundary
}

func NewNormalizer(boundary Boundary) (*Normalizer, error) {
	if err := boundary.Validate(); err != nil {
		return nil, err
	}
	return &Normalizer{boundary: boundary}, nil
}

func (normalizer *Normalizer) Normalize(event Event) (workloadtypes.ActivityRecord, error) {
	if normalizer == nil {
		return workloadtypes.ActivityRecord{}, ErrInvalidEvent
	}
	if !event.Owner.Equal(normalizer.boundary.Owner) ||
		event.SessionID != normalizer.boundary.SessionID ||
		event.CgroupID != normalizer.boundary.CgroupID ||
		event.ObserverGeneration != normalizer.boundary.ObserverGeneration {
		return workloadtypes.ActivityRecord{}, ErrBoundaryMismatch
	}
	operation, destructive, targetKind, ok := eventKind(event.Kind)
	if !ok ||
		event.Sequence == 0 ||
		event.At.IsZero() ||
		event.Outcome.Validate() != nil ||
		!validFileMetadata(event, targetKind) ||
		!validLimitations(event.Limitations) {
		return workloadtypes.ActivityRecord{}, ErrInvalidEvent
	}
	attribution := event.Attribution
	if attribution == "" {
		attribution = workloadtypes.AttributionExact
	}
	var actor *workloadtypes.Actor
	switch attribution {
	case workloadtypes.AttributionExact:
		if event.Actor.Validate() != nil {
			return workloadtypes.ActivityRecord{}, ErrInvalidEvent
		}
		value := event.Actor
		actor = &value
	case workloadtypes.AttributionInferred:
		if event.Actor.Validate() != nil ||
			!hasLimitation(event.Limitations, "actor-inferred") {
			return workloadtypes.ActivityRecord{}, ErrInvalidEvent
		}
		value := event.Actor
		actor = &value
	case workloadtypes.AttributionUnknown:
		if event.Actor != (workloadtypes.Actor{}) ||
			(!hasLimitation(event.Limitations, "state-unavailable") &&
				!hasLimitation(event.Limitations, "actor-unresolved")) {
			return workloadtypes.ActivityRecord{}, ErrInvalidEvent
		}
	default:
		return workloadtypes.ActivityRecord{}, ErrInvalidEvent
	}
	event.Attribution = attribution

	targetPath := ""
	if targetKind {
		targetPath = event.TargetPath
	}
	subject := workloadtypes.FileSubject{
		Kind: workloadtypes.ActivityFile,
		Path: event.Path, TargetPath: targetPath,
		PathState: event.PathState, PathClass: event.PathClass,
		FileType: event.FileType, Device: event.Device, Inode: event.Inode,
		MountID: event.MountID, Destructive: destructive,
	}
	record := workloadtypes.ActivityRecord{
		Schema: workloadtypes.ActivityRecordSchema,
		Owner:  event.Owner, SessionID: event.SessionID,
		Actor: actor,
		Kind:  workloadtypes.ActivityFile, Operation: operation,
		Subject: subject, Outcome: event.Outcome,
		Count: 1, Bytes: event.Bytes,
		FirstAt: event.At, LastAt: event.At,
		FirstSequence: event.Sequence, LastSequence: event.Sequence,
		Attribution: attribution,
		Truncation:  append([]string(nil), event.Limitations...),
		CoverageID:  event.CoverageID, RedactionStatus: workloadtypes.RedactionPending,
	}
	id, err := activityID(event, subject, operation)
	if err != nil {
		return workloadtypes.ActivityRecord{}, ErrInvalidEvent
	}
	record.ID = id
	if err := record.Validate(); err != nil {
		return workloadtypes.ActivityRecord{}, ErrInvalidEvent
	}
	return record, nil
}

func eventKind(kind EventKind) (operation string, destructive, target bool, ok bool) {
	switch kind {
	case EventOpen:
		return "open", false, false, true
	case EventRead:
		return "read", false, false, true
	case EventWrite:
		return "write", false, false, true
	case EventMmap:
		return "mmap", false, false, true
	case EventCreate:
		return "create", false, false, true
	case EventTruncate:
		return "truncate", true, false, true
	case EventRename:
		return "rename", true, true, true
	case EventUnlink:
		return "unlink", true, false, true
	case EventMetadata:
		return "metadata", false, false, true
	case EventHardlink:
		return "hardlink", false, true, true
	case EventSymlink:
		return "symlink", false, true, true
	case EventMkdir:
		return "mkdir", false, false, true
	case EventRmdir:
		return "rmdir", true, false, true
	default:
		return "", false, false, false
	}
}

func validFileMetadata(event Event, targetKind bool) bool {
	if len(event.Path) > 4096 || strings.IndexByte(event.Path, 0) >= 0 ||
		!utf8.ValidString(event.Path) ||
		len(event.TargetPath) > 4096 || strings.IndexByte(event.TargetPath, 0) >= 0 ||
		!utf8.ValidString(event.TargetPath) {
		return false
	}
	switch event.PathState {
	case "resolved":
		if event.Path == "" || !filepath.IsAbs(event.Path) {
			return false
		}
	case "aliased", "raced", "truncated":
		if event.Path == "" {
			return false
		}
	case "unknown":
		if event.Path != "" {
			return false
		}
	default:
		return false
	}
	switch event.PathClass {
	case "workspace", "hostfs", "profile", "runtime", "system", "external", "unknown":
	default:
		return false
	}
	switch event.FileType {
	case "regular", "directory", "symlink", "socket", "fifo", "device", "unknown":
	default:
		return false
	}
	if targetKind && event.TargetPath == "" &&
		!hasLimitation(event.Limitations, "target-path-unavailable") {
		return false
	}
	if !targetKind && event.TargetPath != "" {
		// The normalizer intentionally ignores irrelevant target fields, but
		// still validates their bounded encoding above.
	}
	if event.Inode == 0 {
		return hasLimitation(event.Limitations, "identity-unavailable")
	}
	if event.PathState == "unknown" &&
		!hasLimitation(event.Limitations, "path-unavailable") {
		return false
	}
	return true
}

func validLimitations(values []string) bool {
	if len(values) > 16 {
		return false
	}
	previous := ""
	for _, value := range values {
		switch value {
		case "actor-inferred", "actor-unresolved", "bytes-unavailable",
			"fanotify-merged", "identity-unavailable", "mmap-unavailable",
			"membership-receipt-pid", "outcome-unavailable",
			"path-raced", "path-truncated",
			"path-unavailable", "state-unavailable",
			"target-path-truncated", "target-path-unavailable",
			"timestamp-receipt":
		default:
			return false
		}
		if value <= previous {
			return false
		}
		previous = value
	}
	return true
}

func hasLimitation(values []string, expected string) bool {
	index := sort.SearchStrings(values, expected)
	return index < len(values) && values[index] == expected
}

func activityID(
	event Event,
	subject workloadtypes.FileSubject,
	operation string,
) (string, error) {
	payload := struct {
		Owner              workloadtypes.ActivityOwner `json:"owner"`
		SessionID          string                      `json:"sessionId"`
		CgroupID           uint64                      `json:"cgroupId"`
		ObserverGeneration uint64                      `json:"observerGeneration"`
		Sequence           uint64                      `json:"sequence"`
		At                 time.Time                   `json:"at"`
		Actor              workloadtypes.Actor         `json:"actor"`
		Attribution        string                      `json:"attribution"`
		Operation          string                      `json:"operation"`
		Subject            workloadtypes.FileSubject   `json:"subject"`
		Bytes              uint64                      `json:"bytes"`
		Outcome            workloadtypes.Outcome       `json:"outcome"`
		CoverageID         string                      `json:"coverageId"`
		Limitations        []string                    `json:"limitations,omitempty"`
	}{
		Owner: event.Owner, SessionID: event.SessionID,
		CgroupID: event.CgroupID, ObserverGeneration: event.ObserverGeneration,
		Sequence: event.Sequence, At: event.At.UTC(), Actor: event.Actor,
		Attribution: event.Attribution,
		Operation:   operation, Subject: subject, Bytes: event.Bytes,
		Outcome: event.Outcome, CoverageID: event.CoverageID,
		Limitations: event.Limitations,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("hideout.file-activity/v1\x00"), encoded...))
	return "act_" + base64.RawURLEncoding.EncodeToString(sum[:18]), nil
}

func validSessionID(value string) bool {
	if !strings.HasPrefix(value, "ses_") ||
		len(value) < 5 || len(value) > 128 {
		return false
	}
	for _, current := range value[4:] {
		if (current >= 'a' && current <= 'z') ||
			(current >= 'A' && current <= 'Z') ||
			(current >= '0' && current <= '9') ||
			current == '_' || current == '-' {
			continue
		}
		return false
	}
	return true
}
