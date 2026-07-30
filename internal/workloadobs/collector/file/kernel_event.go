package file

import (
	"bytes"
	"errors"
	"math"
	"path"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	observerbpf "github.com/vibe-agi/hideout/internal/workloadobs/collector/bpf"
	workloadtypes "github.com/vibe-agi/hideout/internal/workloadobs/types"
)

var (
	ErrKernelFileMetadata = errors.New("workload observer kernel file metadata is invalid")
	ErrExecutionUnknown   = errors.New("file event execution identity is unknown")
)

type ClockAnchor struct {
	WallTime    time.Time
	MonotonicNS uint64
}

func (anchor ClockAnchor) Validate() error {
	if anchor.WallTime.IsZero() || anchor.MonotonicNS == 0 {
		return ErrInvalidEvent
	}
	return nil
}

type ExecutionLookup func(executionPID uint32, execSequence uint64) (string, bool)
type PathClassifier func(filePath string) string

func EventFromKernelRecord(
	boundary Boundary,
	anchor ClockAnchor,
	coverageID string,
	raw observerbpf.RawFileEvent,
	executionLookup ExecutionLookup,
	classify PathClassifier,
) (Event, error) {
	if err := boundary.Validate(); err != nil {
		return Event{}, err
	}
	if err := anchor.Validate(); err != nil {
		return Event{}, err
	}
	if raw.CgroupID != boundary.CgroupID {
		return Event{}, ErrBoundaryMismatch
	}
	if raw.Reserved != 0 || raw.Flags&^knownKernelFileFlags() != 0 ||
		raw.Kind < observerbpf.FileEventOpen ||
		raw.Kind > observerbpf.FileEventRmdir ||
		raw.FileType > observerbpf.FileTypeDevice ||
		!validKernelPID(raw.PID) || !validKernelPID(raw.TID) ||
		raw.ObserverSequence == 0 ||
		raw.MonotonicNS < anchor.MonotonicNS ||
		raw.Result < -4095 {
		return Event{}, ErrInvalidEvent
	}

	pathValue, pathTerminated, pathClean := fixedKernelFileString(raw.Path[:])
	pathName, pathNameTerminated, pathNameClean := fixedKernelFileString(raw.PathName[:])
	targetValue, targetTerminated, targetClean := fixedKernelFileString(raw.TargetPath[:])
	targetName, targetNameTerminated, targetNameClean := fixedKernelFileString(raw.TargetName[:])
	if !pathClean || !pathNameClean || !targetClean || !targetNameClean ||
		!validKernelFileUTF8(pathValue, pathName, targetValue, targetName) ||
		(!pathTerminated && raw.Flags&observerbpf.FileFlagPathTruncated == 0) ||
		(!pathNameTerminated && raw.Flags&observerbpf.FileFlagPathTruncated == 0) ||
		(!targetTerminated && raw.Flags&observerbpf.FileFlagTargetTruncated == 0) ||
		(!targetNameTerminated && raw.Flags&observerbpf.FileFlagTargetTruncated == 0) {
		return Event{}, ErrKernelFileMetadata
	}
	if raw.Flags&observerbpf.FileFlagPathUnavailable != 0 && pathValue != "" {
		return Event{}, ErrKernelFileMetadata
	}
	if raw.Flags&observerbpf.FileFlagTargetUnavailable != 0 &&
		targetValue != "" {
		return Event{}, ErrKernelFileMetadata
	}

	kind, targetKind, dataKind, err := kernelFileKind(raw.Kind)
	if err != nil {
		return Event{}, err
	}
	if err := validateKernelFileShape(
		raw.Kind, pathName, targetValue, targetName,
		raw.Flags&observerbpf.FileFlagTargetUnavailable != 0,
	); err != nil {
		return Event{}, err
	}

	filePath := joinKernelFilePath(pathValue, pathName)
	targetPath := joinKernelFilePath(targetValue, targetName)
	if filePath == "" &&
		raw.Flags&(observerbpf.FileFlagPathUnavailable|
			observerbpf.FileFlagPathTruncated) == 0 {
		return Event{}, ErrKernelFileMetadata
	}
	pathState := kernelPathState(filePath, raw.Flags)
	if pathState == "unknown" {
		filePath = ""
	}
	if targetKind && targetPath == "" &&
		raw.Flags&observerbpf.FileFlagTargetUnavailable == 0 {
		return Event{}, ErrKernelFileMetadata
	}

	limitations := kernelFileLimitations(raw.Flags)
	if raw.Flags&observerbpf.FileFlagIdentityUnavailable != 0 {
		if raw.Device != 0 || raw.Inode != 0 || raw.MountID != 0 ||
			(raw.FileType != observerbpf.FileTypeUnknown &&
				!((raw.Kind == observerbpf.FileEventMkdir ||
					raw.Kind == observerbpf.FileEventRmdir) &&
					raw.FileType == observerbpf.FileTypeDirectory)) {
			return Event{}, ErrKernelFileMetadata
		}
	} else if raw.Inode == 0 {
		return Event{}, ErrKernelFileMetadata
	}

	outcome, err := kernelFileOutcome(raw)
	if err != nil {
		return Event{}, err
	}
	if err := validateKernelFileBytes(raw, dataKind); err != nil {
		return Event{}, err
	}

	attribution := workloadtypes.AttributionExact
	actor := workloadtypes.Actor{}
	switch {
	case validKernelPID(raw.ExecutionPID) && raw.ExecSequence != 0:
		if executionLookup == nil {
			return Event{}, ErrExecutionUnknown
		}
		executionID, ok := executionLookup(raw.ExecutionPID, raw.ExecSequence)
		if !ok {
			return Event{}, ErrExecutionUnknown
		}
		actor = workloadtypes.Actor{
			ExecutionID: executionID,
			PID:         raw.PID, UID: raw.UID, GID: raw.GID,
		}
	case raw.ExecutionPID == 0 && raw.ExecSequence == 0 &&
		raw.Flags&observerbpf.FileFlagStateUnavailable != 0:
		attribution = workloadtypes.AttributionUnknown
	default:
		return Event{}, ErrKernelFileMetadata
	}

	delta := raw.MonotonicNS - anchor.MonotonicNS
	if delta > math.MaxInt64 {
		return Event{}, ErrInvalidEvent
	}
	at := anchor.WallTime.Add(time.Duration(delta))
	pathClass := "unknown"
	if classify != nil && filePath != "" {
		pathClass = classify(filePath)
	}
	event := Event{
		Kind:  kind,
		Owner: boundary.Owner, SessionID: boundary.SessionID,
		CgroupID:           boundary.CgroupID,
		ObserverGeneration: boundary.ObserverGeneration,
		Sequence:           raw.ObserverSequence, CPU: uint64(raw.CPU),
		MonotonicNS: raw.MonotonicNS, At: at,
		Actor: actor, Attribution: attribution,
		Path: filePath, TargetPath: targetPath,
		PathState: pathState, PathClass: pathClass,
		FileType: kernelFileType(raw.FileType),
		Device:   raw.Device, Inode: raw.Inode, MountID: raw.MountID,
		Bytes: raw.Bytes, Outcome: outcome,
		CoverageID: coverageID, Limitations: limitations,
	}
	return event, nil
}

func knownKernelFileFlags() uint32 {
	return observerbpf.FileFlagPathTruncated |
		observerbpf.FileFlagPathUnavailable |
		observerbpf.FileFlagPathAliased |
		observerbpf.FileFlagTargetTruncated |
		observerbpf.FileFlagTargetUnavailable |
		observerbpf.FileFlagIdentityUnavailable |
		observerbpf.FileFlagBytesUnavailable |
		observerbpf.FileFlagStateUnavailable |
		observerbpf.FileFlagOutcomeUnknown |
		observerbpf.FileFlagAuthorizationHook
}

func kernelFileKind(raw uint32) (EventKind, bool, bool, error) {
	switch raw {
	case observerbpf.FileEventOpen:
		return EventOpen, false, false, nil
	case observerbpf.FileEventRead:
		return EventRead, false, true, nil
	case observerbpf.FileEventWrite:
		return EventWrite, false, true, nil
	case observerbpf.FileEventMmap:
		return EventMmap, false, true, nil
	case observerbpf.FileEventCreate:
		return EventCreate, false, false, nil
	case observerbpf.FileEventTruncate:
		return EventTruncate, false, false, nil
	case observerbpf.FileEventRename:
		return EventRename, true, false, nil
	case observerbpf.FileEventUnlink:
		return EventUnlink, false, false, nil
	case observerbpf.FileEventMetadata:
		return EventMetadata, false, false, nil
	case observerbpf.FileEventHardlink:
		return EventHardlink, true, false, nil
	case observerbpf.FileEventSymlink:
		return EventSymlink, true, false, nil
	case observerbpf.FileEventMkdir:
		return EventMkdir, false, false, nil
	case observerbpf.FileEventRmdir:
		return EventRmdir, false, false, nil
	default:
		return "", false, false, ErrInvalidEvent
	}
}

func validateKernelFileShape(
	kind uint32,
	pathName, targetPath, targetName string,
	targetUnavailable bool,
) error {
	switch kind {
	case observerbpf.FileEventOpen,
		observerbpf.FileEventRead,
		observerbpf.FileEventWrite,
		observerbpf.FileEventMmap,
		observerbpf.FileEventTruncate,
		observerbpf.FileEventMetadata:
		if pathName != "" || targetPath != "" || targetName != "" {
			return ErrKernelFileMetadata
		}
	case observerbpf.FileEventCreate:
		if targetPath != "" || targetName != "" {
			return ErrKernelFileMetadata
		}
	case observerbpf.FileEventUnlink,
		observerbpf.FileEventMkdir,
		observerbpf.FileEventRmdir:
		if pathName == "" || targetPath != "" || targetName != "" {
			return ErrKernelFileMetadata
		}
	case observerbpf.FileEventRename:
		if pathName == "" ||
			(!targetUnavailable && (targetPath == "" || targetName == "")) {
			return ErrKernelFileMetadata
		}
	case observerbpf.FileEventHardlink:
		if pathName == "" ||
			(!targetUnavailable && (targetPath == "" || targetName == "")) {
			return ErrKernelFileMetadata
		}
	case observerbpf.FileEventSymlink:
		if pathName == "" ||
			(!targetUnavailable && targetPath == "") ||
			targetName != "" {
			return ErrKernelFileMetadata
		}
	default:
		return ErrKernelFileMetadata
	}
	return nil
}

func validateKernelFileBytes(raw observerbpf.RawFileEvent, dataKind bool) error {
	bytesUnavailable := raw.Flags&observerbpf.FileFlagBytesUnavailable != 0
	switch raw.Kind {
	case observerbpf.FileEventRead, observerbpf.FileEventWrite:
		if bytesUnavailable ||
			(raw.Result < 0 && raw.Bytes != 0) ||
			(raw.Result >= 0 && raw.Bytes != uint64(raw.Result)) {
			return ErrKernelFileMetadata
		}
	case observerbpf.FileEventMmap:
		if bytesUnavailable && raw.Bytes != 0 {
			return ErrKernelFileMetadata
		}
	default:
		if dataKind || bytesUnavailable || raw.Bytes != 0 {
			return ErrKernelFileMetadata
		}
	}
	return nil
}

func kernelFileOutcome(raw observerbpf.RawFileEvent) (workloadtypes.Outcome, error) {
	unknown := raw.Flags&observerbpf.FileFlagOutcomeUnknown != 0
	authorization := raw.Flags&observerbpf.FileFlagAuthorizationHook != 0
	if unknown {
		if raw.Result != 0 || !authorization {
			return workloadtypes.Outcome{}, ErrKernelFileMetadata
		}
		return workloadtypes.Outcome{
			Status: workloadtypes.OutcomeUnknown,
			Reason: "post-hook-outcome-unavailable",
		}, nil
	}
	if raw.Result > 0 &&
		raw.Kind != observerbpf.FileEventRead &&
		raw.Kind != observerbpf.FileEventWrite {
		return workloadtypes.Outcome{}, ErrKernelFileMetadata
	}
	if raw.Result < 0 {
		code := int(-raw.Result)
		status := workloadtypes.OutcomeFailed
		if authorization {
			status = workloadtypes.OutcomeDenied
		}
		return workloadtypes.Outcome{Status: status, Code: &code}, nil
	}
	return workloadtypes.Outcome{Status: workloadtypes.OutcomeSucceeded}, nil
}

func kernelFileLimitations(flags uint32) []string {
	result := make([]string, 0, 8)
	if flags&observerbpf.FileFlagBytesUnavailable != 0 {
		result = append(result, "bytes-unavailable")
	}
	if flags&observerbpf.FileFlagIdentityUnavailable != 0 {
		result = append(result, "identity-unavailable")
	}
	if flags&observerbpf.FileFlagOutcomeUnknown != 0 {
		result = append(result, "outcome-unavailable")
	}
	if flags&observerbpf.FileFlagPathTruncated != 0 {
		result = append(result, "path-truncated")
	}
	if flags&observerbpf.FileFlagPathUnavailable != 0 {
		result = append(result, "path-unavailable")
	}
	if flags&observerbpf.FileFlagStateUnavailable != 0 {
		result = append(result, "state-unavailable")
	}
	if flags&observerbpf.FileFlagTargetTruncated != 0 {
		result = append(result, "target-path-truncated")
	}
	if flags&observerbpf.FileFlagTargetUnavailable != 0 {
		result = append(result, "target-path-unavailable")
	}
	sort.Strings(result)
	return result
}

func kernelPathState(filePath string, flags uint32) string {
	switch {
	case filePath == "":
		return "unknown"
	case flags&observerbpf.FileFlagPathAliased != 0:
		return "aliased"
	case flags&(observerbpf.FileFlagPathTruncated|
		observerbpf.FileFlagPathUnavailable) != 0:
		return "truncated"
	case !path.IsAbs(filePath):
		return "aliased"
	default:
		return "resolved"
	}
}

func kernelFileType(value uint32) string {
	switch value {
	case observerbpf.FileTypeRegular:
		return "regular"
	case observerbpf.FileTypeDirectory:
		return "directory"
	case observerbpf.FileTypeSymlink:
		return "symlink"
	case observerbpf.FileTypeSocket:
		return "socket"
	case observerbpf.FileTypeFIFO:
		return "fifo"
	case observerbpf.FileTypeDevice:
		return "device"
	default:
		return "unknown"
	}
}

func fixedKernelFileString(value []byte) (string, bool, bool) {
	if index := bytes.IndexByte(value, 0); index >= 0 {
		return string(value[:index]), true, allKernelFileZero(value[index+1:])
	}
	return string(value), false, true
}

func allKernelFileZero(value []byte) bool {
	for _, current := range value {
		if current != 0 {
			return false
		}
	}
	return true
}

func validKernelFileUTF8(values ...string) bool {
	for _, value := range values {
		if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
			return false
		}
	}
	return true
}

func joinKernelFilePath(directory, name string) string {
	switch {
	case name == "":
		return directory
	case directory == "":
		return name
	case directory == "/":
		return "/" + name
	case strings.HasSuffix(directory, "/"):
		return directory + name
	default:
		return directory + "/" + name
	}
}

func validKernelPID(value uint32) bool {
	return value > 0 && value <= 4194304
}

func NewPathClassifier(
	workspaceRoots, hostFSRoots, profileRoots, runtimeRoots []string,
) PathClassifier {
	roots := []struct {
		class string
		paths []string
	}{
		{class: "workspace", paths: cleanClassifierRoots(workspaceRoots)},
		{class: "hostfs", paths: cleanClassifierRoots(hostFSRoots)},
		{class: "profile", paths: cleanClassifierRoots(profileRoots)},
		{class: "runtime", paths: cleanClassifierRoots(runtimeRoots)},
	}
	return func(filePath string) string {
		if !path.IsAbs(filePath) {
			return "unknown"
		}
		for _, group := range roots {
			for _, root := range group.paths {
				if filePath == root || strings.HasPrefix(filePath, root+"/") {
					return group.class
				}
			}
		}
		switch {
		case filePath == "/etc" || strings.HasPrefix(filePath, "/etc/"),
			filePath == "/usr" || strings.HasPrefix(filePath, "/usr/"),
			filePath == "/bin" || strings.HasPrefix(filePath, "/bin/"),
			filePath == "/sbin" || strings.HasPrefix(filePath, "/sbin/"),
			filePath == "/lib" || strings.HasPrefix(filePath, "/lib/"),
			filePath == "/lib64" || strings.HasPrefix(filePath, "/lib64/"):
			return "system"
		default:
			return "external"
		}
	}
}

func cleanClassifierRoots(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		clean := path.Clean(strings.TrimSpace(value))
		if path.IsAbs(clean) && clean != "/" {
			result = append(result, strings.TrimSuffix(clean, "/"))
		}
	}
	sort.Strings(result)
	return result
}
