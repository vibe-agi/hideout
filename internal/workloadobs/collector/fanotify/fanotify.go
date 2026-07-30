package fanotify

import (
	"errors"
	"sort"
	"strings"
	"time"

	filecollector "github.com/vibe-agi/hideout/internal/workloadobs/collector/file"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

const (
	MaskAccess        uint64 = 0x00000001
	MaskModify        uint64 = 0x00000002
	MaskCloseWrite    uint64 = 0x00000008
	MaskOpen          uint64 = 0x00000020
	MaskQueueOverflow uint64 = 0x00004000
	MaskOnDirectory   uint64 = 0x40000000
)

var (
	ErrUnsupportedMask = errors.New("fanotify mask is unsupported")
	ErrInvalidEvent    = errors.New("fanotify file event is invalid")
)

type RawEvent struct {
	Kind     filecollector.EventKind
	Sequence uint64
	At       time.Time
	PID      uint32

	Actor         workloadtypes.Actor
	ActorResolved bool

	Path      string
	PathState string
	FileType  string
	Device    uint64
	Inode     uint64
	MountID   uint64
}

type Normalizer struct {
	boundary   filecollector.Boundary
	coverageID string
	classify   filecollector.PathClassifier
	validate   *filecollector.Normalizer
}

func NewNormalizer(
	boundary filecollector.Boundary,
	coverageID string,
	classify filecollector.PathClassifier,
) (*Normalizer, error) {
	validator, err := filecollector.NewNormalizer(boundary)
	if err != nil {
		return nil, err
	}
	if !validCoverageID(coverageID) {
		return nil, ErrInvalidEvent
	}
	return &Normalizer{
		boundary: boundary, coverageID: coverageID,
		classify: classify, validate: validator,
	}, nil
}

func (normalizer *Normalizer) Normalize(raw RawEvent) (filecollector.Event, error) {
	if normalizer == nil || normalizer.validate == nil ||
		raw.Sequence == 0 || raw.At.IsZero() ||
		raw.PID == 0 || raw.PID > 4194304 ||
		!supportedKind(raw.Kind) ||
		(raw.Inode == 0 && (raw.Device != 0 || raw.MountID != 0)) {
		return filecollector.Event{}, ErrInvalidEvent
	}

	limitations := []string{
		"bytes-unavailable",
		"fanotify-merged",
		"membership-receipt-pid",
		"mmap-unavailable",
		"outcome-unavailable",
		"timestamp-receipt",
	}
	attribution := workloadtypes.AttributionUnknown
	actor := workloadtypes.Actor{}
	if raw.ActorResolved {
		if raw.Actor.Validate() != nil || raw.Actor.PID != raw.PID {
			return filecollector.Event{}, ErrInvalidEvent
		}
		attribution = workloadtypes.AttributionInferred
		actor = raw.Actor
		limitations = append(limitations, "actor-inferred")
	} else {
		if raw.Actor != (workloadtypes.Actor{}) {
			return filecollector.Event{}, ErrInvalidEvent
		}
		limitations = append(limitations, "actor-unresolved")
	}
	switch raw.PathState {
	case "raced":
		limitations = append(limitations, "path-raced")
	case "truncated":
		limitations = append(limitations, "path-truncated")
	case "unknown":
		limitations = append(limitations, "path-unavailable")
	}
	if raw.Inode == 0 {
		limitations = append(limitations, "identity-unavailable")
	}
	sort.Strings(limitations)

	pathClass := "unknown"
	if normalizer.classify != nil && raw.Path != "" {
		pathClass = normalizer.classify(raw.Path)
	}
	event := filecollector.Event{
		Kind:  raw.Kind,
		Owner: normalizer.boundary.Owner, SessionID: normalizer.boundary.SessionID,
		CgroupID:           normalizer.boundary.CgroupID,
		ObserverGeneration: normalizer.boundary.ObserverGeneration,
		Sequence:           raw.Sequence,
		At:                 raw.At.UTC(),
		Actor:              actor,
		Attribution:        attribution,
		Path:               raw.Path,
		PathState:          raw.PathState,
		PathClass:          pathClass,
		FileType:           raw.FileType,
		Device:             raw.Device,
		Inode:              raw.Inode,
		MountID:            raw.MountID,
		Outcome: workloadtypes.Outcome{
			Status: workloadtypes.OutcomeUnknown,
			Reason: "fanotify-outcome-unavailable",
		},
		CoverageID:  normalizer.coverageID,
		Limitations: limitations,
	}
	if _, err := normalizer.validate.Normalize(event); err != nil {
		return filecollector.Event{}, errors.Join(ErrInvalidEvent, err)
	}
	return event, nil
}

func KindsForMask(mask uint64) ([]filecollector.EventKind, error) {
	operations := mask &^ MaskOnDirectory
	if operations == 0 ||
		operations&^(MaskOpen|MaskAccess|MaskModify) != 0 {
		return nil, ErrUnsupportedMask
	}
	result := make([]filecollector.EventKind, 0, 3)
	if operations&MaskOpen != 0 {
		result = append(result, filecollector.EventOpen)
	}
	if operations&MaskAccess != 0 {
		result = append(result, filecollector.EventRead)
	}
	if operations&MaskModify != 0 {
		result = append(result, filecollector.EventWrite)
	}
	return result, nil
}

func supportedKind(kind filecollector.EventKind) bool {
	switch kind {
	case filecollector.EventOpen,
		filecollector.EventRead,
		filecollector.EventWrite:
		return true
	default:
		return false
	}
}

func validCoverageID(value string) bool {
	if !strings.HasPrefix(value, "cov_") ||
		len(value) < len("cov_")+8 || len(value) > 128 {
		return false
	}
	for _, current := range value[len("cov_"):] {
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
